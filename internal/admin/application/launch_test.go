package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
)

// launchFlags is the flags as a launch has them or not.
type launchFlags struct{ list []ports.Flag }

func (f *launchFlags) List(context.Context) ([]ports.Flag, error) { return f.list, nil }

func (f *launchFlags) Switch(context.Context, string, bool, string, string) (ports.Flag, error) {
	return ports.Flag{}, errors.New("read-only")
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

// TestLaunchChecklist reads each item from its source: the learning setup
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
	}}
	wallet := &launchWallet{custody: `{"provider":"UDUN","configured":true}`}
	catalog := &launchCatalog{}
	content := &launchContent{}
	probe := &launchProbe{present: map[string]bool{"turnstile": true, "mail": false}}
	pl := newFakePlatform()
	h.svc.Flags, h.svc.Wallet, h.svc.Catalog, h.svc.Content, h.svc.Probe, h.svc.Platform = flags, wallet, catalog, content, probe, pl

	// The test server: the learning setup.
	c, err := h.svc.LaunchChecklist(ctx, auditor, "admin.astras.vip")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"welcome_credits": LaunchFail, "learning_mode": LaunchFail, "registration": LaunchOK, "admin_totp": LaunchFail, "two_person": LaunchOK,
		"test_assets": LaunchFail, "custodian": LaunchPending, "withdraw": LaunchOK, "brand": LaunchFail, "coin_profile": LaunchFail,
		"legal": LaunchPending, "third_party": LaunchFail, "admins": LaunchFail, "domain": LaunchOK,
	}
	got := launchStatuses(c)
	for key, status := range want {
		if got[key] != status {
			t.Fatalf("%s is %s, want %s (%v)", key, got[key], status, got)
		}
	}
	if c.Ready || len(c.Items) != len(launchKeys) || c.Items[0].Key != "welcome_credits" {
		t.Fatalf("the learning setup %+v", c)
	}
	if v := c.Items[slicesIndex(c, "admins")].Value; v["active_admins"] != 1 {
		t.Fatalf("the administrators %+v", v)
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
	}
	wallet.custody = `{"provider":"UDUN","configured":true,"gateway_host":"sig10.udun.io"}`
	catalog.logo = "/v1/market/assets/ASTRA/logo?v=2"
	content.legal = `{"articles":[{"slug":"terms","status":"PUBLISHED"},{"slug":"privacy","status":"PUBLISHED"},{"slug":"risk","status":"PUBLISHED"},` +
		`{"slug":"fees","status":"DRAFT"}]}`
	probe.present = map[string]bool{"turnstile": true, "mail": true, "alchemy": true}
	pl.credits = nil
	pl.profile["learning_mode"] = map[string]any{"enabled": false}
	pl.profile["images"] = map[string]any{"logo_light": "/v1/platform/images/logo_light?v=5", "logo_dark": nil, "favicon": "/v1/platform/images/favicon?v=5"}
	h.admin(t, "second@example.com", domain.RoleAdmin)
	c, err = h.svc.LaunchChecklist(ctx, auditor, "admin.astras.vip:443")
	if err != nil || !c.Ready {
		t.Fatalf("set for a launch %v %v", launchStatuses(c), err)
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
