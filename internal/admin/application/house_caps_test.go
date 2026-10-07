package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// fakeMarketMaker keeps HOUSE's caps as market-maker does (review C45):
// the first from the environment, then the caps given change, of the
// version read; each version in the history.
type fakeMarketMaker struct {
	caps    map[string]string
	version int64
	by      string
	history []map[string]any
	// down fails every call as a service down; lose sets the caps but
	// loses the answer.
	down, lose bool
	puts       []ports.HouseCapsWrite
}

func newFakeMarketMaker() *fakeMarketMaker {
	caps := map[string]string{
		"level": "500000000", "symbol": "500000000", "total": "500000000", "contract": "500000000", "safety": "1000",
		"contract_leverage": "10",
	}
	first := map[string]string{}
	for k, v := range caps {
		first[k] = v
	}
	return &fakeMarketMaker{caps: caps, version: 1, by: "environment", history: []map[string]any{{
		"version": 1, "caps": first, "previous": nil, "actor": "environment", "approver": "", "approval_id": "",
		"reason": "the service's environment (HOUSE_*)", "at": "2026-10-07T04:50:00Z",
	}}}
}

func (m *fakeMarketMaker) stored() json.RawMessage {
	out := map[string]any{"version": m.version, "updated_by": m.by, "updated_at": "2026-10-07T05:00:00Z"}
	for k, v := range m.caps {
		out[k] = v
	}
	raw, _ := json.Marshal(out)
	return raw
}

func (m *fakeMarketMaker) HouseCaps(context.Context) (json.RawMessage, error) {
	if m.down {
		return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "market-maker is down")
	}
	return m.stored(), nil
}

func (m *fakeMarketMaker) SetHouseCaps(_ context.Context, w ports.HouseCapsWrite) (json.RawMessage, error) {
	m.puts = append(m.puts, w)
	if m.down {
		return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "market-maker is down")
	}
	if w.Version != m.version {
		return nil, apperr.New(apperr.KindConflict, "HOUSE_CAPS_VERSION", "the caps changed")
	}
	previous := map[string]string{}
	for k, v := range m.caps {
		previous[k] = v
	}
	for k, v := range w.Caps {
		m.caps[k] = v
	}
	m.version++
	m.by = w.Actor
	now := map[string]string{}
	for k, v := range m.caps {
		now[k] = v
	}
	change := map[string]any{
		"version": m.version, "caps": now, "previous": previous, "actor": w.Actor, "approver": w.Approver, "approval_id": w.ApprovalID,
		"reason": w.Reason, "signed_by": "admin", "at": "2026-10-07T05:10:00Z",
	}
	m.history = append([]map[string]any{change}, m.history...)
	if m.lose {
		m.lose = false
		return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "the answer was lost")
	}
	return m.stored(), nil
}

func (m *fakeMarketMaker) HouseCapsChanges(context.Context, int) (json.RawMessage, error) {
	if m.down {
		return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "market-maker is down")
	}
	raw, _ := json.Marshal(map[string]any{"items": m.history})
	return raw, nil
}

