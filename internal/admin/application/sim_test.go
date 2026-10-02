package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// fakeSim answers as market-sim does: one operator moves the price by at
// most 30% (more needs approved_by); it records who asked and approved.
type fakeSim struct {
	events  []string
	params  []string
	version int64
}

func (f *fakeSim) BotUsers(context.Context) ([]string, error) { return []string{"bot-1"}, nil }

func (f *fakeSim) Status(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"symbol":"ASTRA-USDT","perp":"ASTRA-USDT-PERP","bots":[{"user_id":"bot-1"}]}`), nil
}

func (f *fakeSim) History(_ context.Context, minutes int) (json.RawMessage, error) {
	return json.Marshal(map[string]any{"minutes": minutes})
}

func (f *fakeSim) Events(_ context.Context, all bool, limit int) (json.RawMessage, error) {
	return json.Marshal(map[string]any{"all": all, "limit": limit})
}

func (f *fakeSim) CreateEvent(_ context.Context, event map[string]any, actor, approvedBy string) (json.RawMessage, error) {
	if size, _ := event["size"].(float64); size > 0.3 && approvedBy == "" {
		return nil, apperr.New(apperr.KindForbidden, "SIM_EVENT_NEEDS_APPROVAL", "beyond one operator's share").WithDetail("move", size)
	}
	f.events = append(f.events, event["type"].(string)+" by "+actor+" approved by "+approvedBy)
	return json.Marshal(map[string]any{"id": uuid.NewString(), "type": event["type"], "status": "RUNNING"})
}

func (f *fakeSim) EndEvent(_ context.Context, id, actor, _ string) (json.RawMessage, error) {
	f.events = append(f.events, "end "+id+" by "+actor)
	return json.Marshal(map[string]any{"id": id, "status": "DONE", "ended_by": actor})
}

func (f *fakeSim) UpdateParams(_ context.Context, params json.RawMessage, actor, approvedBy string) (json.RawMessage, error) {
	var p struct {
		P0 float64 `json:"p0"`
	}
	_ = json.Unmarshal(params, &p)
	if p.P0 > 2 && approvedBy == "" {
		return nil, apperr.New(apperr.KindForbidden, "SIM_PARAMS_NEED_APPROVAL", "beyond one operator's share").WithDetail("move", 1.5)
	}
	f.version++
	f.params = append(f.params, string(params)+" by "+actor+" approved by "+approvedBy)
	return json.Marshal(map[string]any{"version": f.version})
}

func TestTheSimulatedMarketFromTheConsole(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	sim := &fakeSim{}
	h.svc.Sim, h.svc.SimBots = sim, sim
	for email, role := range map[string]string{
		"ops@example.com": domain.RoleOperator, "boss@example.com": domain.RoleAdmin, "fin@example.com": domain.RoleFinance,
		"aud@example.com": domain.RoleAuditor,
	} {
		h.admin(t, email, role)
	}
	ops, boss, fin, aud := h.login(t, "ops@example.com"), h.login(t, "boss@example.com"), h.login(t, "fin@example.com"), h.login(t, "aud@example.com")

	if raw, err := h.svc.SimStatus(ctx, aud); err != nil || !strings.Contains(string(raw), "ASTRA-USDT") {
		t.Fatalf("every administrator reads the market: %s %v", raw, err)
	}
	if raw, err := h.svc.SimHistory(ctx, aud, 99999); err != nil || string(raw) != `{"minutes":1440}` {
		t.Fatalf("a day at most: %s %v", raw, err)
	}
	jump := func(size float64) SimEventInput { return SimEventInput{Type: "jump", Size: &size} }
	for who, p := range map[string]Principal{"AUDITOR": aud, "FINANCE": fin} {
		if _, err := h.svc.CreateSimEvent(ctx, p, jump(0.05), "push it up"); code(err) != "ADMIN_FORBIDDEN" {
			t.Fatalf("%s moves no price: %v", who, err)
		}
	}
	if _, err := h.svc.CreateSimEvent(ctx, ops, SimEventInput{Type: "SPIN"}, "spin it"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("an unknown event: %v", err)
	}
	if _, err := h.svc.CreateSimEvent(ctx, ops, SimEventInput{Type: "PAUSE", StartsAt: "tomorrow"}, "pause it"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("a start that is no time: %v", err)
	}

	// Within the share: done at once in the operator's name.
	res, err := h.svc.CreateSimEvent(ctx, ops, jump(0.05), "push it up a little")
	if err != nil || res.Event == nil || res.Approval != nil || sim.events[0] != "JUMP by ops@example.com approved by " {
		t.Fatalf("within the share %+v %v %v", res, err, sim.events)
	}
	if last := h.store.audits[len(h.store.audits)-1]; last.GetAction() != "admin.sim.event_created" || last.GetTarget() != "sim" {
		t.Fatalf("audited %v", last)
	}

	// Beyond it: a request that a second administrator with sim.control
	// decides; market-sim then gets both names.
	res, err = h.svc.CreateSimEvent(ctx, ops, jump(0.35), "push it up a lot")
	if err != nil || res.Approval == nil || len(sim.events) != 1 {
		t.Fatalf("beyond the share %+v %v", res, err)
	}
	a := *res.Approval
	if a.Kind != domain.KindSimEvent || a.Status != domain.ApprovalPending || a.Escalation != domain.EscalationSimShare ||
		a.Payload["actor"] != "ops@example.com" || a.Payload["move"] != "0.35" || !strings.Contains(a.Payload["change"], `"size":0.35`) {
		t.Fatalf("the request %+v", a)
	}
	if _, err := h.svc.DecideApproval(ctx, ops, a.ID, true, "my own"); !apperr.Is(err, "ADMIN_SELF_APPROVAL") {
		t.Fatalf("nobody approves their own: %v", err)
	}
	if _, err := h.svc.DecideApproval(ctx, fin, a.ID, true, "funds people"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("FINANCE decides no price move: %v", err)
	}
	done, err := h.svc.DecideApproval(ctx, boss, a.ID, true, "the board agrees")
	if err != nil || done.Status != domain.ApprovalExecuted || !strings.HasPrefix(done.Result, "event ") ||
		sim.events[1] != "JUMP by ops@example.com approved by boss@example.com" {
		t.Fatalf("approved %+v %v %v", done, err, sim.events)
	}

	// The settings likewise.
	small := json.RawMessage(`{"p0":1.5,"sigma":0.6}`)
	if res, err := h.svc.UpdateSimParams(ctx, ops, small, "a calmer market"); err != nil || res.Version == nil || *res.Version != 1 {
		t.Fatalf("settings within the share %+v %v", res, err)
	}
	res, err = h.svc.UpdateSimParams(ctx, ops, json.RawMessage(`{"p0":3}`), "triple the anchor")
	if err != nil || res.Approval == nil || res.Approval.Kind != domain.KindSimParams {
		t.Fatalf("settings beyond the share %+v %v", res, err)
	}
	if done, err := h.svc.DecideApproval(ctx, boss, res.Approval.ID, true, "agreed"); err != nil || done.Result != "settings version 2" ||
		sim.params[1] != `{"p0":3} by ops@example.com approved by boss@example.com` {
		t.Fatalf("settings approved %+v %v %v", done, err, sim.params)
	}
	if _, err := h.svc.UpdateSimParams(ctx, ops, json.RawMessage(`[]`), "nothing"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("settings not an object: %v", err)
	}

	// Ending an event, and the impact of a price on the perpetual.
	id := uuid.NewString()
	if _, err := h.svc.EndSimEvent(ctx, ops, id, "enough of it"); err != nil || sim.events[2] != "end "+id+" by ops@example.com" {
		t.Fatalf("ended %v %v", err, sim.events)
	}
	// derivatives-service's impact with the sides open now, HOUSE apart.
	h.svc.HouseBook.User = "house"
	if raw, err := h.svc.SimImpact(ctx, aud, "0.85"); err != nil || !strings.Contains(string(raw), `"liquidated":2`) ||
		!strings.Contains(string(raw), `"longs":1`) || !strings.Contains(string(raw), `"shorts":1`) ||
		h.derivatives.tiers[len(h.derivatives.tiers)-1] != "ASTRA-USDT-PERP at 0.85" {
		t.Fatalf("impact %s %v", raw, err)
	}
	if q := h.derivatives.queries[len(h.derivatives.queries)-1]; q.Symbol != "ASTRA-USDT-PERP" || q.Limit != simPositionsMax {
		t.Fatalf("positions asked %+v", q)
	}
}
