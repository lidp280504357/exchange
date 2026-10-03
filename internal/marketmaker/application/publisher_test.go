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

	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	orderv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/order/v1"
	"github.com/lidp280504357/exchange/internal/marketmaker/domain"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/flags"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
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
func (h fakeHouse) Contracts(context.Context) (domain.ContractAccount, error) {
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
		Positions: map[string]decimal.Decimal{"BTC-USDT-PERP": d("-0.5")}, Exposure: d("25000"), Equity: d("1000000"),
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
	rigContracts.Equity = d("2000")
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
		Positions: map[string]decimal.Decimal{"BTC-USDT-PERP": d("-0.5")}, Exposure: d("25000"), Equity: d("2600"),
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
	account.Equity = d("2506")
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
