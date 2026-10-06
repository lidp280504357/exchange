package application

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	marketv1 "github.com/skill/exchange/api/gen/go/exchange/market/v1"
	riskv1 "github.com/skill/exchange/api/gen/go/exchange/risk/v1"
	"github.com/skill/exchange/internal/marketdata/domain"
)

// fakeRefMarks is the reference market's marks as MarkFeed keeps them.
type fakeRefMarks struct {
	mu       sync.Mutex
	mark     domain.ReferenceMark
	received time.Time
	has      bool
	settled  map[time.Time]domain.SettledFunding
	asked    map[time.Time]int
}

func (f *fakeRefMarks) Latest(string) (domain.ReferenceMark, time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mark, f.received, f.has
}

func (f *fakeRefMarks) Settled(_ string, at time.Time) (domain.SettledFunding, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked[at]++
	s, ok := f.settled[at]
	return s, ok
}

// followRig follows a reference market whose mark the test sets; the
// flag market.reference_mark is on while follow is.
type followRig struct {
	*marksRig
	ref    *fakeRefMarks
	follow bool
}

func newFollowRig(t *testing.T, now string) *followRig {
	t.Helper()
	r := &followRig{marksRig: newMarksRig(t, newMemStore(), now), ref: &fakeRefMarks{
		settled: map[time.Time]domain.SettledFunding{}, asked: map[time.Time]int{},
	}}
	r.marks.FollowReference(r.ref, func(symbol string) bool { return r.follow && symbol == perp.Symbol }, 0)
	return r
}

// stream sets the reference market's latest mark, arrived just now.
func (r *followRig) stream(mark, index, rate string, next time.Time) {
	r.ref.mu.Lock()
	defer r.ref.mu.Unlock()
	r.ref.mark = domain.ReferenceMark{
		Symbol: perp.Symbol, Mark: d(mark), Index: d(index), FundingRate: d(rate), HasRate: true, NextFunding: next, At: r.now,
	}
	r.ref.received, r.ref.has = r.now, true
}

// tick moves a second on, the reference market streaming its mark as it
// does every second unless silent.
func (r *followRig) tick(silent bool) {
	r.now = r.now.Add(time.Second)
	if !silent {
		r.ref.mu.Lock()
		r.ref.received, r.ref.mark.At = r.now, r.now
		r.ref.mu.Unlock()
	}
	r.marks.Tick(context.Background())
}

func lastMark(t *testing.T, msgs []any) *marketv1.MarkPriceUpdated {
	t.Helper()
	var last *marketv1.MarkPriceUpdated
	for _, m := range msgs {
		if p, ok := m.(*marketv1.MarkPriceUpdated); ok {
			last = p
		}
	}
	return last
}

func published(t *testing.T, r *followRig) []any {
	t.Helper()
	var out []any
	for _, m := range r.pub.take(t) {
		out = append(out, m)
	}
	return out
}

