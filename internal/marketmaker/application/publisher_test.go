package application

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventv1 "github.com/skill/exchange/api/gen/go/exchange/event/v1"
	marketv1 "github.com/skill/exchange/api/gen/go/exchange/market/v1"
	orderv1 "github.com/skill/exchange/api/gen/go/exchange/order/v1"
	"github.com/skill/exchange/internal/marketmaker/domain"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/kafka"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

type specList []domain.Spec

func (s specList) Specs(context.Context) ([]domain.Spec, error) { return s, nil }

func (specList) Backed(context.Context) ([]string, error) { return []string{"USDT", "BTC", "ETH"}, nil }

type fakeHouse struct {
	holdings  domain.Holdings
	contracts *domain.ContractAccount
}

func (h fakeHouse) Holdings(context.Context) (domain.Holdings, error) { return h.holdings, nil }
func (h fakeHouse) Contracts(context.Context, []string) (domain.ContractAccount, error) {
	return *h.contracts, nil
}

type onFlags struct {
	mu   sync.Mutex
	deny []string
}

func (f *onFlags) Enabled(key string, s flags.Subject) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return key != flags.KeyHouseLiquidity || !slices.Contains(f.deny, s.Symbol)
}

type records struct {
	mu   sync.Mutex
	recs []kafka.Record
}

func (r *records) Publish(_ context.Context, recs ...kafka.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recs = append(r.recs, recs...)
	return nil
}

// take returns the books published since the last call, with their topics.
func (r *records) take(t *testing.T) (topics []string, books []*orderv1.ReferenceBookUpdate) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range r.recs {
		var env eventv1.Envelope
		if err := proto.Unmarshal(rec.Envelope, &env); err != nil {
			t.Fatal(err)
		}
		var m orderv1.ReferenceBookUpdate
		if err := env.GetPayload().UnmarshalTo(&m); err != nil {
			t.Fatal(err)
		}
		topics, books = append(topics, rec.Topic), append(books, &m)
	}
	r.recs = nil
	return topics, books
}

var (
	btcSpec  = domain.Spec{Symbol: "BTC-USDT", Base: "BTC", Quote: "USDT", TickSize: d("0.01"), LotSize: d("0.0001")}
	perpSpec = domain.Spec{Symbol: "BTC-USDT-PERP", Base: "BTC", Quote: "USDT", TickSize: d("0.1"), LotSize: d("0.001"), Contract: true}
)

func levels(pq ...string) []*marketv1.PriceLevel {
	var out []*marketv1.PriceLevel
	for i := 0; i+1 < len(pq); i += 2 {
		out = append(out, &marketv1.PriceLevel{Price: pq[i], Quantity: pq[i+1]})
	}
	return out
}

// rigContracts is HOUSE's FUTURES account in newRig: short 0.5 BTC-USDT-PERP
// with 1,000,000 of equity. Tests may change it, then refresh.
var rigContracts domain.ContractAccount

func newRig(t *testing.T) (*Publisher, *records, *onFlags, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	rigContracts = domain.ContractAccount{
		Positions: map[string]decimal.Decimal{"BTC-USDT-PERP": d("-0.5")},
		Exposure:  map[string]decimal.Decimal{"USDT": d("25000")}, Equity: map[string]decimal.Decimal{"USDT": d("1000000")},
	}
	house := fakeHouse{
		holdings:  domain.Holdings{"BTC": d("0.4"), "USDT": d("500000")},
		contracts: &rigContracts,
	}
	fl := &onFlags{}
	rec := &records{}
	cfg := DefaultConfig()
	cfg.HouseUser = "house"
	p := New(cfg, specList{btcSpec, perpSpec}, house, fl, rec, event.NewFactory("market-maker", "test"), slog.New(slog.DiscardHandler),
		prometheus.NewRegistry())
	p.now = func() time.Time { return now }
	p.refresh(context.Background())
	return p, rec, fl, &now
}

