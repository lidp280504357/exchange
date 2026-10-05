package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// fakePlatform answers as instrument-service's profile and ledger-service's
// welcome credits do: a stale version is refused.
type fakePlatform struct {
	profile   map[string]any
	credits   []ports.WelcomeCredit
	version   int64
	updatedBy string
	sets      []string
	// lose sets the credits but loses the answer, once; failRead fails the
	// next read of them, once.
	lose, failRead bool
}

// creditCatalog lists the assets the welcome credits may name, with their
// decimals.
type creditCatalog struct{ ports.Instruments }

func (creditCatalog) List(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"assets":[{"asset_code":"USDT","decimals":6},{"asset_code":"BTC","decimals":8},{"asset_code":"ETH","decimals":8},` +
		`{"asset_code":"DOGE","decimals":8}],"pairs":[]}`), nil
}

func newFakePlatform() *fakePlatform {
	return &fakePlatform{
		profile: map[string]any{
			"name": "Astras", "short_name": "Astras", "domain": "astras.vip", "version": float64(4),
			"test_mode": map[string]any{"enabled": true, "banner": true}, "registration": map[string]any{"status": "OPEN"},
			"images": map[string]any{"logo_light": nil, "logo_dark": nil, "favicon": nil, "apple_touch_icon": nil},
		},
		credits: []ports.WelcomeCredit{{Asset: "BTC", Amount: decimal.RequireFromString("0.1")}, {Asset: "USDT", Amount: decimal.NewFromInt(10_000)}},
		version: 1,
	}
}

func (f *fakePlatform) Profile(context.Context) (json.RawMessage, error) {
	return json.Marshal(f.profile)
}

func (f *fakePlatform) UpdateProfile(_ context.Context, write json.RawMessage, actor, _ string) (json.RawMessage, error) {
	var w map[string]any
	if err := json.Unmarshal(write, &w); err != nil {
		return nil, err
	}
	if w["expected_version"] != f.profile["version"] {
		return nil, apperr.New(apperr.KindConflict, "INSTRUMENT_PLATFORM_CHANGED", "changed")
	}
	delete(w, "expected_version")
	for k, v := range w {
		f.profile[k] = v
	}
	f.profile["version"] = f.profile["version"].(float64) + 1
	f.profile["updated_by"] = "admin:" + actor
	return json.Marshal(f.profile)
}

func (f *fakePlatform) PutImage(_ context.Context, kind, _, _, _, _ string) (json.RawMessage, error) {
	f.profile["images"].(map[string]any)[kind] = "/v1/platform/images/" + kind
	f.profile["version"] = f.profile["version"].(float64) + 1
	return json.Marshal(f.profile)
}

func (f *fakePlatform) DeleteImage(_ context.Context, kind, _, _ string) (json.RawMessage, error) {
	f.profile["images"].(map[string]any)[kind] = nil
	f.profile["version"] = f.profile["version"].(float64) + 1
	return json.Marshal(f.profile)
}

func (f *fakePlatform) WelcomeCredits(context.Context) (json.RawMessage, error) {
	if f.failRead {
		f.failRead = false
		return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "the ledger is not answering")
	}
	return json.Marshal(map[string]any{"credits": f.credits, "flag_enabled": true, "version": f.version, "updated_by": f.updatedBy})
}

func (f *fakePlatform) SetWelcomeCredits(_ context.Context, credits []ports.WelcomeCredit, expected int64, actor, reason string) (json.RawMessage, error) {
	if expected != f.version {
		return nil, apperr.New(apperr.KindConflict, "LEDGER_SETTINGS_CHANGED", "changed")
	}
	f.credits, f.version, f.updatedBy = credits, f.version+1, actor
	f.sets = append(f.sets, actor+": "+reason)
	if f.lose {
		f.lose = false
		return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "the answer was lost")
	}
	return f.WelcomeCredits(context.Background())
}

func credit(asset, amount string) ports.WelcomeCredit {
	return ports.WelcomeCredit{Asset: asset, Amount: decimal.RequireFromString(amount)}
}

// TestPlatformProfile: everyone reads the profile; one ADMIN changes it,
// audited with what changed; a stale version is refused; the images'
// kinds, types and size are checked here, their bytes never audited.
func TestPlatformProfile(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	pl := newFakePlatform()
	h.svc.Platform = pl
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "ops@example.com", domain.RoleOperator)
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	boss, ops, auditor := h.login(t, "boss@example.com"), h.login(t, "ops@example.com"), h.login(t, "audit@example.com")

	if _, err := h.svc.PlatformProfile(ctx, auditor); err != nil {
		t.Fatal(err)
	}
	write := json.RawMessage(`{"name":"Nova","short_name":"Nova","expected_version":4}`)
	if _, err := h.svc.UpdatePlatformProfile(ctx, ops, write, "rename the exchange"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an OPERATOR renames: %v", err)
	}
	if _, err := h.svc.UpdatePlatformProfile(ctx, boss, json.RawMessage(`{"name":"Nova"}`), "rename the exchange"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("without the version read: %v", err)
	}
	if _, err := h.svc.UpdatePlatformProfile(ctx, boss, write, "rename the exchange"); err != nil {
		t.Fatal(err)
	}
	got := h.auditsOf("admin.platform.updated")
	if len(got) != 1 || !strings.Contains(got[0], `"name":{"new":"Nova","old":"Astras"}`) || !strings.Contains(got[0], `"version":5`) ||
		strings.Contains(got[0], `"test_mode"`) {
		t.Fatalf("audited %v", got)
	}
	if _, err := h.svc.UpdatePlatformProfile(ctx, boss, write, "rename it again"); code(err) != "INSTRUMENT_PLATFORM_CHANGED" {
		t.Fatalf("a stale version: %v", err)
	}

	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG fake"))
	for name, c := range map[string]struct{ kind, data, mime, code string }{
		"a kind":      {"banner", png, "image/png", apperr.CodeNotFound},
		"a type":      {"favicon", png, "image/gif", apperr.CodeInvalidArgument},
		"not base64":  {"favicon", "%%%", "image/png", apperr.CodeInvalidArgument},
		"too large":   {"favicon", base64.StdEncoding.EncodeToString(make([]byte, 201<<10)), "image/png", apperr.CodeInvalidArgument},
		"an OPERATOR": {"favicon", png, "image/png", "ADMIN_FORBIDDEN"},
	} {
		who := boss
		if name == "an OPERATOR" {
			who = ops
		}
		if _, err := h.svc.PutPlatformImage(ctx, who, c.kind, c.data, c.mime, "a new favicon"); code(err) != c.code {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := h.svc.PutPlatformImage(ctx, boss, "favicon", png, "image/png", "a new favicon"); err != nil {
		t.Fatal(err)
	}
	if got := h.auditsOf("admin.platform.image_updated"); len(got) != 1 || !strings.Contains(got[0], `"sha256":"`) || strings.Contains(got[0], png) {
		t.Fatalf("the upload audited %v", got)
	}
	if _, err := h.svc.DeletePlatformImage(ctx, boss, "favicon", "back to the built-in one"); err != nil || len(h.auditsOf("admin.platform.image_removed")) != 1 {
		t.Fatalf("removed %v", err)
	}
}

