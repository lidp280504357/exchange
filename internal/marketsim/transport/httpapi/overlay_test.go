package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketsim/domain"
)

// An OVERLAY renders its pair, target factor, ramps, risk, its factor and
// progress now and its course's prices (J0 contract §3.4); the simulated
// market's own events render none of them.
func TestAnOverlayRenders(t *testing.T) {
	start := time.Now().Add(-10 * time.Second)
	e := domain.Event{
		ID: "o1", Type: domain.EventOverlay, Symbol: "BTC-USDT", TargetFactor: 1.2, RampUp: 20 * time.Second, Hold: 0,
		RampDown: 5 * time.Second, Risk: true, StartsAt: start, StartedAt: start, Status: domain.EventRunning,
		BasePrice: decimal.RequireFromString("100000"), PeakPrice: decimal.RequireFromString("110000"), Price: decimal.RequireFromString("120000"),
	}
	j := eventJSON(e)
	if j.Symbol != "BTC-USDT" || *j.TargetFactor != 1.2 || j.RampUpS != 20 || j.RampDownS != 5 || !*j.Risk || *j.FactorNow < 1.09 ||
		*j.FactorNow > 1.11 || *j.Progress < 0.39 || *j.Progress > 0.41 || *j.BasePrice != "100000" || *j.PeakPrice != "110000" ||
		j.EndReferencePrice != nil {
		t.Fatalf("running %+v", j)
	}
	e.Status, e.EndedAt = domain.EventDone, time.Now()
	e.EndReferencePrice, e.EndPlatformPrice = decimal.RequireFromString("100100"), decimal.RequireFromString("100100")
	if j := eventJSON(e); *j.FactorNow != 1 || *j.Progress != 1 || *j.EndPlatformPrice != "100100" {
		t.Fatalf("done %+v", j)
	}
	raw, _ := json.Marshal(eventJSON(domain.Event{ID: "j1", Type: domain.EventJump, Size: 0.1, StartsAt: start}))
	for _, key := range []string{"symbol", "target_factor", "factor_now", "ramp_up_seconds", "base_price"} {
		if strings.Contains(string(raw), `"`+key+`"`) {
			t.Fatalf("a jump renders %s: %s", key, raw)
		}
	}
}