func TestHouseOffersTheReferenceBookWithinItsRooms(t *testing.T) {
	p, rec, _, _ := newRig(t)
	ctx := context.Background()
	p.OnSnapshot(&marketv1.DepthSnapshot{
		Symbol: "BTC-USDT", Sequence: 10, Reference: true, TakenAt: timestamppb.Now(),
		Bids: levels("49999.99", "0.1", "49999.985", "2"), Asks: levels("50000.01", "1", "50000.02", "3"),
	})
	if err := p.publish(ctx, p.round()); err != nil {
		t.Fatal(err)
	}
	topics, books := rec.take(t)
	if len(books) != 1 || topics[0] != event.TopicOrderReferences {
		t.Fatalf("books %v on %v", books, topics)
	}
	b := books[0]
	// Bids round down to the tick; each level is worth at most 20,000.
	if b.GetHouseUserId() != "house" || b.GetBids()[0].GetPrice() != "49999.99" || b.GetBids()[0].GetQuantity() != "0.1" ||
		b.GetBids()[1].GetPrice() != "49999.98" || b.GetBids()[1].GetQuantity() != "0.4" || b.GetAsks()[0].GetQuantity() != "0.3999" {
		t.Fatalf("levels %v", b)
	}
	// 0.4 BTC less 1,000 USDT's worth to sell; 100,000's worth to buy.
	if b.GetSellRoom() != "0.38" || b.GetBuyRoom() != "1.6" {
		t.Fatalf("rooms buy %s sell %s", b.GetBuyRoom(), b.GetSellRoom())
	}
	// Unchanged: nothing until the heartbeat.
	if err := p.publish(ctx, p.round()); err != nil {
		t.Fatal(err)
	}
	if _, again := rec.take(t); len(again) != 0 {
		t.Fatalf("republished an unchanged book: %v", again)
	}
}

func TestHouseStopsWhereItMustNotTrade(t *testing.T) {
	p, rec, fl, now := newRig(t)
	ctx := context.Background()
	p.OnSnapshot(&marketv1.DepthSnapshot{Symbol: "BTC-USDT", Sequence: 10, Reference: true, Bids: levels("50000", "1"), Asks: levels("50001", "1")})
	_ = p.publish(ctx, p.round())
	rec.take(t)

	fl.mu.Lock()
	fl.deny = []string{"BTC-USDT"} // market.house_liquidity off for the pair
	fl.mu.Unlock()
	_ = p.publish(ctx, p.round())
	if _, books := rec.take(t); len(books) != 1 || len(books[0].GetBids())+len(books[0].GetAsks()) != 0 || books[0].GetBuyRoom() != "" {
		t.Fatalf("an empty book takes the liquidity away: %v", books)
	}
	_ = p.publish(ctx, p.round())
	if _, books := rec.take(t); len(books) != 0 {
		t.Fatalf("the empty book goes once: %v", books)
	}
	fl.mu.Lock()
	fl.deny = nil
	fl.mu.Unlock()

	// A missed update makes the book unusable until the next snapshot.
	p.OnUpdate(&marketv1.DepthUpdate{Symbol: "BTC-USDT", Sequence: 12, PrevSequence: 11, Reference: true, Bids: levels("50000", "0")})
	_ = p.publish(ctx, p.round())
	if _, books := rec.take(t); len(books) != 0 {
		t.Fatalf("offered a book with a gap: %v", books)
	}
	p.OnSnapshot(&marketv1.DepthSnapshot{Symbol: "BTC-USDT", Sequence: 13, Reference: true, Bids: levels("50000", "1"), Asks: levels("50001", "1")})
	_ = p.publish(ctx, p.round())
	if _, books := rec.take(t); len(books) != 1 || len(books[0].GetBids()) != 1 {
		t.Fatalf("after the snapshot: %v", books)
	}

	// A book without news for Stale is not offered.
	*now = now.Add(p.cfg.Stale)
	_ = p.publish(ctx, p.round())
	if _, books := rec.take(t); len(books) != 1 || len(books[0].GetBids()) != 0 {
		t.Fatalf("a stale book: %v", books)
	}
	// The platform's own book (the reference not shown) means no HOUSE.
	p.OnSnapshot(&marketv1.DepthSnapshot{Symbol: "BTC-USDT", Sequence: 14, Bids: levels("50000", "1")})
	if _, ok := p.books["BTC-USDT"]; ok {
		t.Fatal("kept a book that is not the reference market's")
	}
}

