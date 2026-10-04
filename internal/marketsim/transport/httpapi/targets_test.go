package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketsim/application"
	"github.com/skill/exchange/internal/marketsim/domain"
)

// A threshold target renders its window, its closing and hold times, its
// course and its spikes; a spike its width and target; a running target's
// plan where the price is against it (A6 contract, market-sim.md).
func TestATargetAndItsPlanRender(t *testing.T) {
	start := time.Date(2026, 10, 4, 6, 0, 0, 0, time.UTC)
	e := domain.Event{
		ID: "t1", Type: domain.EventTarget, Direction: domain.Above, Then: domain.ThenHold, Price: decimal.RequireFromString("1.08"),
		Duration: 30 * time.Minute, Hold: 10 * time.Minute, StartsAt: start, StartedAt: start, Status: domain.EventRunning,
		CrossedAt: start.Add(29 * time.Minute), Result: domain.ResultHit, FromP: decimal.RequireFromString("1"),
	}
	j := eventJSON(e)
	if j.Direction != "ABOVE" || j.Then != "HOLD" || j.Result != "HIT" || *j.EndsAt != "2026-10-04T06:30:00.000Z" ||
		*j.ClosingAt != "2026-10-04T06:27:00.000Z" || *j.HoldUntil != "2026-10-04T06:39:00.000Z" || *j.CrossedAt != "2026-10-04T06:29:00.000Z" ||
		j.ParentID != nil {
		t.Fatalf("the target %+v", j)
	}
	spike := eventJSON(domain.Event{ID: "s1", Type: domain.EventSpike, Size: -0.04, Width: 20 * time.Second, ParentID: "t1", StartsAt: start})
	if spike.WidthS != 20 || spike.ParentID == nil || *spike.ParentID != "t1" || spike.EndsAt != nil {
		t.Fatalf("the spike %+v", spike)
	}
	params := domain.DefaultParams()
	points := domain.Plan(e, 1, params)
	v := application.TargetView{
		Event: e, From: 1, Points: points, Spikes: []domain.Event{{ID: "s1", Type: domain.EventSpike, ParentID: "t1"}},
		Now: domain.PlanAt(points, start.Add(10*time.Minute)), Target: 1.03, Deviation: 0.002, AtRisk: true,
	}
	p := planJSON(v)
	if len(p.Points) != 31 || p.Points[0].Plan != "1" || p.Now == nil || !p.Now.AtRisk || p.Now.Target != "1.03" || len(p.Spikes) != 1 {
		t.Fatalf("the plan %+v", p)
	}
	raw, _ := json.Marshal(p)
	for _, key := range []string{`"closing_at":"2026-10-04T06:27:00.000Z"`, `"level":"1.08"`, `"deviation":0.002`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("%s not in %s", key, raw)
		}
	}
}
