package application

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	marketv1 "github.com/skill/exchange/api/gen/go/exchange/market/v1"
	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/marketdata/ports"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
)

// bookSource serves one snapshot per symbol and streams what the test
// sends on diffs and trades until the context ends.
type bookSource struct {
	mu     sync.Mutex
	snaps  map[string]int64 // lastID of each symbol's snapshot
	diffs  chan bookDiff
	trades chan domain.Trade
	opened chan []ports.Reference
}

type bookDiff struct {
	symbol string
	d      domain.DepthDiff
}

func (s *bookSource) DepthSnapshot(_ context.Context, ref ports.Reference) (int64, []domain.Level, []domain.Level, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snaps[ref.Symbol], []domain.Level{{Price: d("100"), Quantity: d("1")}, {Price: d("99"), Quantity: d("2")}},
		[]domain.Level{{Price: d("101"), Quantity: d("1")}}, nil
}

func (s *bookSource) RecentTrades(context.Context, ports.Reference, int) ([]domain.Trade, error) {
	return nil, nil
}

func (s *bookSource) BookStream(ctx context.Context, refs []ports.Reference, on ports.BookHandlers) error {
	s.opened <- refs
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case x := <-s.diffs:
			on.Depth(x.symbol, x.d)
		case t := <-s.trades:
			on.Trade(t)
		}
	}
}

// depthFlags turn the reference feed on and show the reference book of
// every symbol but the denied ones.
type depthFlags struct {
	mu   sync.Mutex
	deny []string
}

func (f *depthFlags) Enabled(key string, s flags.Subject) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return key != flags.KeyReferenceDepth || !slices.Contains(f.deny, s.Symbol)
}

func (f *depthFlags) set(deny ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deny = deny
}

func newBooksRig(t *testing.T) (*Books, *bookSource, *recorder, *depthFlags) {
	t.Helper()
	src := &bookSource{
		snaps: map[string]int64{"BTC-USDT": 100}, diffs: make(chan bookDiff), trades: make(chan domain.Trade),
		opened: make(chan []ports.Reference, 4),
	}
	lst := newListing([]ports.Pair{{Symbol: "BTC-USDT", Reference: ports.Reference{Symbol: "BTC-USDT", Remote: "BTCUSDT", Multiplier: decimal.NewFromInt(1)}}}, nil)
	fl := &depthFlags{}
	rec := &recorder{}
	b := NewBooks(src, NewReferenceMap(lst, slog.New(slog.DiscardHandler)), fl, rec, event.NewFactory("market-data-service", "test"),
		slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	return b, src, rec, fl
}

// waitFor polls cond for up to 5 seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for range 250 {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestReferenceBooksArePublishedAsSnapshotsThenUpdates(t *testing.T) {
	b, src, rec, _ := newBooksRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = b.Run(ctx) }()
	<-src.opened
	// An update the snapshot (at 100) covers, then one after it.
	src.diffs <- bookDiff{"BTC-USDT", domain.DepthDiff{First: 99, Last: 100}}
	src.diffs <- bookDiff{"BTC-USDT", domain.DepthDiff{First: 101, Last: 101, Asks: []domain.Level{{Price: d("102"), Quantity: d("5")}}}}
	waitFor(t, "the snapshot", func() bool { return b.Shown("BTC-USDT") })

	msgs := b.collect()
	if len(msgs) != 1 {
		t.Fatalf("messages %+v", msgs)
	}
	snap := msgs[0].msg.(*marketv1.DepthSnapshot)
	if !snap.GetReference() || len(snap.GetBids()) != 2 || len(snap.GetAsks()) != 2 || snap.GetAsks()[1].GetPrice() != "102" {
		t.Fatalf("snapshot %v", snap)
	}
	if msgs = b.collect(); len(msgs) != 0 {
		t.Fatalf("nothing changed, yet %d messages", len(msgs))
	}
	// A change goes out as an update that follows the snapshot's sequence.
	src.diffs <- bookDiff{"BTC-USDT", domain.DepthDiff{First: 102, Last: 102, Bids: []domain.Level{{Price: d("100"), Quantity: d("0")}}}}
	src.trades <- domain.Trade{Symbol: "BTC-USDT", ID: "t", Number: 7, Price: d("101"), Quantity: d("0.5"), Quote: d("50.5"), TakerSide: "BUY", At: time.Now()}
	var up *marketv1.DepthUpdate
	var printed *marketv1.TradesPrinted
	waitFor(t, "the update and the trade", func() bool {
		for _, m := range b.collect() {
			switch x := m.msg.(type) {
			case *marketv1.DepthUpdate:
				if len(x.GetBids())+len(x.GetAsks()) > 0 { // not a heartbeat
					up = x
				}
			case *marketv1.TradesPrinted:
				printed = x
			}
		}
		return up != nil && printed != nil
	})
	if up.GetPrevSequence() != up.GetSequence()-1 || up.GetSequence() <= snap.GetSequence() || len(up.GetBids()) != 1 ||
		up.GetBids()[0].GetQuantity() != "0" {
		t.Fatalf("update %v after snapshot %d", up, snap.GetSequence())
	}
	if !printed.GetReference() || len(printed.GetTrades()) != 1 || printed.GetTrades()[0].GetTradeNumber() != 7 {
		t.Fatalf("trades %v", printed)
	}
	if trades, ok := b.Trades("BTC-USDT", 10); !ok || len(trades) != 1 {
		t.Fatalf("recent trades %v %v", trades, ok)
	}
	// A quiet book beats every second, with no levels.
	time.Sleep(bookHeartbeat + 50*time.Millisecond)
	var beat *marketv1.DepthUpdate
	for _, m := range b.collect() {
		if x, ok := m.msg.(*marketv1.DepthUpdate); ok {
			beat = x
		}
	}
	if beat == nil || len(beat.GetBids())+len(beat.GetAsks()) != 0 || beat.GetPrevSequence() != beat.GetSequence()-1 {
		t.Fatalf("heartbeat %v", beat)
	}
	_ = rec
}