// A pair or contract that leaves TRADING gets its empty book in the next
// round (instrument.events), not after the next read of the specs; one that
// trades again is offered once the specs are read again, at once
// (requirements §761, D2 2026-10-03).
func TestHouseLeavesAHaltedSymbolAtOnce(t *testing.T) {
	p, rec, _, now := newRig(t)
	ctx := context.Background()
	p.OnSnapshot(&marketv1.DepthSnapshot{Symbol: "BTC-USDT", Sequence: 1, Reference: true, Bids: levels("50000", "1"), Asks: levels("50001", "1")})
	p.OnSnapshot(&marketv1.DepthSnapshot{Symbol: "BTC-USDT-PERP", Sequence: 1, Reference: true, Bids: levels("49999.9", "5"), Asks: levels("50000.1", "5")})
	_ = p.publish(ctx, p.round())
	if _, books := rec.take(t); len(books) != 2 {
		t.Fatalf("both offered: %v", books)
	}
	p.OnStatus("BTC-USDT", "HALT")
	p.OnStatus("BTC-USDT-PERP", "HALT")
	_ = p.publish(ctx, p.round())
	topics, books := rec.take(t)
	empty := map[string]string{}
	for i, b := range books {
		if len(b.GetBids())+len(b.GetAsks()) == 0 && b.GetHouseUserId() == "house" {
			empty[b.GetSymbol()] = topics[i]
		}
	}
	if len(books) != 2 || empty["BTC-USDT"] != event.TopicOrderReferences || empty["BTC-USDT-PERP"] != event.TopicDerivOrderReferences {
		t.Fatalf("empty books on halt: %v on %v", books, topics)
	}
	*now = now.Add(5 * time.Second) // past the heartbeat: still nothing for them
	_ = p.publish(ctx, p.round())
	if _, books := rec.take(t); len(books) != 0 {
		t.Fatalf("the empty books go once: %v", books)
	}
	p.OnStatus("BTC-USDT", "TRADING")
	p.refresh(ctx)
	p.OnSnapshot(&marketv1.DepthSnapshot{Symbol: "BTC-USDT", Sequence: 2, Reference: true, Bids: levels("50000", "1"), Asks: levels("50001", "1")})
	p.OnSnapshot(&marketv1.DepthSnapshot{Symbol: "BTC-USDT-PERP", Sequence: 2, Reference: true, Bids: levels("49999.9", "5"), Asks: levels("50000.1", "5")})
	_ = p.publish(ctx, p.round())
	if _, books := rec.take(t); len(books) != 2 || len(books[0].GetBids())+len(books[1].GetBids()) == 0 {
		t.Fatalf("offered again once trading (the specs read again): %v", books)
	}
}

func TestContractsGoToTheirEngineWithTheirRooms(t *testing.T) {
	p, rec, _, now := newRig(t)
	ctx := context.Background()
	p.OnSnapshot(&marketv1.DepthSnapshot{Symbol: "BTC-USDT-PERP", Sequence: 1, Reference: true, Bids: levels("49999.9", "5"), Asks: levels("50000.1", "5")})
	// An update applies on top of the snapshot.
	p.OnUpdate(&marketv1.DepthUpdate{Symbol: "BTC-USDT-PERP", Sequence: 2, PrevSequence: 1, Reference: true, Asks: levels("50000.1", "2", "50000.2", "1")})
	_ = p.publish(ctx, p.round())
	topics, books := rec.take(t)
	if len(books) != 1 || topics[0] != event.TopicDerivOrderReferences || len(books[0].GetAsks()) != 2 ||
		books[0].GetAsks()[0].GetQuantity() != "0.399" || books[0].GetAsks()[1].GetPrice() != "50000.2" {
		t.Fatalf("books %v on %v", books, topics)
	}
	// HOUSE short 0.5 at a 50,000 mid: 100,000 caps the position either way.
	if books[0].GetBuyRoom() != "2.5" || books[0].GetSellRoom() != "1.5" {
		t.Fatalf("rooms buy %s sell %s", books[0].GetBuyRoom(), books[0].GetSellRoom())
	}
	// A loss leaves HOUSE 2,000 of equity: ten times that is less than its
	// positions are worth, so it only reduces them.
	rigContracts.Equity["USDT"] = d("2000")
	*now = now.Add(2 * time.Second) // the holdings are due again, and so is the heartbeat
	p.refresh(ctx)
	_ = p.publish(ctx, p.round())
	_, books = rec.take(t)
	if len(books) != 1 || books[0].GetBuyRoom() != "0.5" || books[0].GetSellRoom() != "0" {
		t.Fatalf("over its leverage: %v", books)
	}
}

