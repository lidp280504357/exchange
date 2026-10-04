package application

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// TestThresholdTargetsAndSpikes checks the console's side of ASTRA A6: a
// threshold target's and a spike's fields are checked and passed to
// market-sim as they are, the rest left to it; its plan and the form's
// preview are read with reports.read.
func TestThresholdTargetsAndSpikes(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	sim := &fakeSim{}
	h.svc.Sim, h.svc.SimBots = sim, sim
	h.admin(t, "ops@example.com", domain.RoleOperator)
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	ops, auditor := h.login(t, "ops@example.com"), h.login(t, "audit@example.com")
	in1h, in2h := h.now.Add(time.Hour).Format(time.RFC3339), h.now.Add(2*time.Hour).Format(time.RFC3339)

	// A target above 0.60 within 30 minutes, holding 10 minutes once crossed, with one spike.
	target := SimEventInput{
		Type: "TARGET", Price: "0.60", DurationSeconds: 1800, Direction: "above", Then: "hold", HoldSeconds: 600,
		Spikes: []SimSpike{{At: in1h, Size: -0.04, WidthSeconds: 20}},
	}
	if _, err := h.svc.CreateSimEvent(ctx, ops, target, "drift above 0.60"); err != nil {
		t.Fatal(err)
	}
	spikes, _ := sim.last["spikes"].([]SimSpike)
	if sim.last["direction"] != "ABOVE" || sim.last["then"] != "HOLD" || sim.last["hold_seconds"] != 600 || len(spikes) != 1 || spikes[0].Size != -0.04 {
		t.Fatalf("passed on %+v", sim.last)
	}
	// A spike of its own.
	if _, err := h.svc.CreateSimEvent(ctx, ops, SimEventInput{Type: "SPIKE", Size: ptr(0.03), WidthSeconds: 30, StartsAt: in2h}, "a spike"); err != nil ||
		sim.last["width_seconds"] != 30 {
		t.Fatalf("a spike %v %+v", err, sim.last)
	}
	// The console's own checks.
	for name, bad := range map[string]SimEventInput{
		"direction":          {Type: "TARGET", Price: "0.6", DurationSeconds: 600, Direction: "UP"},
		"then":               {Type: "TARGET", Price: "0.6", DurationSeconds: 600, Then: "STOP"},
		"on a jump":          {Type: "JUMP", Size: ptr(0.01), Direction: "ABOVE"},
		"spikes on a jump":   {Type: "JUMP", Size: ptr(0.01), Spikes: []SimSpike{{At: in1h, Size: 0.01}}},
		"width on a jump":    {Type: "JUMP", Size: ptr(0.01), WidthSeconds: 20},
		"a wide spike":       {Type: "SPIKE", Size: ptr(0.01), WidthSeconds: 61},
		"a past spike":       {Type: "TARGET", Price: "0.6", DurationSeconds: 600, Spikes: []SimSpike{{At: h.now.Add(-time.Minute).Format(time.RFC3339), Size: 0.01}}},
		"a huge spike":       {Type: "TARGET", Price: "0.6", DurationSeconds: 600, Spikes: []SimSpike{{At: in1h, Size: 0.11}}},
		"a flat spike":       {Type: "TARGET", Price: "0.6", DurationSeconds: 600, Spikes: []SimSpike{{At: in1h, Size: 0}}},
		"a spike's time":     {Type: "TARGET", Price: "0.6", DurationSeconds: 600, Spikes: []SimSpike{{At: "soon", Size: 0.01}}},
		"a lone huge spike":  {Type: "SPIKE", Size: ptr(-0.11)},
		"a lone flat spike":  {Type: "SPIKE", Size: ptr(0.0)},
		"a spike of no size": {Type: "SPIKE", WidthSeconds: 20},
	} {
		if _, err := h.svc.CreateSimEvent(ctx, ops, bad, "a bad one"); code(err) != apperr.CodeInvalidArgument {
			t.Fatalf("%s: %v", name, err)
		}
	}

	// Reads: the plan and the preview, by anyone who reads reports.
	id := "0192a000-0000-7000-8000-00000000a6a6"
	if _, err := h.svc.SimPlan(ctx, auditor, id); err != nil || sim.plan != id {
		t.Fatalf("the plan %v %q", err, sim.plan)
	}
	if _, err := h.svc.SimPlan(ctx, auditor, "target-1"); code(err) != apperr.CodeNotFound {
		t.Fatalf("not an event's ID: %v", err)
	}
	q := ports.SimTargetQuery{Direction: "below", Price: "0.500", DurationSeconds: 600, StartsAt: in1h}
	raw, err := h.svc.SimTargetPreview(ctx, auditor, q)
	if err != nil || sim.preview.Direction != "BELOW" || sim.preview.Price != "0.5" {
		t.Fatalf("the preview %v %+v", err, sim.preview)
	}
	// With the server's time, which the form takes its spikes' times from (review 29).
	var preview struct {
		Now      time.Time `json:"now"`
		Feasible bool      `json:"feasible"`
	}
	if err := json.Unmarshal(raw, &preview); err != nil || !preview.Now.Equal(h.now) || !preview.Feasible {
		t.Fatalf("the preview's now %s %v", raw, err)
	}
	// A day ahead at most, the preview as creating (review 29).
	later := h.now.Add(25 * time.Hour).Format(time.RFC3339)
	if _, err := h.svc.SimTargetPreview(ctx, auditor, ports.SimTargetQuery{Price: "0.5", DurationSeconds: 600, StartsAt: later}); code(err) != "ADMIN_SIM_TOO_FAR_AHEAD" {
		t.Fatalf("a preview a day and more ahead: %v", err)
	}
	if _, err := h.svc.CreateSimEvent(ctx, ops, SimEventInput{Type: "JUMP", Size: ptr(0.01), StartsAt: later}, "a jump too far ahead"); code(err) != "ADMIN_SIM_TOO_FAR_AHEAD" {
		t.Fatalf("an event a day and more ahead: %v", err)
	}
	for name, bad := range map[string]ports.SimTargetQuery{
		"no price":     {Price: "", DurationSeconds: 600},
		"a price ≤ 0":  {Price: "-1", DurationSeconds: 600},
		"too short":    {Price: "0.5", DurationSeconds: 59},
		"a direction":  {Direction: "UP", Price: "0.5", DurationSeconds: 600},
		"a start time": {Price: "0.5", DurationSeconds: 600, StartsAt: "later"},
	} {
		if _, err := h.svc.SimTargetPreview(ctx, auditor, bad); code(err) != apperr.CodeInvalidArgument {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// spikySim is market-sim whose operator's share is 5% of a spike and a
// target up to 2.
type spikySim struct{ pricedSim }

func (s *spikySim) CreateEvent(ctx context.Context, event map[string]any, actor, approvedBy string) (json.RawMessage, error) {
	size, _ := event["size"].(float64)
	if approvedBy == "" && (event["type"] == "SPIKE" && math.Abs(size) > 0.05 || event["type"] == "TARGET" && event["price"] == "2") {
		return nil, apperr.New(apperr.KindForbidden, "SIM_EVENT_NEEDS_APPROVAL", "beyond one operator's share").WithDetail("move", 0.4)
	}
	return s.pricedSim.CreateEvent(ctx, event, actor, approvedBy)
}

// TestSpikesWaitingForASecondAdministrator: a spike beyond one operator's
// share is measured for its decider at its tip, its impact where the
// perpetual's mark takes it (about half of it); a target's request lapses
// at its first spike, and an approved one carries its spikes to market-sim.
func TestSpikesWaitingForASecondAdministrator(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	sim := &spikySim{}
	h.svc.Sim, h.svc.SimBots = sim, sim
	h.admin(t, "ops@example.com", domain.RoleOperator)
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	ops, boss := h.login(t, "ops@example.com"), h.login(t, "boss@example.com")

	res, err := h.svc.CreateSimEvent(ctx, ops, SimEventInput{Type: "SPIKE", Size: ptr(-0.08), WidthSeconds: 20}, "a deep spike")
	if err != nil || res.Approval == nil {
		t.Fatalf("waits %+v %v", res, err)
	}
	pv, err := h.svc.SimApprovalPreview(ctx, boss, res.Approval.ID)
	if err != nil || pv.Expected.String() != "1.15" || *pv.Move != -0.08 {
		t.Fatalf("a spike's tip %+v %v", pv, err)
	}
	if got := h.derivatives.tiers[len(h.derivatives.tiers)-1]; got != "ASTRA-USDT-PERP at 1.19875" {
		t.Fatalf("the impact where the mark goes, about half of it: %s", got)
	}
	if share := spikeMarkShare(60); share != 0.525 {
		t.Fatalf("a wide spike's share without the middle: %v", share)
	}

	// A target with spikes in an hour and later lapses at the first, not in a day.
	in1h := h.now.Add(time.Hour).Format(time.RFC3339)
	in2h := h.now.Add(2 * time.Hour).Format(time.RFC3339)
	target := SimEventInput{
		Type: "TARGET", Price: "2", DurationSeconds: 3 * 3600, Direction: "ABOVE", Then: "FOLLOW",
		Spikes: []SimSpike{{At: in1h, Size: 0.04}, {At: in2h, Size: -0.06, WidthSeconds: 30}, {At: in2h, Size: -0.02}},
	}
	res, err = h.svc.CreateSimEvent(ctx, ops, target, "a target with spikes")
	if err != nil || res.Approval == nil || !simExpiry(*res.Approval).Equal(h.now.Add(time.Hour).Truncate(time.Second)) {
		t.Fatalf("lapses at its first spike %+v %v", res, err)
	}
	// Its decider sees the level's impact and its worst spike each way at
	// the mark (review 29): up from the level, 2 x (1 + 0.04 x 0.5125);
	// down from the target now, 1.25 x (1 - 0.06 x 0.5125).
	calls := len(h.derivatives.tiers)
	pv, err = h.svc.SimApprovalPreview(ctx, boss, res.Approval.ID)
	if err != nil || pv.Expected.String() != "2" || len(pv.SpikeImpacts) != 2 ||
		pv.SpikeImpacts[0].Price.Round(8).String() != "1.2115625" || pv.SpikeImpacts[1].Price.Round(8).String() != "2.041" {
		t.Fatalf("a target's spikes for its decider %+v %v", pv, err)
	}
	if got := h.derivatives.tiers[calls:]; len(got) != 3 || got[0] != "ASTRA-USDT-PERP at 2" || got[1] != "ASTRA-USDT-PERP at 1.2115625" ||
		got[2] != "ASTRA-USDT-PERP at 2.041" {
		t.Fatalf("measured at %v", got)
	}
	// Decided in time, its spikes go with it.
	h.now = h.now.Add(30 * time.Minute)
	done, err := h.svc.DecideApproval(ctx, boss, res.Approval.ID, true, "in time for its spike")
	spikes, _ := sim.last["spikes"].([]any)
	if err != nil || done.Status != domain.ApprovalExecuted || len(spikes) != 3 || sim.last["direction"] != "ABOVE" {
		t.Fatalf("carried out %+v %v %+v", done, err, sim.last)
	}
	if sp, _ := spikes[0].(map[string]any); sp["at"] != in1h || sp["size"] != 0.04 {
		t.Fatalf("its spike %+v", spikes[0])
	}
	// Anything but a target has no spikes to measure.
	if marks := simSpikeMarks(domain.Approval{Kind: domain.KindSimEvent, Payload: map[string]string{"change": `{"type":"JUMP","size":0.4}`}},
		decimal.NewFromFloat(1.25)); marks != nil {
		t.Fatalf("a jump's spikes %v", marks)
	}
}

func ptr[T any](v T) *T { return &v }