// A contract follows the reference market's mark price, index price and
// funding rate estimate while market.reference_mark is on for it and the
// market's mark is at most 10 seconds old (coin-M design §3.1). The
// self-computed prices go on underneath: they stand in whenever the
// market's mark is stale (MarkPriceSourceDegraded, not reduce-only), and
// the contract degrades only when neither is at hand.
func TestMarksFollowTheReferenceMarket(t *testing.T) {
	r := newFollowRig(t, "2026-10-06T01:00:00Z")
	period := at("2026-10-06T08:00:00Z")
	r.stream("60120", "60060", "0.00023", period)

	// The flag off: self-computed, the gap to the market watched.
	r.pub.take(t)
	r.tick(false)
	if m := lastMark(t, published(t, r)); m == nil || m.GetSource() != MarkSourcePlatform || m.GetMarkPrice() != "60000" {
		t.Fatalf("published %v", m)
	}
	p, _ := r.marks.Latest(perp.Symbol)
	if p.Source != MarkSourcePlatform || !p.Mark.Equal(d("60000")) || !p.Computed.Equal(d("60000")) || p.SourceDegraded {
		t.Fatalf("the flag off: %+v", p)
	}
	if got := gauge(t, r.marks.gap, perp.Symbol); got > -0.0019 || got < -0.0021 {
		t.Fatalf("gap %v, want 60000 / 60120 - 1", got)
	}
	if gauge(t, r.marks.source, perp.Symbol) != 0 {
		t.Fatal("the source gauge says the reference market")
	}

	// On: the market's prices and estimate.
	r.follow = true
	r.pub.take(t)
	r.tick(false)
	p, _ = r.marks.Latest(perp.Symbol)
	if p.Source != MarkSourceBinance || !p.Mark.Equal(d("60120")) || !p.Index.Equal(d("60060")) || !p.FundingRate.Equal(d("0.00023")) ||
		!p.Computed.Equal(d("60000")) || !p.ComputedIndex.Equal(d("60000")) {
		t.Fatalf("following: %+v", p)
	}
	msgs := published(t, r)
	// The basis is Binance's mark over its index: 60 / 60060.
	m := lastMark(t, msgs)
	if m == nil || m.GetMarkPrice() != "60120" || m.GetIndexPrice() != "60060" || m.GetFundingRate() != "0.00023" ||
		m.GetSource() != MarkSourceBinance || m.GetBasis() != "0.000999000999" {
		t.Fatalf("published %v", m)
	}
	var estimate *marketv1.FundingRateUpdated
	for _, msg := range msgs {
		if f, ok := msg.(*marketv1.FundingRateUpdated); ok {
			estimate = f
		}
	}
	if estimate == nil || estimate.GetSource() != MarkSourceBinance || estimate.GetFundingRate() != "0.00023" ||
		estimate.GetPremium() != "" || estimate.GetSamples() != 0 {
		t.Fatalf("the estimate when its source changed: %v", estimate)
	}
	if gauge(t, r.marks.source, perp.Symbol) != 1 {
		t.Fatal("the source gauge")
	}

	// The market goes silent: its mark holds for 10 seconds, then the
	// self-computed one stands in and the source is degraded.
	for range 10 {
		r.tick(true)
	}
	if p, _ := r.marks.Latest(perp.Symbol); p.Source != MarkSourceBinance || p.SourceDegraded {
		t.Fatalf("a 10-second-old reference mark still followed: %+v", p)
	}
	r.tick(true)
	p, _ = r.marks.Latest(perp.Symbol)
	if p.Source != MarkSourcePlatform || !p.Mark.Equal(d("60000")) || !p.SourceDegraded || p.Degraded {
		t.Fatalf("the market stale: %+v", p)
	}
	if gauge(t, r.marks.sourceDegraded, perp.Symbol) != 1 || gauge(t, r.marks.source, perp.Symbol) != 0 {
		t.Fatal("the source gauges while stale")
	}
	// Its own sources gone too: degraded 10 seconds after the last price.
	delete(r.sources, "BTC-USDT")
	r.store.takeOutbox(t)
	for range 9 {
		r.tick(true)
	}
	if len(r.store.takeOutbox(t)) != 0 {
		t.Fatal("degraded within 10 seconds")
	}
	r.tick(true)
	r.tick(true)
	degraded := false
	for _, m := range r.store.takeOutbox(t) {
		if _, ok := m.(*riskv1.SystemDegraded); ok {
			degraded = true
		}
	}
	if !degraded {
		t.Fatal("neither price: not degraded")
	}

	// The market back, its own sources still gone: the contract follows
	// the market, recovered.
	r.tick(false)
	p, _ = r.marks.Latest(perp.Symbol)
	if p.Source != MarkSourceBinance || p.SourceDegraded || p.Degraded || !p.Computed.IsZero() {
		t.Fatalf("the market back: %+v", p)
	}
	recovered := false
	for _, m := range r.store.takeOutbox(t) {
		if _, ok := m.(*riskv1.SystemRecovered); ok {
			recovered = true
		}
	}
	if !recovered || gauge(t, r.marks.sourceDegraded, perp.Symbol) != 0 {
		t.Fatal("not recovered")
	}
	// Its own sources stay away for a minute: no degradation while the
	// market's mark is fresh.
	for range 60 {
		r.tick(false)
	}
	for _, m := range r.store.takeOutbox(t) {
		if _, ok := m.(*riskv1.SystemDegraded); ok {
			t.Fatal("degraded while the reference mark is fresh")
		}
	}

	// The flag goes off: self-computed again (sources back), and no
	// stale-source alarm for a contract that does not follow.
	r.sources["BTC-USDT"] = []domain.SourcePrice{{Source: "binance", Price: d("60010")}}
	r.follow = false
	r.tick(true)
	for range 20 {
		r.tick(true)
	}
	if p, _ := r.marks.Latest(perp.Symbol); p.Source != MarkSourcePlatform || p.SourceDegraded || !p.Mark.Equal(d("60010")) {
		t.Fatalf("the flag off again: %+v", p)
	}
}

// Right after a start the reference market's stream is still connecting:
// the source is not reported degraded within the first 10 seconds.
func TestMarksGiveTheReferenceStreamTimeToConnect(t *testing.T) {
	r := newFollowRig(t, "2026-10-06T01:00:00Z")
	r.follow = true
	for range 10 {
		r.tick(true)
	}
	if p, _ := r.marks.Latest(perp.Symbol); p.Source != MarkSourcePlatform || p.SourceDegraded {
		t.Fatalf("the first 10 seconds: %+v", p)
	}
	r.tick(true)
	if p, _ := r.marks.Latest(perp.Symbol); !p.SourceDegraded {
		t.Fatalf("no reference mark after 11 seconds: %+v", p)
	}
}