func TestTheEnginesBookIsRelayedWhereTheReferenceIsNotShown(t *testing.T) {
	b, src, rec, fl := newBooksRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = b.Run(ctx) }()
	<-src.opened
	src.diffs <- bookDiff{"BTC-USDT", domain.DepthDiff{First: 101, Last: 101}}
	waitFor(t, "the snapshot", func() bool { return b.Shown("BTC-USDT") })
	shown := b.collect()[0].msg.(*marketv1.DepthSnapshot)

	engine := &marketv1.DepthSnapshot{Symbol: "BTC-USDT", Sequence: 42, Bids: []*marketv1.PriceLevel{{Price: "90", Quantity: "1"}}}
	b.RelayDepth(ctx, engine) // shown: the engine's book stays internal
	b.RelayTrades(ctx, []domain.Trade{{Symbol: "BTC-USDT", ID: "p1", Price: d("90"), Quantity: d("1"), Quote: d("90"), TakerSide: "SELL"}})
	if got := rec.take(t); len(got) != 0 {
		t.Fatalf("relayed while the reference is shown: %v", got)
	}

	fl.set("BTC-USDT") // the reference book is no longer shown
	if msgs := b.collect(); len(msgs) != 0 {
		t.Fatalf("messages %+v", msgs)
	}
	b.RelayDepth(ctx, engine)
	b.RelayTrades(ctx, []domain.Trade{{Symbol: "BTC-USDT", ID: "p1", Price: d("90"), Quantity: d("1"), Quote: d("90"), TakerSide: "SELL"}})
	got := rec.take(t)
	if len(got) != 2 {
		t.Fatalf("relayed %v", got)
	}
	relayed := got[0].(*marketv1.DepthSnapshot)
	if relayed.GetReference() || relayed.GetSequence() <= shown.GetSequence() || relayed.GetBids()[0].GetPrice() != "90" {
		t.Fatalf("relayed book %v after %d: its sequence must keep growing", relayed, shown.GetSequence())
	}
	if tp := got[1].(*marketv1.TradesPrinted); tp.GetReference() || tp.GetTrades()[0].GetTakerSide().String() != "SIDE_SELL" {
		t.Fatalf("relayed trades %v", tp)
	}

	fl.set() // shown again: a fresh snapshot with a later sequence
	var again *marketv1.DepthSnapshot
	for _, m := range b.collect() {
		if s, ok := m.msg.(*marketv1.DepthSnapshot); ok {
			again = s
		}
	}
	if again == nil || !again.GetReference() || again.GetSequence() <= relayed.GetSequence() {
		t.Fatalf("snapshot %v after %d", again, relayed.GetSequence())
	}
}

