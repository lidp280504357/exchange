package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// closureDerivatives answers the contracts' reduce-only states as given,
// fails the lift of one and garbles the answer of another.
type closureDerivatives struct {
	fakeDerivatives
	states  string
	fail    string
	garbled string
}

func (d *closureDerivatives) Contracts(context.Context) (json.RawMessage, error) {
	return json.RawMessage(d.states), nil
}

func (d *closureDerivatives) LiftReduceOnly(ctx context.Context, symbol, actor string) (json.RawMessage, error) {
	if symbol == d.fail {
		return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "derivatives down")
	}
	if symbol == d.garbled {
		_, _ = d.fakeDerivatives.LiftReduceOnly(ctx, symbol, actor)
		return json.RawMessage(`<html>`), nil
	}
	return d.fakeDerivatives.LiftReduceOnly(ctx, symbol, actor)
}

// Spot's last closure (A92): the contracts that went reduce-only between
// closing spot and opening it again (a minute's grace after), still so -
// none while spot is closed or was never closed; lifting them together
// takes derivatives.write and a reason, each audited as
// admin.contracts.resumed, one failing not stopping the others.
func TestSpotClosure(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "aud@example.com", domain.RoleAuditor)
	boss, aud := h.login(t, "boss@example.com"), h.login(t, "aud@example.com")
	now := h.now
	pf := &productFlags{now: func() time.Time { return now }, flags: map[string]ports.Flag{}}
	h.svc.Flags = pf
	at := func(d time.Duration) string { return h.now.Add(d).UTC().Format(time.RFC3339Nano) }

	// Never switched: nothing to list.
	c, err := h.svc.SpotReduceOnly(ctx, aud)
	if err != nil || c.ClosedAt != nil || len(c.Contracts) != 0 {
		t.Fatalf("never closed %+v %v", c, err)
	}

	// Closed at -10m: still closed, nothing to list either.
	now = h.now.Add(-10 * time.Minute)
	if _, err := h.svc.Flags.Switch(ctx, "product.spot", false, "boss@example.com", "drill"); err != nil {
		t.Fatal(err)
	}
	if c, err := h.svc.SpotReduceOnly(ctx, aud); err != nil || c.ClosedAt != nil {
		t.Fatalf("closed now %+v %v", c, err)
	}
	// An edit of the closed flag's rules or note (a row off again at -6m)
	// does not move when it closed (A107).
	now = h.now.Add(-6 * time.Minute)
	if _, err := h.svc.Flags.Switch(ctx, "product.spot", false, "boss@example.com", "a note"); err != nil {
		t.Fatal(err)
	}

	// Opened at -2m (and once more at -1m): the contracts that went
	// reduce-only from -10m to a minute after -2m, still so.
	now = h.now.Add(-2 * time.Minute)
	if _, err := h.svc.Flags.Switch(ctx, "product.spot", true, "boss@example.com", "drill over"); err != nil {
		t.Fatal(err)
	}
	now = h.now.Add(-time.Minute)
	if _, err := h.svc.Flags.Switch(ctx, "product.spot", true, "boss@example.com", "again"); err != nil {
		t.Fatal(err)
	}
	d := &closureDerivatives{fail: "ASTRA-USD-PERP", states: `{"contracts":[
		{"symbol":"ASTRA-USDT-PERP","reduce_only":true,"reduce_only_reason":"MARK_PRICE_STALE","reduce_only_since":"` + at(-9*time.Minute) + `"},
		{"symbol":"ASTRA-USD-PERP","reduce_only":true,"reduce_only_reason":"MARK_PRICE_STALE","reduce_only_since":"` + at(-90*time.Second) + `"},
		{"symbol":"BTC-USDT-PERP","reduce_only":true,"reduce_only_reason":"INDEX_SOURCES","reduce_only_since":"` + at(-time.Hour) + `"},
		{"symbol":"ETH-USDT-PERP","reduce_only":true,"reduce_only_reason":"MARK_PRICE_STALE","reduce_only_since":"` + at(0) + `"},
		{"symbol":"SOL-USDT-PERP","reduce_only":false,"reduce_only_reason":"MARK_PRICE_STALE","reduce_only_since":"` + at(-5*time.Minute) + `"}]}`}
	h.svc.Derivatives = d
	c, err = h.svc.SpotReduceOnly(ctx, aud)
	if err != nil || c.ClosedAt == nil || !c.ClosedAt.Equal(h.now.Add(-10*time.Minute)) || c.OpenedAt == nil || !c.OpenedAt.Equal(h.now.Add(-2*time.Minute)) {
		t.Fatalf("the closure %+v %v", c, err)
	}
	if len(c.Contracts) != 2 || c.Contracts[0].Symbol != "ASTRA-USD-PERP" || c.Contracts[1].Symbol != "ASTRA-USDT-PERP" ||
		c.Contracts[1].Reason != "MARK_PRICE_STALE" || !c.Contracts[1].Since.Equal(h.now.Add(-9*time.Minute)) {
		t.Fatalf("the contracts it left reduce-only %+v", c.Contracts)
	}

	// Lifting: derivatives.write and a reason.
	if _, err := h.svc.LiftSpotReduceOnly(ctx, aud, "sound again"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an AUDITOR: %v", err)
	}
	if _, err := h.svc.LiftSpotReduceOnly(ctx, boss, " "); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("no reason: %v", err)
	}
	before := len(h.store.audits)
	lifts, err := h.svc.LiftSpotReduceOnly(ctx, boss, "the index is back")
	if err != nil || len(lifts) != 2 {
		t.Fatalf("lifted %+v %v", lifts, err)
	}
	if lifts[0].Symbol != "ASTRA-USD-PERP" || lifts[0].Lifted || lifts[0].Error != "COMMON_UNAVAILABLE: derivatives down" {
		t.Fatalf("the one failing %+v", lifts[0])
	}
	if lifts[1].Symbol != "ASTRA-USDT-PERP" || !lifts[1].Lifted || lifts[1].Error != "" {
		t.Fatalf("the one lifted %+v", lifts[1])
	}
	if len(d.lifted) != 1 || d.lifted[0] != "ASTRA-USDT-PERP by boss@example.com" {
		t.Fatalf("asked of derivatives-service %v", d.lifted)
	}
	audits := h.store.audits[before:]
	if len(audits) != 2 {
		t.Fatalf("audited %d", len(audits))
	}
	for i, a := range audits {
		if a.GetAction() != "admin.contracts.resumed" || a.GetActor() != "boss@example.com" || a.GetTarget() != "contract:"+lifts[i].Symbol ||
			a.GetReason() != "the index is back" || !strings.Contains(a.GetDetails(), `"product":"spot"`) {
			t.Fatalf("audit %d %+v", i, a)
		}
	}
	if !strings.Contains(audits[0].GetDetails(), `"error":"COMMON_UNAVAILABLE: derivatives down"`) || !strings.Contains(audits[1].GetDetails(), `"lifted":true`) {
		t.Fatalf("the audits' details %s / %s", audits[0].GetDetails(), audits[1].GetDetails())
	}

	// An answer not understood (the lift asked, maybe done) is audited as
	// a failure and the next contract still asked (A107).
	d.fail, d.garbled = "", "ASTRA-USD-PERP"
	before = len(h.store.audits)
	if lifts, err = h.svc.LiftSpotReduceOnly(ctx, boss, "once more"); err != nil || len(lifts) != 2 {
		t.Fatalf("lifted again %+v %v", lifts, err)
	}
	if lifts[0].Lifted || !strings.Contains(lifts[0].Error, "another shape") || !lifts[1].Lifted || len(h.store.audits)-before != 2 ||
		!strings.Contains(h.store.audits[before].GetDetails(), "another shape") {
		t.Fatalf("a garbled answer: %+v, audited %d", lifts, len(h.store.audits)-before)
	}
}
