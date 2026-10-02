package application

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"

	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	riskv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/risk/v1"
	"github.com/lidp280504357/exchange/internal/marketdata/domain"
	"github.com/lidp280504357/exchange/internal/marketdata/ports"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
)

type contractList []ports.Contract

func (c contractList) Listed(context.Context, string) (bool, error)        { return true, nil }
func (c contractList) Symbols(context.Context) ([]string, error)           { return nil, nil }
func (c contractList) Contracts(context.Context) ([]ports.Contract, error) { return c, nil }
func (c contractList) Pairs(context.Context) ([]ports.Pair, error)         { return nil, nil }
func (c contractList) Ranks(context.Context) (map[string]int32, error)     { return nil, nil }
func (c contractList) SetPairStatus(context.Context, string, string, string) (string, error) {
	return "", ErrUnknownSymbol
}

type fakeSources map[string][]domain.SourcePrice

func (f fakeSources) Prices(symbol string) []domain.SourcePrice {
	return append([]domain.SourcePrice(nil), f[symbol]...)
}

type recorder struct {
	mu   sync.Mutex
	recs []kafka.Record
	fail bool
}

func (r *recorder) Publish(_ context.Context, recs ...kafka.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return errors.New("broker down")
	}
	r.recs = append(r.recs, recs...)
	return nil
}

