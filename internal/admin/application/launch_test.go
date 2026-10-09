package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
)

// launchFlags is the flags as a launch has them or not.
type launchFlags struct{ list []ports.Flag }

func (f *launchFlags) List(context.Context) ([]ports.Flag, error) { return f.list, nil }

func (f *launchFlags) Switch(context.Context, string, bool, string, string) (ports.Flag, error) {
	return ports.Flag{}, errors.New("read-only")
}

func (f *launchFlags) History(context.Context, string, int) ([]ports.FlagChange, error) {
	return nil, nil
}

// launchWallet answers the custodian's state.
type launchWallet struct {
	fakeWallet
	custody string
}

func (w *launchWallet) Custody(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(w.custody), nil
}

// launchCatalog answers the platform coin's profile.
type launchCatalog struct {
	ports.Instruments
	logo string
}

func (c *launchCatalog) AssetProfile(_ context.Context, code string) (json.RawMessage, error) {
	return json.Marshal(map[string]any{"display_name": code, "logo_url": c.logo, "version": 2})
}

// List has three backed assets in pairs, a backed one in none (the
// custodian's test asset) and an internal one.
func (c *launchCatalog) List(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"assets":[
		{"asset_code":"USDT","deposit_enabled":true,"withdraw_enabled":true},{"asset_code":"BTC","deposit_enabled":true,"withdraw_enabled":true},
		{"asset_code":"ETH","deposit_enabled":false,"withdraw_enabled":true},{"asset_code":"TUSD","deposit_enabled":true,"withdraw_enabled":true},
		{"asset_code":"ASTRA","deposit_enabled":false,"withdraw_enabled":false}],
		"pairs":[{"base_asset":"BTC","quote_asset":"USDT"},{"base_asset":"ETH","quote_asset":"USDT"},{"base_asset":"ASTRA","quote_asset":"USDT"}]}`), nil
}

// launchLedger holds HOUSE's inventory (MARKET_MAKER).
type launchLedger struct {
	*fakeLedger
	house map[string]string
}

func (l *launchLedger) SystemBalances(context.Context, string) ([]ports.Balance, error) {
	out := []ports.Balance{{AccountType: "FEE_REVENUE", Asset: "USDT", Available: "7"}}
	for asset, v := range l.house {
		out = append(out, ports.Balance{AccountType: "MARKET_MAKER", Asset: asset, Available: v})
	}
	return out, nil
}

// launchContent answers the legal pages, or has none yet (D1).
type launchContent struct {
	ports.Content
	legal string
}

func (c *launchContent) Articles(_ context.Context, section string) (json.RawMessage, error) {
	if section != "LEGAL" || c.legal == "" {
		return nil, errors.New("section must be ANNOUNCEMENT or HELP")
	}
	return json.RawMessage(c.legal), nil
}

// launchProbe reports the third parties' configuration.
type launchProbe struct{ present map[string]bool }

func (p *launchProbe) Check(context.Context, bool) []ports.ServiceHealth {
	return []ports.ServiceHealth{{Service: "auth-service", Ready: true, ConfigPresent: p.present}, {Service: "ledger-service", Ready: true}}
}

func launchStatuses(c LaunchChecklist) map[string]string {
	out := map[string]string{}
	for _, it := range c.Items {
		out[it.Key] = it.Status
	}
	return out
}

// TestLaunchChecklist reads each item from its source: the test setup
// of the test server is red item by item; set as a launch needs it, every
// item is green and the platform ready.
func TestLaunchChecklist(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	auditor := h.login(t, "audit@example.com")
	flags := &launchFlags{list: []ports.Flag{
		{Key: "admin.login_without_totp", Enabled: true},
		{Key: "admin.two_person_approval", Enabled: true},
		{Key: "wallet.test_assets", Enabled: true, Rules: json.RawMessage(`{"regions":{"only":["AQ"]}}`)},
		{Key: "wallet.withdraw", Enabled: true},
		{Key: "ledger.welcome_credit", Enabled: true},
		{Key: "market.house_liquidity", Enabled: true},
	}}
	wallet := &launchWallet{custody: `{"provider":"UDUN","configured":true}`}
	catalog := &launchCatalog{}
	content := &launchContent{}
	probe := &launchProbe{present: map[string]bool{"turnstile": true, "mail": false}}
	pl := newFakePlatform()
	ledger := &launchLedger{fakeLedger: h.ledger, house: map[string]string{"USDT": "2000000", "BTC": "0"}}
	h.svc.Flags, h.svc.Wallet, h.svc.Catalog, h.svc.Content, h.svc.Probe, h.svc.Platform, h.svc.Ledger = flags, wallet, catalog, content, probe, pl, ledger
	apps := newFakeApps()
	h.svc.Apps, h.svc.AppFiles, h.svc.AppUploads = apps, newFakeAppFiles(func() time.Time { return h.now }), newMemUploads()

	// The test server: the test setup.
	c, err := h.svc.LaunchChecklist(ctx, auditor, "admin.astras.vip")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"welcome_credits": LaunchFail, "test_mode": LaunchFail, "registration": LaunchOK, "admin_totp": LaunchFail, "two_person": LaunchOK,
		"test_assets": LaunchFail, "custodian": LaunchPending, "withdraw": LaunchOK, "brand": LaunchFail, "coin_profile": LaunchFail,
		"legal": LaunchPending, "third_party": LaunchFail, "admins": LaunchFail, "domain": LaunchOK, "house": LaunchFail,
		"app_downloads": LaunchOK,
	}
	got := launchStatuses(c)
	for key, status := range want {
		if got[key] != status {
			t.Fatalf("%s is %s, want %s (%v)", key, got[key], status, got)
		}
	}
	if c.Ready || len(c.Items) != len(launchKeys) || c.Items[0].Key != "welcome_credits" {
		t.Fatalf("the test setup %+v", c)
	}
	if v := c.Items[slicesIndex(c, "admins")].Value; v["active_admins"] != 1 {
		t.Fatalf("the administrators %+v", v)
	}
	// The apps to download (H4): for information, nothing offered is OK,
	// with the download entries' switch (H5); unreadable is UNKNOWN.
	if v := c.Items[slicesIndex(c, "app_downloads")].Value; len(v) != 3 || v["android"] != nil || v["ios"] != nil || v["entry_visible"] != true {
		t.Fatalf("the apps %+v", v)
	}
	// What each platform offers, by its lower-case name (A77 ②): a link,
	// an app uploaded; a link switched off offers nothing.
	android, ios := apps.apps[domain.AppAndroid], apps.apps[domain.AppIOS]
	android.Mode, android.LinkURL, android.Enabled = "LINK", "https://example.com/astras.apk", true
	ios.Mode, ios.Enabled, ios.Current = "FILE", true, &ports.StoredAppFile{FileID: "0192a000-0000-7000-8000-000000000001", Kind: domain.AppKindApp}
	c2, err := h.svc.LaunchChecklist(ctx, auditor, "admin.astras.vip")
	if err != nil {
		t.Fatal(err)
	}
	if it := c2.Items[slicesIndex(c2, "app_downloads")]; it.Status != LaunchOK || it.Value["android"] != "LINK" || it.Value["ios"] != "FILE" {
		t.Fatalf("the apps offered %+v", it)
	}
	android.Enabled = false
	if c2, _ = h.svc.LaunchChecklist(ctx, auditor, "admin.astras.vip"); c2.Items[slicesIndex(c2, "app_downloads")].Value["android"] != nil {
		t.Fatalf("a link switched off %+v", c2.Items[slicesIndex(c2, "app_downloads")])
	}
	android.Mode, android.LinkURL, ios.Mode, ios.Enabled, ios.Current = "OFF", "", "OFF", false, nil
	apps.down = true
	if c, _ := h.svc.LaunchChecklist(ctx, auditor, "admin.astras.vip"); launchStatuses(c)["app_downloads"] != LaunchUnknown {
		t.Fatalf("the apps unreadable %v", launchStatuses(c))
	}
	apps.down = false
	// HOUSE holds no BTC and no ETH (no balance at all); the test asset of
	// no pair is not asked for.
	if v := c.Items[slicesIndex(c, "house")].Value["backed"].(map[string]string); len(v) != 3 || v["BTC"] != "0" || v["ETH"] != "0" || v["USDT"] != "2000000" {
		t.Fatalf("HOUSE %+v", v)
	}
	// The stand-in custodian is red, an unknown host waits.
	wallet.custody = `{"provider":"UDUN","configured":true,"gateway_host":"udun-mock"}`
	if c, _ := h.svc.LaunchChecklist(ctx, auditor, "admin.astras.vip"); launchStatuses(c)["custodian"] != LaunchFail {
		t.Fatalf("the stand-in %v", launchStatuses(c))
	}

	// Set as a launch needs it.
	flags.list = []ports.Flag{
		{Key: "admin.login_without_totp"},
		{Key: "admin.two_person_approval", Enabled: true},
		{Key: "wallet.test_assets"},
		{Key: "wallet.withdraw", Enabled: true},
		{Key: "ledger.welcome_credit", Enabled: true},
		{Key: "market.house_liquidity", Enabled: true},
	}
	wallet.custody = `{"provider":"UDUN","configured":true,"gateway_host":"sig10.udun.io"}`
	catalog.logo = "/v1/market/assets/ASTRA/logo?v=2"
	content.legal = `{"articles":[{"slug":"terms","status":"PUBLISHED"},{"slug":"privacy","status":"PUBLISHED"},{"slug":"risk","status":"PUBLISHED"},` +
		`{"slug":"fees","status":"DRAFT"}]}`
	probe.present = map[string]bool{"turnstile": true, "mail": true, "alchemy": true}
	pl.credits = nil
	pl.profile["test_mode"] = map[string]any{"enabled": false, "banner": true}
	pl.profile["images"] = map[string]any{"logo_light": "/v1/platform/images/logo_light?v=5", "logo_dark": nil, "favicon": "/v1/platform/images/favicon?v=5"}
	ledger.house = map[string]string{"USDT": "2000000", "BTC": "20", "ETH": "500"}
	h.admin(t, "second@example.com", domain.RoleAdmin)
	// Still the seeded name, in any case: the brand is not the platform's own yet.
	for _, seeded := range []string{"Astras", "ASTRAS "} {
		pl.profile["name"] = seeded
		if c, _ := h.svc.LaunchChecklist(ctx, auditor, "admin.astras.vip"); launchStatuses(c)["brand"] != LaunchFail || c.Ready {
			t.Fatalf("the seeded name %q %v", seeded, launchStatuses(c))
		}
	}
	pl.profile["name"] = "Nova"
	c, err = h.svc.LaunchChecklist(ctx, auditor, "admin.astras.vip:443")
	if err != nil || !c.Ready {
		t.Fatalf("set for a launch %v %v", launchStatuses(c), err)
	}

	// A legal page for test mode alone leaves the bundled draft live.
	content.legal = `{"articles":[{"slug":"terms","modes":"TEST","status":"PUBLISHED"},{"slug":"privacy","modes":"FORMAL","status":"PUBLISHED"},` +
		`{"slug":"risk","modes":"BOTH","status":"PUBLISHED"}]}`
	if c, _ := h.svc.LaunchChecklist(ctx, auditor, "admin.astras.vip"); launchStatuses(c)["legal"] != LaunchFail ||
		fmt.Sprint(c.Items[slicesIndex(c, "legal")].Value["missing"]) != "[terms]" {
		t.Fatalf("a test-mode page %v %v", launchStatuses(c), c.Items[slicesIndex(c, "legal")].Value)
	}
	// A legal page published for later is not in effect yet.
	content.legal = `{"articles":[{"slug":"terms","status":"PUBLISHED","publish_at":"2026-10-30T00:00:00Z"},{"slug":"privacy","status":"PUBLISHED"},` +
		`{"slug":"risk","status":"PUBLISHED"}]}`
	if c, _ := h.svc.LaunchChecklist(ctx, auditor, "admin.astras.vip"); launchStatuses(c)["legal"] != LaunchFail {
		t.Fatalf("a scheduled page %v", launchStatuses(c))
	}
	// A third party no service reported (its metrics did not answer) is unknown, not unset.
	probe.present = map[string]bool{"turnstile": true, "mail": true}
	if c, _ := h.svc.LaunchChecklist(ctx, auditor, "admin.astras.vip"); launchStatuses(c)["third_party"] != LaunchUnknown {
		t.Fatalf("an unreported third party %v", launchStatuses(c))
	}
	// HOUSE not quoting.
	flags.list[5] = ports.Flag{Key: "market.house_liquidity"}
	if c, _ := h.svc.LaunchChecklist(ctx, auditor, "admin.astras.vip"); launchStatuses(c)["house"] != LaunchFail {
		t.Fatalf("HOUSE off %v", launchStatuses(c))
	}

	// A console reached at another host than the profile's domain.
	if c, _ := h.svc.LaunchChecklist(ctx, auditor, "admin.example.com"); launchStatuses(c)["domain"] != LaunchFail || c.Ready {
		t.Fatalf("another domain %v", launchStatuses(c))
	}
}

func slicesIndex(c LaunchChecklist, key string) int {
	for i, it := range c.Items {
		if it.Key == key {
			return i
		}
	}
	return -1
}