func TestHouseCaps(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "fin@example.com", domain.RoleFinance)
	h.admin(t, "ops@example.com", domain.RoleOperator)
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	boss, fin, ops, auditor := h.login(t, "boss@example.com"), h.login(t, "fin@example.com"), h.login(t, "ops@example.com"),
		h.login(t, "audit@example.com")
	if _, err := h.svc.HouseCapsOf(ctx, auditor); code(err) != apperr.CodeUnavailable {
		t.Fatalf("without market-maker: %v", err)
	}
	m := newFakeMarketMaker()
	h.svc.MarketMaker = m

	// Every administrator reads them, with the first version's.
	v, err := h.svc.HouseCapsOf(ctx, auditor)
	if err != nil || v.Caps.Level != "500000000" || v.Caps.ContractLeverage != "10" || v.Caps.Version != 1 || v.Pending != nil || len(v.Changes) != 1 ||
		!strings.Contains(string(v.Initial), `"safety":"1000"`) {
		t.Fatalf("read %+v %v", v, err)
	}
	lower := HouseCapsRequest{Caps: map[string]string{"level": "400000000", "symbol": "500000000"}, Version: 1, Reason: "smaller levels"}
	for _, who := range []Principal{ops, auditor} {
		if _, err := h.svc.RequestHouseCaps(ctx, who, lower); code(err) != "ADMIN_FORBIDDEN" {
			t.Fatalf("%s asks: %v", who.Admin.Role, err)
		}
	}
	// Each cap within its range (user 06:0x): the level zero or more, the
	// other USDT caps above zero, none above 1e15; the leverage 1 to 125.
	for name, bad := range map[string]HouseCapsRequest{
		"no such cap":        {Caps: map[string]string{"depth": "1"}, Version: 1, Reason: "x y z"},
		"below zero":         {Caps: map[string]string{"total": "-1"}, Version: 1, Reason: "x y z"},
		"a level below zero": {Caps: map[string]string{"level": "-0.1"}, Version: 1, Reason: "x y z"},
		"a pair's cap zero":  {Caps: map[string]string{"symbol": "0"}, Version: 1, Reason: "x y z"},
		"no safety margin":   {Caps: map[string]string{"safety": "0"}, Version: 1, Reason: "x y z"},
		"above 1e15":         {Caps: map[string]string{"contract": "1000000000000000.01"}, Version: 1, Reason: "x y z"},
		"not a decimal":      {Caps: map[string]string{"safety": "lots"}, Version: 1, Reason: "x y z"},
		"no leverage":        {Caps: map[string]string{"contract_leverage": "0"}, Version: 1, Reason: "x y z"},
		"under 1x":           {Caps: map[string]string{"contract_leverage": "0.5"}, Version: 1, Reason: "x y z"},
		"over 125x":          {Caps: map[string]string{"contract_leverage": "126"}, Version: 1, Reason: "x y z"},
		"nothing changes":    {Caps: map[string]string{"level": "500000000.00"}, Version: 1, Reason: "x y z"},
		"without a reason":   {Caps: map[string]string{"level": "1"}, Version: 1},
		"nothing asked for":  {Version: 1, Reason: "x y z"},
	} {
		if _, err := h.svc.RequestHouseCaps(ctx, fin, bad); code(err) != apperr.CodeInvalidArgument {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// One change moves a cap ten times at most either way (market-maker's
	// HOUSE_CAPS_STEP, review C47 ②), a level cap to or from no cap aside.
	for name, c := range map[string]map[string]string{
		"eleven times the total": {"total": "5500000000"},
		"a tenth and a bit less": {"contract": "49999999"},
	} {
		if _, err := h.svc.RequestHouseCaps(ctx, fin, HouseCapsRequest{Caps: c, Version: 1, Reason: "x y z"}); code(err) != "HOUSE_CAPS_STEP" {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if err := checkHouseCapStep("level", decimal.NewFromInt(500), decimal.Zero); err != nil {
		t.Fatalf("a level cap to no cap: %v", err)
	}
	if err := checkHouseCapStep("total", decimal.NewFromInt(100), decimal.NewFromInt(1000)); err != nil {
		t.Fatalf("ten times: %v", err)
	}
	// The edges are in: a level not capped, 1x and 125x, 1e15.
	for _, edge := range []map[string]string{{"level": "0"}, {"contract_leverage": "1"}, {"contract_leverage": "125"}, {"total": "1000000000000000"}} {
		if err := checkEdge(edge); err != nil {
			t.Fatalf("%v refused: %v", edge, err)
		}
	}
	if _, err := h.svc.RequestHouseCaps(ctx, fin, HouseCapsRequest{Caps: lower.Caps, Version: 0, Reason: lower.Reason}); code(err) != "HOUSE_CAPS_VERSION" {
		t.Fatalf("a stale version: %v", err)
	}

	// A request for a second administrator, whatever the approval mode:
	// only what changes, with what it replaces.
	a, err := h.svc.RequestHouseCaps(ctx, fin, lower)
	if err != nil || a.Kind != domain.KindHouseCaps || a.Mode != domain.ModeTwoPerson || a.Status != domain.ApprovalPending ||
		a.Payload["caps"] != `{"level":"400000000"}` || a.Payload["previous"] != `{"level":"500000000"}` || a.Payload["changed"] != "level" ||
		a.Payload["expected_version"] != "1" || a.Payload["actor"] != "fin@example.com" || len(m.puts) != 0 {
		t.Fatalf("the request %+v %v", a, err)
	}
	if got := h.auditsOf("admin.house.caps_requested"); len(got) != 1 || !strings.HasPrefix(got[0], "house:caps smaller levels") {
		t.Fatalf("the request audited %v", got)
	}
	if _, err := h.svc.RequestHouseCaps(ctx, boss, HouseCapsRequest{Caps: map[string]string{"total": "100000000"}, Version: 1, Reason: "another"}); code(err) != "ADMIN_HOUSE_CAPS_PENDING" ||
		detailOf(err) != a.ID {
		t.Fatalf("a second request while one waits: %v", err)
	}
	if v, _ := h.svc.HouseCapsOf(ctx, auditor); v.Pending == nil || v.Pending.ID != a.ID {
		t.Fatalf("the waiting request not shown: %+v", v.Pending)
	}
	if _, err := h.svc.DecideApproval(ctx, fin, a.ID, true, "my own"); code(err) != "ADMIN_SELF_APPROVAL" {
		t.Fatalf("approved by its requester: %v", err)
	}
	if _, err := h.svc.DecideApproval(ctx, ops, a.ID, true, "an OPERATOR approves"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("approved by an OPERATOR: %v", err)
	}
	// market-maker set it but the answer was lost: still pending; approved
	// again, its 409 is the earlier attempt's work.
	m.lose = true
	if _, err := h.svc.DecideApproval(ctx, boss, a.ID, true, "agreed"); err == nil {
		t.Fatal("a lost answer decided the request")
	}
	if got := h.store.approvals[a.ID]; got.Status != domain.ApprovalPending || m.version != 2 || m.caps["level"] != "400000000" {
		t.Fatalf("after the lost answer %+v %+v", got, m.caps)
	}
	done, err := h.svc.DecideApproval(ctx, boss, a.ID, true, "agreed again")
	if err != nil || done.Status != domain.ApprovalExecuted || done.Result != "caps version 2 (set by an earlier attempt)" {
		t.Fatalf("approved again %+v %v", done, err)
	}
	// Audited all the same (review FZ, A75 ③).
	if got := h.auditsOf("admin.house.caps_changed"); len(got) != 1 || !strings.Contains(got[0], `"set_by_an_earlier_attempt":true`) ||
		!strings.Contains(got[0], `{"after":"400000000","before":"500000000","cap":"level"}`) || !strings.Contains(got[0], `"version":2`) {
		t.Fatalf("the earlier attempt's change audited %v", got)
	}
	if w := m.puts[0]; w.Version != 1 || w.Actor != "fin@example.com" || w.Approver != "boss@example.com" || w.ApprovalID != a.ID ||
		w.Reason != "smaller levels" || len(w.Caps) != 1 || w.Caps["level"] != "400000000" {
		t.Fatalf("the change sent %+v", w)
	}

	// A request approved at once: changed, audited cap by cap.
	b, err := h.svc.RequestHouseCaps(ctx, fin, HouseCapsRequest{Caps: map[string]string{"safety": "100", "contract_leverage": "5"}, Version: 2, Reason: "a margin"})
	if err != nil {
		t.Fatal(err)
	}
	if done, err := h.svc.DecideApproval(ctx, boss, b.ID, true, "agreed"); err != nil || done.Status != domain.ApprovalExecuted || done.Result != "caps version 3" ||
		m.caps["safety"] != "100" || m.caps["contract_leverage"] != "5" || m.by != "fin@example.com" {
		t.Fatalf("approved %+v %v %+v", done, err, m.caps)
	}
	if got := h.auditsOf("admin.house.caps_changed"); len(got) != 2 || !strings.Contains(got[1], `{"after":"100","before":"1000","cap":"safety"}`) ||
		!strings.Contains(got[1], `{"after":"5","before":"10","cap":"contract_leverage"}`) || !strings.Contains(got[1], `"approved_by":"boss@example.com"`) {
		t.Fatalf("the change audited %v", got)
	}
	if v, _ := h.svc.HouseCapsOf(ctx, auditor); v.Pending != nil || len(v.Changes) != 3 || v.Changes[0].Version != 3 || v.Changes[0].ApprovalID != b.ID ||
		v.Changes[0].SignedBy != "admin" || !strings.Contains(string(v.Initial), `"safety":"1000"`) {
		t.Fatalf("after the change %+v", v)
	}

	// The caps moved meanwhile: the request fails when approved.
	c, err := h.svc.RequestHouseCaps(ctx, fin, HouseCapsRequest{Caps: map[string]string{"total": "100000000"}, Version: 3, Reason: "a smaller total"})
	if err != nil {
		t.Fatal(err)
	}
	m.version = 4
	if done, err := h.svc.DecideApproval(ctx, boss, c.ID, true, "agreed"); err != nil || done.Status != domain.ApprovalFailed ||
		!strings.HasPrefix(done.Result, "HOUSE_CAPS_VERSION") || m.caps["total"] != "500000000" {
		t.Fatalf("moved meanwhile %+v %v", done, err)
	}
	// Market-maker down: the request stays as it was; a day later it lapses.
	d, err := h.svc.RequestHouseCaps(ctx, fin, HouseCapsRequest{Caps: map[string]string{"total": "100000000"}, Version: 4, Reason: "a smaller total"})
	if err != nil {
		t.Fatal(err)
	}
	m.down = true
	if _, err := h.svc.DecideApproval(ctx, boss, d.ID, true, "agreed"); code(err) != apperr.CodeUnavailable || h.store.approvals[d.ID].Status != domain.ApprovalPending {
		t.Fatalf("market-maker down: %v %+v", err, h.store.approvals[d.ID])
	}
	m.down = false
	h.now = h.now.Add(25 * time.Hour)
	if done, err := h.svc.DecideApproval(ctx, boss, d.ID, true, "late"); err != nil || done.Status != domain.ApprovalFailed ||
		!strings.HasPrefix(done.Result, "expired at") || m.caps["total"] != "500000000" {
		t.Fatalf("lapsed %+v %v", done, err)
	}
	// A lapsed request stands in no one's way.
	if _, err := h.svc.RequestHouseCaps(ctx, fin, HouseCapsRequest{Caps: map[string]string{"total": "100000000"}, Version: 4, Reason: "again"}); err != nil {
		t.Fatalf("after a lapsed one: %v", err)
	}
}

// checkEdge checks each cap of caps against its range.
func checkEdge(caps map[string]string) error {
	for name, v := range caps {
		if err := checkHouseCap(name, decimal.RequireFromString(v)); err != nil {
			return err
		}
	}
	return nil
}

// TestHouseCapsShown checks the console's view of market-maker's history:
// the latest 10 changes of the 100 read, and the first version's caps
// only when it is among those read.
func TestHouseCapsShown(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	auditor := h.login(t, "audit@example.com")
	m := newFakeMarketMaker()
	h.svc.MarketMaker = m
	for v := int64(2); v <= 12; v++ {
		m.history = append([]map[string]any{{"version": v, "caps": map[string]string{"level": "1"}, "actor": "fin@example.com", "at": "2026-10-07T05:00:00Z"}}, m.history...)
	}
	v, err := h.svc.HouseCapsOf(ctx, auditor)
	if err != nil || len(v.Changes) != 10 || v.Changes[0].Version != 12 || v.Changes[9].Version != 3 || !strings.Contains(string(v.Initial), `"level":"500000000"`) {
		t.Fatalf("12 versions: %d changes, initial %s, %v", len(v.Changes), v.Initial, err)
	}
	m.history = m.history[:5]
	if v, err := h.svc.HouseCapsOf(ctx, auditor); err != nil || len(v.Changes) != 5 || v.Initial != nil {
		t.Fatalf("the first version not among those read: initial %s, %v", v.Initial, err)
	}
}