// take returns the payloads published since the last call.
func (r *recorder) take(t *testing.T) []proto.Message {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []proto.Message
	for _, rec := range r.recs {
		var env eventv1.Envelope
		if err := proto.Unmarshal(rec.Envelope, &env); err != nil {
			t.Fatal(err)
		}
		m, err := env.GetPayload().UnmarshalNew()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	r.recs = nil
	return out
}

var perp = ports.Contract{
	Symbol: "BTC-USDT-PERP", IndexSymbol: "BTC-USDT", Status: "TRADING", FundingIntervalHours: 8,
	InterestRate: d("0.0001"), FundingCap: d("0.0075"), ImpactNotional: d("10000"),
}

type marksRig struct {
	store   *memStore
	svc     *Service
	sources fakeSources
	pub     *recorder
	marks   *Marks
	now     time.Time
}

func newMarksRig(t *testing.T, store *memStore, now string) *marksRig {
	t.Helper()
	r := &marksRig{store: store, sources: fakeSources{"BTC-USDT": {{Source: "binance", Price: d("60000")}}}, pub: &recorder{}, now: at(now)}
	r.svc = newService(t, store, &r.now)
	events := event.NewFactory("market-data-service", "test")
	pusher := NewPusher(r.svc, r.pub, events, prometheus.NewRegistry())
	r.marks = NewMarks(r.svc, contractList{perp}, r.sources, store, pusher, r.pub, events, MarksConfig{MinSources: 1},
		slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	r.marks.now = func() time.Time { return r.now }
	if err := r.marks.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return r
}

// run ticks every second for n seconds.
func (r *marksRig) run(n int) {
	for range n {
		r.now = r.now.Add(time.Second)
		r.marks.Tick(context.Background())
	}
}

func (r *marksRig) book(bid, ask string) {
	r.svc.OnDepth(&marketv1.DepthSnapshot{
		Symbol: perp.Symbol, Sequence: r.now.UnixNano(),
		Bids: []*marketv1.PriceLevel{{Price: bid, Quantity: "10"}}, Asks: []*marketv1.PriceLevel{{Price: ask, Quantity: "10"}},
	})
}

func TestMarkPriceFollowsTheBookAroundTheIndex(t *testing.T) {
	r := newMarksRig(t, newMemStore(), "2026-09-30T01:00:00Z")
	r.run(1)
	p, ok := r.marks.Latest(perp.Symbol)
	if !ok || !p.Mark.Equal(d("60000")) || !p.Index.Equal(d("60000")) || !p.FundingRate.Equal(d("0.0001")) ||
		!p.NextFunding.Equal(at("2026-09-30T08:00:00Z")) {
		t.Fatalf("an empty book marks at the index, funding at the interest rate: %+v", p)
	}
	var kinds []string
	for _, m := range r.pub.take(t) {
		kinds = append(kinds, string(m.ProtoReflect().Descriptor().Name()))
	}
	if len(kinds) != 3 || kinds[0] != "IndexPriceUpdated" || kinds[1] != "MarkPriceUpdated" || kinds[2] != "FundingRateUpdated" {
		t.Fatalf("published %v", kinds)
	}

	// The contract trades 2% above the index: the mark rises to 1% above
	// at most, and the impact bid (10 BTC deep) makes the premium.
	r.book("61190", "61210")
	r.run(300)
	p, _ = r.marks.Latest(perp.Symbol)
	if !p.Mark.Equal(d("60600")) {
		t.Fatalf("mark %s, want the 1%% bound", p.Mark)
	}
	if p.Samples != 301 || !p.FundingRate.Equal(d("0.0075")) {
		t.Fatalf("funding estimate %s after %d samples, want the cap", p.FundingRate, p.Samples)
	}
	// An unchanged estimate is pushed again every 10 seconds only.
	funding := 0
	for _, m := range r.pub.take(t) {
		if f, ok := m.(*marketv1.FundingRateUpdated); ok && !f.GetFinal() {
			funding++
		}
	}
	if funding > 35 {
		t.Fatalf("%d funding estimates in 300 seconds", funding)
	}
}

func TestFundingPeriodsSettleAtTheBoundary(t *testing.T) {
	store := newMemStore()
	r := newMarksRig(t, store, "2026-09-30T07:58:00Z")
	r.book("60030", "60050") // premium (60030 - 60000) / 60000 = 0.0005
	r.run(119)               // up to 07:59:59
	r.pub.take(t)
	r.run(1) // 08:00:00 starts the next period
	var final *marketv1.FundingRateUpdated
	for _, m := range r.pub.take(t) {
		if f, ok := m.(*marketv1.FundingRateUpdated); ok && f.GetFinal() {
			final = f
		}
	}
	if final == nil {
		t.Fatal("no settled rate")
	}
	// 119 samples of 0.0005: 0.0005 + clamp(0.0001 - 0.0005, +-0.0005)
	// = 0.0001.
	if final.GetFundingRate() != "0.0001" || final.GetSamples() != 119 || final.GetPremium() != "0.0005" ||
		!final.GetFundingTime().AsTime().Equal(at("2026-09-30T08:00:00Z")) || final.GetMarkPrice() == "" {
		t.Fatalf("settled %v", final)
	}
	list, err := r.marks.Settled(context.Background(), perp.Symbol, time.Time{}, at("2026-10-01T00:00:00Z"), 10)
	if err != nil || len(list) != 1 || !list[0].Rate.Equal(d("0.0001")) {
		t.Fatalf("history %+v %v", list, err)
	}
	p, _ := r.marks.Latest(perp.Symbol)
	if p.Samples != 1 || !p.NextFunding.Equal(at("2026-09-30T16:00:00Z")) {
		t.Fatalf("the next period %+v", p)
	}
}

func TestARestartKeepsTheSamplesAndSettlesWhatEnded(t *testing.T) {
	store := newMemStore()
	r := newMarksRig(t, store, "2026-09-30T07:57:00Z")
	r.book("60120", "60140") // premium 0.002
	r.run(90)                // saved on the first tick and a minute later: 61 samples
	// Down from 07:58:30 to 08:00:30: the samples since the last save are
	// lost, the period ended while down.
	r2 := newMarksRig(t, store, "2026-09-30T08:00:30Z")
	r2.book("60120", "60140")
	r2.run(1)
	list, err := r2.marks.Settled(context.Background(), perp.Symbol, time.Time{}, at("2026-10-01T00:00:00Z"), 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("history %+v %v", list, err)
	}
	// 0.002 + clamp(0.0001 - 0.002, -0.0005) = 0.0015.
	if s := list[0]; s.Samples != 61 || !s.Rate.Equal(d("0.0015")) || !s.FundingTime.Equal(at("2026-09-30T08:00:00Z")) {
		t.Fatalf("settled after the restart %+v", s)
	}

	// A restart within a period continues its samples.
	r2.run(120)
	r3 := newMarksRig(t, store, r2.now.Format(time.RFC3339))
	r3.run(1)
	p, _ := r3.marks.Latest(perp.Symbol)
	if p.Samples < 61 {
		t.Fatalf("restored %d samples", p.Samples)
	}
}

func TestAContractWithoutAnIndexDegradesAfterTenSeconds(t *testing.T) {
	r := newMarksRig(t, newMemStore(), "2026-09-30T01:00:00Z")
	r.run(5)
	r.pub.take(t)
	delete(r.sources, "BTC-USDT")
	r.run(9)
	if len(r.store.takeOutbox(t)) != 0 {
		t.Fatal("degraded within 10 seconds")
	}
	r.store.emitFails = true // the report is retried
	r.run(1)
	r.store.emitFails = false
	r.run(1)
	degraded := 0
	// Risk events go through the outbox, derived market data directly.
	for _, m := range r.store.takeOutbox(t) {
		if e, ok := m.(*riskv1.SystemDegraded); ok {
			degraded++
			if e.GetReason() != ReasonIndexSources || e.GetSymbol() != perp.Symbol || e.GetLastMarkAt() == nil {
				t.Fatalf("degraded %v", e)
			}
		}
	}
	for _, m := range r.pub.take(t) {
		if _, ok := m.(*riskv1.SystemDegraded); ok {
			t.Fatal("a risk event published directly")
		}
		if _, ok := m.(*marketv1.MarkPriceUpdated); ok {
			t.Fatal("a mark price without an index")
		}
	}
	if p, _ := r.marks.Latest(perp.Symbol); degraded != 1 || !p.Degraded || !p.Mark.Equal(d("60000")) {
		t.Fatalf("%d reports; latest %+v", degraded, p)
	}
	r.run(30)
	for _, m := range r.store.takeOutbox(t) {
		if _, ok := m.(*riskv1.SystemDegraded); ok {
			t.Fatal("reported twice")
		}
	}
	r.sources["BTC-USDT"] = []domain.SourcePrice{{Source: "binance", Price: d("61000")}}
	r.run(1)
	recovered := false
	for _, m := range r.store.takeOutbox(t) {
		if _, ok := m.(*riskv1.SystemRecovered); ok {
			recovered = true
		}
	}
	if p, _ := r.marks.Latest(perp.Symbol); !recovered || p.Degraded || !p.Mark.Equal(d("61000")) {
		t.Fatalf("recovered %v; latest %+v", recovered, p)
	}
}

func TestIndexWeightsAndMinimumSources(t *testing.T) {
	r := newMarksRig(t, newMemStore(), "2026-09-30T01:00:00Z")
	r.marks.minSources = 2
	r.marks.weights = map[string]int32{"b": 3, "off": 0}
	r.sources["BTC-USDT"] = []domain.SourcePrice{
		{Source: "a", Price: d("60000")}, {Source: "b", Price: d("60100")}, {Source: "off", Price: d("59000")},
	}
	r.run(1)
	p, _ := r.marks.Latest(perp.Symbol)
	if !p.Index.Equal(d("60100")) || len(p.Components) != 3 {
		t.Fatalf("weighted index %s %+v", p.Index, p.Components)
	}
	r.sources["BTC-USDT"] = r.sources["BTC-USDT"][:1]
	r.run(1)
	if p, _ := r.marks.Latest(perp.Symbol); !p.At.Equal(r.now.Add(-time.Second)) {
		t.Fatalf("one source of the two required made a price at %s", p.At)
	}
}

// The platform coin's index pair follows no reference market: its index
// is the platform's own price, the middle of the engine's book, else a
// recent trade. A followed pair keeps the reference market's sources only.
func TestPlatformIndex(t *testing.T) {
	ctx := context.Background()
	now := at("2026-10-02T10:00:00Z")
	svc := newService(t, newMemStore(), &now)
	refs := NewReferenceMap(testListing(), slog.New(slog.DiscardHandler))
	idx := PlatformIndex{Feed: fakeSources{"BTC-USDT": {{Source: "binance", Price: d("60000")}}}, Svc: svc, Refs: refs}
	if got := idx.Prices("BTC-USDT"); len(got) != 1 || got[0].Source != "binance" {
		t.Fatalf("a followed pair: %+v", got)
	}
	if got := idx.Prices("ASTRA-USDT"); len(got) != 0 {
		t.Fatalf("no market yet: %+v", got)
	}
	trade := trade(1, "1.02", "100", "2026-10-02T09:58:00Z")
	trade.Symbol = "ASTRA-USDT"
	if _, err := svc.OnTrades(ctx, []domain.Trade{trade}); err != nil {
		t.Fatal(err)
	}
	if got := idx.Prices("ASTRA-USDT"); len(got) != 1 || got[0].Source != SourcePlatform || !got[0].Price.Equal(d("1.02")) {
		t.Fatalf("a recent trade: %+v", got)
	}
	svc.OnDepth(&marketv1.DepthSnapshot{
		Symbol: "ASTRA-USDT", Sequence: 1, Bids: []*marketv1.PriceLevel{{Price: "1.01", Quantity: "10"}},
		Asks: []*marketv1.PriceLevel{{Price: "1.03", Quantity: "10"}},
	})
	if got := idx.Prices("ASTRA-USDT"); len(got) != 1 || !got[0].Price.Equal(d("1.02")) {
		t.Fatalf("the middle of the book: %+v", got)
	}
	svc.OnDepth(&marketv1.DepthSnapshot{Symbol: "ASTRA-USDT", Sequence: 2, Bids: []*marketv1.PriceLevel{{Price: "1.01", Quantity: "10"}}})
	now = now.Add(6 * time.Minute)
	if got := idx.Prices("ASTRA-USDT"); len(got) != 0 {
		t.Fatalf("a one-sided book and an old trade: %+v", got)
	}
}
