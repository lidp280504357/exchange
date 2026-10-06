package binance

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"time"
)

// The weights of the requests Source sends, as Binance's API docs give them.
func TestRequestWeights(t *testing.T) {
	q := func(kv ...string) url.Values {
		v := url.Values{}
		for i := 0; i+1 < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return v
	}
	many := make([]string, 87)
	for i := range many {
		many[i] = fmt.Sprintf("C%dUSDT", i)
	}
	list, _ := json.Marshal(many)
	for _, c := range []struct {
		spot bool
		path string
		q    url.Values
		want int
	}{
		{true, "/api/v3/depth", q("limit", "1000"), 50},
		{false, "/fapi/v1/depth", q("limit", "1000"), 20},
		{false, "/dapi/v1/depth", q("limit", "500"), 10},
		{true, "/api/v3/aggTrades", q("limit", "100"), 4},
		{false, "/fapi/v1/aggTrades", q("limit", "100"), 20},
		{true, "/api/v3/klines", q("limit", "1000"), 2},
		{false, "/fapi/v1/klines", q("limit", "1000"), 5},
		{false, "/dapi/v1/klines", q("limit", "1"), 1},
		{true, "/api/v3/ticker/24hr", q("symbols", `["BTCUSDT","ETHUSDT"]`), 2},
		{true, "/api/v3/ticker/24hr", q("symbols", string(list)), 40},
		{false, "/fapi/v1/ticker/24hr", q(), 40},
		{false, "/fapi/v1/fundingRate", q("limit", "1000"), 1},
	} {
		if got := requestWeight(c.spot, c.path, c.q); got != c.want {
			t.Errorf("%s %v: %d, want %d", c.path, c.q, got, c.want)
		}
	}
}

// A host's budget is half of Binance's limit a minute: a request waits for
// enough of the oldest weight to leave the minute. Binance's own count at
// three quarters of the limit holds the host until the next minute.
func TestHostWeight(t *testing.T) {
	if spot := newHostWeight(spotWeightLimit, 80); spot.budget != 4800 {
		t.Fatalf("spot budget %d", spot.budget)
	}
	h := newHostWeight(futuresWeightLimit, 50) // 1200 a minute
	t0 := time.Date(2026, 10, 7, 1, 0, 10, 0, time.UTC)
	for i := range 60 {
		h.take(t0.Add(time.Duration(i)*time.Second), 20)
	}
	at := t0.Add(59 * time.Second)
	if d := h.delay(at, 20); d != time.Second {
		t.Fatalf("one more 20 waits for the first to leave: %s", d)
	}
	if d := h.delay(at, 40); d != 2*time.Second {
		t.Fatalf("a 40 waits for two: %s", d)
	}
	if d := h.delay(t0.Add(2*time.Minute), 20); d != 0 || len(h.spent) != 0 {
		t.Fatalf("a minute later: %s, %d left", d, len(h.spent))
	}
	// Heavier than the whole budget: it goes once nothing else counts.
	h.take(t0.Add(2*time.Minute), 20)
	if d := h.delay(t0.Add(2*time.Minute), 1500); d != time.Minute {
		t.Fatalf("a 1500: %s", d)
	}
	if d := h.delay(t0.Add(3*time.Minute), 1500); d != 0 {
		t.Fatalf("a 1500 alone: %s", d)
	}
	// Binance counts 1799 of 2400: nothing changes; 1800: held until a
	// second past the next minute (01:04:01).
	now := t0.Add(3 * time.Minute)
	h.observe(now, 1799)
	if d := h.delay(now, 1); d != 0 {
		t.Fatalf("under three quarters: %s", d)
	}
	h.observe(now, 1800)
	if d := h.delay(now, 1); d != 51*time.Second {
		t.Fatalf("at three quarters: %s", d)
	}
}