// A period of a contract that follows the reference market settles at
// the rate and mark price the market settled it at, waited for up to two
// minutes; then at the last rate the market estimated for it; a period
// the market ends at another time settles at the self-computed rate.
func TestFundingSettlesAtTheReferenceMarketsRate(t *testing.T) {
	end := at("2026-10-06T08:00:00Z")
	settled := func(t *testing.T, r *followRig) *marketv1.FundingRateUpdated {
		t.Helper()
		for _, m := range published(t, r) {
			if f, ok := m.(*marketv1.FundingRateUpdated); ok && f.GetFinal() {
				return f
			}
		}
		return nil
	}

	t.Run("the settled rate", func(t *testing.T) {
		r := newFollowRig(t, "2026-10-06T07:59:00Z")
		r.follow = true
		r.book("60030", "60050") // a self-computed premium of 0.0005
		r.stream("60100", "60050", "0.00019", end)
		for range 60 { // up to 08:00:00: the period ended
			r.tick(false)
		}
		r.stream("60110", "60060", "0.00007", end.Add(8*time.Hour))
		r.pub.take(t)
		for range 30 {
			r.tick(false)
		}
		if f := settled(t, r); f != nil {
			t.Fatalf("settled before the market's rate came: %v", f)
		}
		if r.ref.asked[end] == 0 {
			t.Fatal("the market's settled rate was never asked for")
		}
		r.ref.mu.Lock()
		r.ref.settled[end] = domain.SettledFunding{Symbol: perp.Symbol, FundingTime: end.Add(time.Millisecond), Rate: d("0.00021"), Mark: d("60095.5")}
		r.ref.mu.Unlock()
		r.tick(false)
		f := settled(t, r)
		if f == nil || f.GetFundingRate() != "0.00021" || f.GetMarkPrice() != "60095.5" || !f.GetFundingTime().AsTime().Equal(end) ||
			f.GetSource() != MarkSourceBinance || f.GetPremium() != "" || f.GetSamples() != 0 {
			t.Fatalf("settled %v", f)
		}
		list, _ := r.marks.Settled(context.Background(), perp.Symbol, time.Time{}, end.Add(time.Hour), 10)
		if len(list) != 1 || !list[0].Rate.Equal(d("0.00021")) || !list[0].MarkPrice.Equal(d("60095.5")) || list[0].Source != MarkSourceBinance {
			t.Fatalf("history %+v", list)
		}
		// The next period's estimate is the market's.
		if p, _ := r.marks.Latest(perp.Symbol); !p.FundingRate.Equal(d("0.00007")) || !p.NextFunding.Equal(end.Add(8*time.Hour)) {
			t.Fatalf("the next period: %+v", p)
		}
	})

	t.Run("the market's last estimate", func(t *testing.T) {
		r := newFollowRig(t, "2026-10-06T07:59:00Z")
		r.follow = true
		r.stream("60100", "60050", "0.00019", end)
		for range 60 {
			r.tick(false)
		}
		r.stream("60110", "60060", "0.00007", end.Add(8*time.Hour))
		r.pub.take(t)
		for range 119 {
			r.tick(false)
		}
		if f := settled(t, r); f != nil {
			t.Fatalf("settled within the two minutes: %v", f)
		}
		r.tick(false)
		f := settled(t, r)
		if f == nil || f.GetFundingRate() != "0.00019" || f.GetMarkPrice() != "60110" || f.GetSource() != MarkSourceBinance {
			t.Fatalf("settled %v", f)
		}
	})

	t.Run("another period end", func(t *testing.T) {
		r := newFollowRig(t, "2026-10-06T07:59:00Z")
		r.follow = true
		r.book("60030", "60050")
		r.stream("60100", "60050", "0.00019", end.Add(-4*time.Hour)) // a 4-hour period on the market
		for range 60 {
			r.tick(false)
		}
		if p, _ := r.marks.Latest(perp.Symbol); !p.FundingRate.Equal(d("0.0001")) || p.Source != MarkSourceBinance {
			t.Fatalf("the market's rate followed for a period it ends elsewhere: %+v", p)
		}
		r.tick(false)
		f := settled(t, r)
		if f == nil || f.GetFundingRate() != "0.0001" || r.ref.asked[end] != 0 || f.GetSource() != MarkSourcePlatform ||
			f.GetPremium() != "0.0005" || f.GetSamples() != 59 {
			t.Fatalf("settled %v; asked %v", f, r.ref.asked)
		}
	})
}

// gauge reads one series of a gauge vector.
func gauge(t *testing.T, g *prometheus.GaugeVec, symbol string) float64 {
	t.Helper()
	var m dto.Metric
	if err := g.WithLabelValues(symbol).Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetGauge().GetValue()
}