// A listing gets a connection of its own; a delisting or a changed
// reference restarts only the group it was in, the other books going on
// (review ET ②: every group restarting reloaded every snapshot at once).
func TestOnlyTheChangedGroupsRestart(t *testing.T) {
	b, src, _, _ := newBooksRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	one := decimal.NewFromInt(1)
	btc := ports.Reference{Symbol: "BTC-USDT", Remote: "BTCUSDT", Multiplier: one}
	eth := ports.Reference{Symbol: "ETH-USDT", Remote: "ETHUSDT", Multiplier: one}
	opened := func(want ...string) {
		t.Helper()
		var got []string
		select {
		case refs := <-src.opened:
			for _, r := range refs {
				got = append(got, r.Symbol)
			}
		case <-time.After(5 * time.Second):
		}
		if !slices.Equal(got, want) {
			t.Fatalf("opened %v, want %v", got, want)
		}
	}
	nothingOpened := func() {
		t.Helper()
		select {
		case refs := <-src.opened:
			t.Fatalf("opened %v", refs)
		case <-time.After(100 * time.Millisecond):
		}
	}
	regroup := func(running []*runningGroup, refs ...ports.Reference) []*runningGroup {
		m := map[string]ports.Reference{}
		for _, r := range refs {
			m[r.Symbol] = r
		}
		return b.regroup(ctx, running, b.track(m))
	}
	running := regroup(nil, btc)
	opened("BTC-USDT")
	running = regroup(running, btc, eth) // ETH listed: a group of its own
	opened("ETH-USDT")
	nothingOpened()
	running = regroup(running, eth) // BTC delisted: its group goes
	nothingOpened()
	if len(running) != 1 || running[0].group.refs[0].Symbol != "ETH-USDT" {
		t.Fatalf("running %+v", running)
	}
	thousand := eth
	thousand.Multiplier = decimal.NewFromInt(1000)
	running = regroup(running, thousand) // ETH's reference changed: its group restarts
	opened("ETH-USDT")
	if len(running) != 1 || !running[0].group.refs[0].Multiplier.Equal(thousand.Multiplier) {
		t.Fatalf("running %+v", running)
	}
	stopGroups(running)
}

func TestBooksAreSplitIntoConnections(t *testing.T) {
	m := map[string]ports.Reference{}
	for i := range 30 {
		sym := string(rune('A'+i%26)) + string(rune('A'+i/26)) + "X-USDT"
		m[sym] = ports.Reference{Symbol: sym, Remote: sym}
	}
	m["BTC-USDT-PERP"] = ports.Reference{Symbol: "BTC-USDT-PERP", Remote: "BTCUSDT", Market: ports.MarketUSDM}
	m["AAVE-USDT-PERP"] = ports.Reference{Symbol: "AAVE-USDT-PERP", Remote: "AAVEUSDT", Market: ports.MarketUSDM}
	m["BTC-USD-PERP"] = ports.Reference{Symbol: "BTC-USD-PERP", Remote: "BTCUSD_PERP", Market: ports.MarketCoinM, ContractSize: d("100")}
	gs := groups(m, nil)
	if len(gs) != 4 || gs[0].market != ports.MarketSpot || len(gs[0].refs) != bookStreamsPerConn || len(gs[1].refs) != 5 ||
		gs[2].market != ports.MarketUSDM || gs[2].refs[0].Symbol != "AAVE-USDT-PERP" || gs[2].refs[1].Symbol != "BTC-USDT-PERP" ||
		gs[3].market != ports.MarketCoinM || gs[3].refs[0].Symbol != "BTC-USD-PERP" {
		t.Fatalf("groups %+v", gs)
	}
	// BTC's symbols lead their groups: their snapshots load first.
	gs = groups(m, map[string]bool{"BTC": true})
	if gs[2].refs[0].Symbol != "BTC-USDT-PERP" || gs[2].refs[1].Symbol != "AAVE-USDT-PERP" {
		t.Fatalf("BTC first: %+v", gs[2].refs)
	}
}

// A pair without a reference market (ASTRA-USDT) shows the platform's own
// book and trades whatever the flags say: the rig shows the reference
// market for every symbol, yet nothing is served for it from there, and
// the engine's book and trades are relayed as the platform's.
func TestAPairWithoutAReferenceShowsThePlatform(t *testing.T) {
	b, _, rec, _ := newBooksRig(t)
	ctx := context.Background()
	if b.Shown("ASTRA-USDT") {
		t.Fatal("a pair without a reference market shows it")
	}
	if _, ok := b.Depth("ASTRA-USDT", 10); ok {
		t.Fatal("a reference book served")
	}
	if _, ok := b.Trades("ASTRA-USDT", 10); ok {
		t.Fatal("reference trades served")
	}
	b.RelayDepth(ctx, &marketv1.DepthSnapshot{Symbol: "ASTRA-USDT", Bids: []*marketv1.PriceLevel{{Price: "0.5", Quantity: "100"}}})
	b.RelayTrades(ctx, []domain.Trade{{
		Symbol: "ASTRA-USDT", Sequence: 1, ID: "t1", Price: d("0.5"), Quantity: d("10"), Quote: d("5"), TakerSide: "BUY", At: time.Now(),
	}})
	msgs := rec.take(t)
	if len(msgs) != 2 {
		t.Fatalf("relayed %d messages", len(msgs))
	}
	depth, ok := msgs[0].(*marketv1.DepthSnapshot)
	if !ok || depth.GetReference() || depth.GetBids()[0].GetPrice() != "0.5" {
		t.Fatalf("the platform's book: %v", msgs[0])
	}
	trades, ok := msgs[1].(*marketv1.TradesPrinted)
	if !ok || trades.GetReference() || len(trades.GetTrades()) != 1 || trades.GetTrades()[0].GetPrice() != "0.5" {
		t.Fatalf("the platform's trades: %v", msgs[1])
	}
}