// The contracts HOUSE offers share the room its positions may still grow
// by: two offered, each may take half of it before HOUSE's positions are
// read again.
func TestTheContractsShareTheRoom(t *testing.T) {
	now := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	// Ten times 2,600 of equity less 25,000 of positions: 1,000 to grow by.
	account := domain.ContractAccount{
		Positions: map[string]decimal.Decimal{"BTC-USDT-PERP": d("-0.5")},
		Exposure:  map[string]decimal.Decimal{"USDT": d("25000")}, Equity: map[string]decimal.Decimal{"USDT": d("2600")},
	}
	ethPerp := domain.Spec{Symbol: "ETH-USDT-PERP", Base: "ETH", Quote: "USDT", TickSize: d("0.01"), LotSize: d("0.001"), Contract: true}
	rec := &records{}
	cfg := DefaultConfig()
	cfg.HouseUser = "house"
	p := New(cfg, specList{btcSpec, perpSpec, ethPerp}, fakeHouse{holdings: domain.Holdings{"USDT": d("500000")}, contracts: &account},
		&onFlags{}, rec, event.NewFactory("market-maker", "test"), slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	p.now = func() time.Time { return now }
	ctx := context.Background()
	p.refresh(ctx)
	p.OnSnapshot(&marketv1.DepthSnapshot{Symbol: "BTC-USDT-PERP", Sequence: 1, Reference: true, Bids: levels("49999.9", "5"), Asks: levels("50000.1", "5")})
	p.OnSnapshot(&marketv1.DepthSnapshot{Symbol: "ETH-USDT-PERP", Sequence: 1, Reference: true, Bids: levels("2499.99", "5"), Asks: levels("2500.01", "5")})
	_ = p.publish(ctx, p.round())
	_, books := rec.take(t)
	rooms := map[string][2]string{}
	for _, b := range books {
		rooms[b.GetSymbol()] = [2]string{b.GetBuyRoom(), b.GetSellRoom()}
	}
	// 500 each: 0.01 BTC beyond the short's 0.5 back, 0.2 ETH either way.
	if rooms["BTC-USDT-PERP"] != [2]string{"0.51", "0.01"} || rooms["ETH-USDT-PERP"] != [2]string{"0.2", "0.2"} {
		t.Fatalf("rooms %v", rooms)
	}
	// 60 left: a half is less than BTC's lot of 50, so BTC's share is one
	// lot rather than nothing; ETH's lot is 2.50, its share the half.
	account.Equity["USDT"] = d("2506")
	now = now.Add(3 * time.Second)
	p.refresh(ctx)
	p.OnSnapshot(&marketv1.DepthSnapshot{Symbol: "BTC-USDT-PERP", Sequence: 2, Reference: true, Bids: levels("49999.9", "5"), Asks: levels("50000.1", "5")})
	p.OnSnapshot(&marketv1.DepthSnapshot{Symbol: "ETH-USDT-PERP", Sequence: 2, Reference: true, Bids: levels("2499.99", "5"), Asks: levels("2500.01", "5")})
	_ = p.publish(ctx, p.round())
	_, books = rec.take(t)
	for _, b := range books {
		rooms[b.GetSymbol()] = [2]string{b.GetBuyRoom(), b.GetSellRoom()}
	}
	if rooms["BTC-USDT-PERP"] != [2]string{"0.501", "0.001"} || rooms["ETH-USDT-PERP"] != [2]string{"0.012", "0.012"} {
		t.Fatalf("small room %v", rooms)
	}
}

// The USDT HOUSE holds above the safety is shared by the spot books it
// offers that spend it (review M1, ADR-0015); a book it stops offering
// takes no share (review of 569a958).
func TestTheSpotBooksShareTheQuote(t *testing.T) {
	now := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	ethSpec := domain.Spec{Symbol: "ETH-USDT", Base: "ETH", Quote: "USDT", TickSize: d("0.01"), LotSize: d("0.001")}
	account := domain.ContractAccount{Positions: map[string]decimal.Decimal{}}
	rec := &records{}
	fl := &onFlags{}
	cfg := DefaultConfig()
	cfg.HouseUser = "house"
	// 41,000 USDT: 40,000 above the safety of 1,000.
	p := New(cfg, specList{btcSpec, ethSpec}, fakeHouse{holdings: domain.Holdings{"USDT": d("41000")}, contracts: &account},
		fl, rec, event.NewFactory("market-maker", "test"), slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	p.now = func() time.Time { return now }
	ctx := context.Background()
	p.refresh(ctx)
	books := func(seq int64) map[string]string {
		t.Helper()
		p.OnSnapshot(&marketv1.DepthSnapshot{
			Symbol: "BTC-USDT", Sequence: seq, Reference: true, Bids: levels("49999.99", "5"),
			Asks: levels("50000.01", "5"),
		})
		p.OnSnapshot(&marketv1.DepthSnapshot{
			Symbol: "ETH-USDT", Sequence: seq, Reference: true, Bids: levels("2499.99", "50"),
			Asks: levels("2500.01", "50"),
		})
		if err := p.publish(ctx, p.round()); err != nil {
			t.Fatal(err)
		}
		_, out := rec.take(t)
		rooms := map[string]string{}
		for _, b := range out {
			rooms[b.GetSymbol()] = b.GetBuyRoom()
		}
		return rooms
	}
	// Two books: 20,000 each, 0.4 BTC and 8 ETH.
	if rooms := books(1); rooms["BTC-USDT"] != "0.4" || rooms["ETH-USDT"] != "8" {
		t.Fatalf("shared by two: %v", rooms)
	}
	// ETH-USDT no longer offered (an empty book once): BTC-USDT spends
	// all 40,000.
	fl.mu.Lock()
	fl.deny = []string{"ETH-USDT"}
	fl.mu.Unlock()
	now = now.Add(3 * time.Second)
	if rooms := books(2); rooms["BTC-USDT"] != "0.8" || rooms["ETH-USDT"] != "" {
		t.Fatalf("one book left: %v", rooms)
	}
}

// blindSpecs cannot read the backed assets until told.
type blindSpecs struct {
	specList
	read *atomic.Bool
}

func (s blindSpecs) Backed(ctx context.Context) ([]string, error) {
	if !s.read.Load() {
		return nil, errors.New("instrument-service unavailable")
	}
	return s.specList.Backed(ctx)
}

// Until the backed assets are read HOUSE sells only what it holds, of
// any asset; then it may sell an internal asset short (ADR-0013).
func TestHouseSellsShortOnlyOnceItKnowsTheBackedAssets(t *testing.T) {
	now := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	sol := domain.Spec{Symbol: "SOL-USDT", Base: "SOL", Quote: "USDT", TickSize: d("0.01"), LotSize: d("0.01")}
	read := &atomic.Bool{}
	account := domain.ContractAccount{Positions: map[string]decimal.Decimal{}}
	rec := &records{}
	cfg := DefaultConfig()
	cfg.HouseUser = "house"
	p := New(cfg, blindSpecs{specList{sol}, read}, fakeHouse{holdings: domain.Holdings{"USDT": d("500000")}, contracts: &account},
		&onFlags{}, rec, event.NewFactory("market-maker", "test"), slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	p.now = func() time.Time { return now }
	ctx := context.Background()
	p.refresh(ctx)
	p.OnSnapshot(&marketv1.DepthSnapshot{Symbol: "SOL-USDT", Sequence: 1, Reference: true, Bids: levels("150", "10"), Asks: levels("150.01", "10")})
	_ = p.publish(ctx, p.round())
	if _, books := rec.take(t); len(books) != 1 || books[0].GetSellRoom() != "0" {
		t.Fatalf("not read: %v", books)
	}
	read.Store(true)
	now = now.Add(time.Minute) // the specs are due again, and so is the heartbeat
	p.refresh(ctx)
	_ = p.publish(ctx, p.round())
	if _, books := rec.take(t); len(books) != 1 || books[0].GetSellRoom() == "0" {
		t.Fatalf("read: %v", books)
	}
}

// switchSpecs lists what the test sets, or fails while down, counting the
// reads.
type switchSpecs struct {
	mu    sync.Mutex
	list  []domain.Spec
	down  bool
	reads int
}

func (s *switchSpecs) Specs(context.Context) ([]domain.Spec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if s.down {
		return nil, errors.New("instrument-service unavailable")
	}
	return slices.Clone(s.list), nil
}

func (s *switchSpecs) Backed(context.Context) ([]string, error) {
	return []string{"USDT", "BTC", "ETH"}, nil
}

// A pair quoted in BTC is valued through BTC-USDT: halted, that pair still
// prices BTC from its reference market, so ETH-BTC keeps HOUSE (review of
// 6fa3b68); a halted pair the specs list is priced but not quoted.
func TestACrossPairKeepsItsPriceWhileItsQuotesPairHalts(t *testing.T) {
	now := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	ethUSDT := domain.Spec{Symbol: "ETH-USDT", Base: "ETH", Quote: "USDT", TickSize: d("0.01"), LotSize: d("0.001")}
	ethBTC := domain.Spec{Symbol: "ETH-BTC", Base: "ETH", Quote: "BTC", TickSize: d("0.00001"), LotSize: d("0.001")}
	specs := &switchSpecs{list: []domain.Spec{btcSpec, ethUSDT, ethBTC}}
	account := domain.ContractAccount{Positions: map[string]decimal.Decimal{}}
	rec := &records{}
	cfg := DefaultConfig()
	cfg.HouseUser = "house"
	p := New(cfg, specs, fakeHouse{holdings: domain.Holdings{"USDT": d("500000"), "BTC": d("10"), "ETH": d("100")}, contracts: &account},
		&onFlags{}, rec, event.NewFactory("market-maker", "test"), slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	p.now = func() time.Time { return now }
	ctx := context.Background()
	offered := func(seq int64) map[string]bool {
		t.Helper()
		p.OnSnapshot(&marketv1.DepthSnapshot{Symbol: "BTC-USDT", Sequence: seq, Reference: true, Bids: levels("50000", "5"), Asks: levels("50001", "5")})
		p.OnSnapshot(&marketv1.DepthSnapshot{Symbol: "ETH-USDT", Sequence: seq, Reference: true, Bids: levels("2500", "50"), Asks: levels("2500.01", "50")})
		p.OnSnapshot(&marketv1.DepthSnapshot{Symbol: "ETH-BTC", Sequence: seq, Reference: true, Bids: levels("0.05", "50"), Asks: levels("0.05001", "50")})
		if err := p.publish(ctx, p.round()); err != nil {
			t.Fatal(err)
		}
		_, books := rec.take(t)
		out := map[string]bool{}
		for _, b := range books {
			out[b.GetSymbol()] = len(b.GetBids())+len(b.GetAsks()) > 0
		}
		return out
	}
	p.refresh(ctx)
	if got := offered(1); !got["BTC-USDT"] || !got["ETH-USDT"] || !got["ETH-BTC"] {
		t.Fatalf("all three offered: %v", got)
	}
	p.OnStatus("BTC-USDT", "HALT")
	now = now.Add(p.cfg.Heartbeat)
	if got := offered(2); got["BTC-USDT"] || !got["ETH-BTC"] {
		t.Fatalf("BTC-USDT halted, ETH-BTC still offered: %v", got)
	}
	// The specs read again list BTC-USDT halted: still a price, no quote.
	specs.mu.Lock()
	specs.list[0].Halted = true
	specs.mu.Unlock()
	now = now.Add(specsEvery)
	p.refresh(ctx)
	if got := offered(3); got["BTC-USDT"] || !got["ETH-BTC"] {
		t.Fatalf("after the specs are read again: %v", got)
	}
}

// While instrument-service is away the specs are read again every
// specsRetry, not every round, and what was read last stays.
func TestAFailedReadOfTheSpecsIsTriedAgainLater(t *testing.T) {
	now := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	specs := &switchSpecs{list: []domain.Spec{btcSpec}}
	account := domain.ContractAccount{Positions: map[string]decimal.Decimal{}}
	cfg := DefaultConfig()
	cfg.HouseUser = "house"
	p := New(cfg, specs, fakeHouse{holdings: domain.Holdings{"USDT": d("500000")}, contracts: &account},
		&onFlags{}, &records{}, event.NewFactory("market-maker", "test"), slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	p.now = func() time.Time { return now }
	ctx := context.Background()
	p.refresh(ctx)
	specs.mu.Lock()
	specs.down = true
	specs.mu.Unlock()
	p.OnStatus("ETH-USDT", "TRADING") // a pair starts trading: read again at once
	for range 8 {                     // two seconds of rounds
		p.refresh(ctx)
		now = now.Add(p.cfg.Interval)
	}
	if specs.reads != 2 || len(p.list) != 1 {
		t.Fatalf("%d reads, list %v", specs.reads, p.list)
	}
	now = now.Add(specsRetry)
	p.refresh(ctx)
	if specs.reads != 3 {
		t.Fatalf("tried again after specsRetry: %d reads", specs.reads)
	}
}

// A coin-margined contract (coin-margined design 2026-10-06 §2.3): HOUSE
// quotes it in contracts against its BTC account, the equity valued at
// BTC-USDT's mid; its levels are worth at most 20,000 USD each (200
// contracts of 100 USD).
func TestHouseQuotesACoinMarginedContractInContracts(t *testing.T) {
	now := time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC)
	coin := domain.Spec{
		Symbol: "BTC-USD-PERP", Base: "BTC", Quote: "USD", TickSize: d("0.1"), LotSize: d("1"), Contract: true, Settle: "BTC",
		ContractSize: d("100"),
	}
	// 0.5 BTC of equity at 50,000 is 25,000, ten times 250,000; 300
	// contracts short are worth 30,000: 220,000 USD (2,200 contracts) of
	// room. The cap is 100,000 USD either way: 1,000 contracts.
	account := domain.ContractAccount{
		Positions: map[string]decimal.Decimal{"BTC-USD-PERP": d("-300")},
		Exposure:  map[string]decimal.Decimal{"BTC": d("30000")}, Equity: map[string]decimal.Decimal{"BTC": d("0.5"), "USDT": d("0")},
	}
	rec := &records{}
	cfg := DefaultConfig()
	cfg.HouseUser = "house"
	p := New(cfg, specList{btcSpec, coin}, fakeHouse{holdings: domain.Holdings{"USDT": d("500000")}, contracts: &account},
		&onFlags{}, rec, event.NewFactory("market-maker", "test"), slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	p.now = func() time.Time { return now }
	ctx := context.Background()
	p.refresh(ctx)
	if got := p.settleAssets(); len(got) != 2 || got[1] != "BTC" {
		t.Fatalf("the accounts read: %v", got)
	}
	p.OnSnapshot(&marketv1.DepthSnapshot{Symbol: "BTC-USDT", Sequence: 1, Reference: true, Bids: levels("49999.99", "1"), Asks: levels("50000.01", "1")})
	p.OnSnapshot(&marketv1.DepthSnapshot{Symbol: "BTC-USD-PERP", Sequence: 1, Reference: true, Bids: levels("49999.9", "4500"), Asks: levels("50000.1", "4500")})
	_ = p.publish(ctx, p.round())
	_, books := rec.take(t)
	var book *orderv1.ReferenceBookUpdate
	for _, b := range books {
		if b.GetSymbol() == "BTC-USD-PERP" {
			book = b
		}
	}
	if book == nil || book.GetBuyRoom() != "1300" || book.GetSellRoom() != "700" || len(book.GetBids()) != 1 || book.GetBids()[0].GetQuantity() != "200" {
		t.Fatalf("the coin-margined book %v", book)
	}
	// The BTC account shrinks to 0.06 (3,000 USD, 30,000 at 10x): the short
	// uses all of it. HOUSE only buys back its 300 contracts and sells none,
	// so a user's buy finds no room (TestHouseRoomCapsACoinMarginedContract).
	account.Equity["BTC"] = d("0.06")
	now = now.Add(2 * time.Second)
	p.refresh(ctx)
	_ = p.publish(ctx, p.round())
	_, books = rec.take(t)
	book = nil
	for _, b := range books {
		if b.GetSymbol() == "BTC-USD-PERP" {
			book = b
		}
	}
	if book == nil || book.GetBuyRoom() != "300" || book.GetSellRoom() != "0" {
		t.Fatalf("the coin-margined book without room %v", book)
	}
}