// TestWelcomeCredits: lowering or clearing applies at once; a raise waits
// for a second ADMIN, at most 10,000 USDT, priced; the approval sets it as
// of the version asked against.
func TestWelcomeCredits(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	pl := newFakePlatform()
	h.svc.Platform, h.svc.Prices, h.svc.Catalog = pl, fakePrices{"BTC-USDT": decimal.NewFromInt(60_000)}, creditCatalog{}
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "second@example.com", domain.RoleAdmin)
	h.admin(t, "finance@example.com", domain.RoleFinance)
	boss, second, finance := h.login(t, "boss@example.com"), h.login(t, "second@example.com"), h.login(t, "finance@example.com")

	// Lower USDT, drop BTC (0 is none): at once, audited old and new.
	res, err := h.svc.SetWelcomeCredits(ctx, boss, []ports.WelcomeCredit{credit("usdt", "5000"), credit("BTC", "0")}, 1, "less for the launch")
	if err != nil || res.Approval != nil || pl.version != 2 || len(pl.credits) != 1 || pl.credits[0].Asset != "USDT" || pl.sets[0] != "boss@example.com: less for the launch" {
		t.Fatalf("lowered %+v %v %+v", res, err, pl.credits)
	}
	if got := h.auditsOf("admin.platform.welcome_changed"); len(got) != 1 || !strings.Contains(got[0], `"new":[{"asset":"USDT","amount":"5000"}]`) {
		t.Fatalf("audited %v", got)
	}
	if _, err := h.svc.SetWelcomeCredits(ctx, boss, nil, 1, "clear them"); code(err) != "LEDGER_SETTINGS_CHANGED" {
		t.Fatalf("a stale version: %v", err)
	}
	for name, bad := range map[string][]ports.WelcomeCredit{
		"twice":      {credit("USDT", "1"), credit("usdt", "2")},
		"negative":   {credit("USDT", "-1")},
		"no asset":   {credit(" ", "1")},
		"one letter": {credit("X", "1")},
		// Raises held to the ledger's rules before a second ADMIN sees them.
		"unknown asset":     {credit("USDT", "5000"), credit("NOPE", "1")},
		"too many decimals": {credit("USDT", "5000.1234567")},
	} {
		if _, err := h.svc.SetWelcomeCredits(ctx, boss, bad, 2, "a bad list"); code(err) != apperr.CodeInvalidArgument {
			t.Fatalf("%s: %v", name, err)
		}
	}

	// Raising waits for a second ADMIN, worth what it adds.
	res, err = h.svc.SetWelcomeCredits(ctx, boss, []ports.WelcomeCredit{credit("USDT", "6000"), credit("BTC", "0.01")}, 2, "a little more")
	if err != nil || res.Approval == nil || pl.version != 2 {
		t.Fatalf("a raise %+v %v", res, err)
	}
	a := *res.Approval
	if a.Kind != domain.KindWelcomeCredit || a.Mode != domain.ModeTwoPerson || a.Escalation != domain.EscalationWelcomeRaise ||
		a.Payload["raise_usdt"] != "1600" || a.Payload["expected_version"] != "2" || len(h.auditsOf("admin.platform.welcome_requested")) != 1 {
		t.Fatalf("the request %+v", a)
	}
	// The same raise asked again while it waits is refused, naming the first (review ㉛),
	// written otherwise too: in another order, another way, with an asset at 0 (review BH ③).
	for name, same := range map[string][]ports.WelcomeCredit{
		"in another order":  {credit("BTC", "0.01"), credit("USDT", "6000.0")},
		"with a 0 for ETH":  {credit("USDT", "6000"), credit("ETH", "0"), credit("BTC", "0.010")},
		"in lower case too": {credit("usdt", "6000"), credit("btc", "0.01")},
	} {
		_, err := h.svc.SetWelcomeCredits(ctx, boss, same, 2, "a little more, again")
		var ae *apperr.Error
		if code(err) != "ADMIN_WELCOME_RAISE_PENDING" || !errors.As(err, &ae) || ae.Details["approval_id"] != a.ID {
			t.Fatalf("the same raise twice, %s: %v", name, err)
		}
	}
	// Another ADMIN may ask for the same, and so may the first once its
	// request no longer waits.
	other, err := h.svc.SetWelcomeCredits(ctx, second, []ports.WelcomeCredit{credit("USDT", "6000"), credit("BTC", "0.01")}, 2, "the same, from another ADMIN")
	if err != nil || other.Approval == nil {
		t.Fatalf("another ADMIN's same raise: %+v %v", other, err)
	}
	if _, err := h.svc.DecideApproval(ctx, second, other.Approval.ID, false, "withdrawn: one is enough"); err != nil {
		t.Fatalf("withdrawn by its requester: %v", err)
	}
	if _, err := h.svc.DecideApproval(ctx, boss, a.ID, true, "my own raise"); code(err) != "ADMIN_SELF_APPROVAL" {
		t.Fatalf("approved by its requester: %v", err)
	}
	if _, err := h.svc.DecideApproval(ctx, finance, a.ID, true, "finance approves"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("approved by FINANCE: %v", err)
	}
	done, err := h.svc.DecideApproval(ctx, second, a.ID, true, "agreed, a little more")
	if err != nil || done.Status != domain.ApprovalExecuted || done.Result != "welcome credits version 3" || len(pl.credits) != 2 ||
		pl.sets[1] != "boss@example.com: a little more (approved by second@example.com)" {
		t.Fatalf("approved %+v %v %v", done, err, pl.sets)
	}

	// Withdrawn, the same raise may be asked again (and withdrawn again).
	again, err := h.svc.SetWelcomeCredits(ctx, boss, []ports.WelcomeCredit{credit("USDT", "6500"), credit("BTC", "0.01")}, 3, "a bit more")
	if err != nil || again.Approval == nil {
		t.Fatalf("a raise: %+v %v", again, err)
	}
	if _, err := h.svc.DecideApproval(ctx, boss, again.Approval.ID, false, "withdrawn: not now"); err != nil {
		t.Fatalf("withdrawn: %v", err)
	}
	if again, err = h.svc.SetWelcomeCredits(ctx, boss, []ports.WelcomeCredit{credit("USDT", "6500"), credit("BTC", "0.01")}, 3, "a bit more after all"); err != nil ||
		again.Approval == nil {
		t.Fatalf("the same raise once the first was withdrawn: %+v %v", again, err)
	}
	if _, err := h.svc.DecideApproval(ctx, boss, again.Approval.ID, false, "withdrawn again"); err != nil {
		t.Fatalf("withdrawn again: %v", err)
	}

	// The cap, and an asset with no price.
	if _, err := h.svc.SetWelcomeCredits(ctx, boss, []ports.WelcomeCredit{credit("USDT", "20000"), credit("BTC", "0.01")}, 3, "much more"); code(err) != "ADMIN_WELCOME_RAISE_CAP" {
		t.Fatalf("beyond the cap: %v", err)
	}
	if _, err := h.svc.SetWelcomeCredits(ctx, boss, []ports.WelcomeCredit{credit("USDT", "6000"), credit("BTC", "0.01"), credit("DOGE", "10")}, 3,
		"some dogecoin"); code(err) != "ADMIN_WELCOME_UNPRICED" {
		t.Fatalf("no price: %v", err)
	}

	// A request whose setting changed meanwhile fails when approved.
	res, err = h.svc.SetWelcomeCredits(ctx, boss, []ports.WelcomeCredit{credit("USDT", "7000"), credit("BTC", "0.01")}, 3, "more again")
	if err != nil || res.Approval == nil {
		t.Fatalf("another raise %+v %v", res, err)
	}
	if _, err := h.svc.SetWelcomeCredits(ctx, second, []ports.WelcomeCredit{credit("USDT", "1000"), credit("BTC", "0.01")}, 3, "less meanwhile"); err != nil {
		t.Fatal(err)
	}
	done, err = h.svc.DecideApproval(ctx, second, res.Approval.ID, true, "too late")
	if err != nil || done.Status != domain.ApprovalFailed || !strings.HasPrefix(done.Result, "LEDGER_SETTINGS_CHANGED") || pl.credits[1].Amount.String() != "1000" {
		t.Fatalf("a stale request %+v %v", done, err)
	}

	// The ledger set a raise but its answer was lost: the request stays
	// pending; approved again, the ledger's 409 is the earlier attempt's
	// work, found at the next version with these credits by this actor
	// (review ㉚).
	res, err = h.svc.SetWelcomeCredits(ctx, boss, []ports.WelcomeCredit{credit("USDT", "1500"), credit("BTC", "0.01")}, pl.version, "a bit more")
	if err != nil || res.Approval == nil {
		t.Fatalf("a raise %+v %v", res, err)
	}
	pl.lose = true
	if _, err := h.svc.DecideApproval(ctx, second, res.Approval.ID, true, "agreed, a bit more"); err == nil {
		t.Fatal("a lost answer decided the request")
	}
	if got := h.store.approvals[res.Approval.ID]; got.Status != domain.ApprovalPending || pl.credits[1].Amount.String() != "1500" {
		t.Fatalf("after the lost answer %+v %+v", got, pl.credits)
	}
	// Approved again while the setting cannot be read: still unknown, still pending (review ㉛).
	pl.failRead = true
	if _, err := h.svc.DecideApproval(ctx, second, res.Approval.ID, true, "agreed, the ledger is slow"); code(err) != apperr.CodeUnavailable {
		t.Fatalf("a 409 not read again: %v", err)
	}
	if got := h.store.approvals[res.Approval.ID]; got.Status != domain.ApprovalPending {
		t.Fatalf("after a 409 not read again %+v", got)
	}
	done, err = h.svc.DecideApproval(ctx, second, res.Approval.ID, true, "agreed again")
	if err != nil || done.Status != domain.ApprovalExecuted || done.Result != "welcome credits version 5 (set by an earlier attempt)" || len(pl.sets) != 4 {
		t.Fatalf("approved again %+v %v %v", done, err, pl.sets)
	}

	// Not decided within a day, a raise lapses: listed as expired, and
	// approving it fails it and sets nothing.
	res, err = h.svc.SetWelcomeCredits(ctx, boss, []ports.WelcomeCredit{credit("USDT", "2000"), credit("BTC", "0.01")}, pl.version, "more, later")
	if err != nil || res.Approval == nil {
		t.Fatalf("a raise %+v %v", res, err)
	}
	h.now = h.now.Add(welcomeApprovalTTL)
	list, _, err := h.svc.Approvals(ctx, boss, domain.ApprovalPending, "", 10)
	if err != nil || len(list) != 1 || !list[0].Lapsed {
		t.Fatalf("listed %+v %v", list, err)
	}
	version := pl.version
	done, err = h.svc.DecideApproval(ctx, second, res.Approval.ID, true, "a day late")
	if err != nil || done.Status != domain.ApprovalFailed || !strings.HasPrefix(done.Result, "expired at ") || pl.version != version {
		t.Fatalf("a lapsed raise %+v %v", done, err)
	}
}
