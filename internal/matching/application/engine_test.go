package application

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	orderv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/order/v1"
	tradev1 "github.com/lidp280504357/exchange/api/gen/go/exchange/trade/v1"
	"github.com/lidp280504357/exchange/internal/matching/ports"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
)

type memStore struct {
	wal       []ports.WALEntry
	snapshots map[int32]ports.Snapshot
	outbox    []ports.Output
	failSave  int
}

func (s *memStore) Snapshots(context.Context) ([]ports.Snapshot, error) {
	var out []ports.Snapshot
	for _, snap := range s.snapshots {
		out = append(out, snap)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Partition < out[j].Partition })
	return out, nil
}

func (s *memStore) WAL(_ context.Context, after map[int32]int64) ([]ports.WALEntry, error) {
	var out []ports.WALEntry
	for _, w := range s.wal {
		if seq, ok := after[w.Partition]; !ok || w.Seq > seq {
			out = append(out, w)
		}
	}
	return out, nil
}

func (s *memStore) Save(_ context.Context, wal []ports.WALEntry, events []ports.Output, snaps []ports.Snapshot) error {
	if s.failSave > 0 {
		s.failSave--
		return errors.New("database unavailable")
	}
	s.wal = append(s.wal, wal...)
	s.outbox = append(s.outbox, events...)
	for _, snap := range snaps {
		s.snapshots[snap.Partition] = snap
	}
	return nil
}

func (s *memStore) PurgeWAL(context.Context, time.Time) (int64, error) { return 0, nil }

var factory = event.NewFactory("spot-trading-service", "test")

func place(t *testing.T, id, user string, side orderv1.Side, price, qty string) *eventv1.Envelope {
	t.Helper()
	env, err := factory.New(context.Background(), &orderv1.PlaceOrder{Order: &orderv1.Order{
		OrderId: id, UserId: user, Symbol: "BTC-USDT", Side: side, Type: orderv1.OrderType_ORDER_TYPE_LIMIT,
		TimeInForce: orderv1.TimeInForce_TIME_IN_FORCE_GTC, Price: price, Quantity: qty,
		MakerFeeRate: "0.001", TakerFeeRate: "0.002", BaseDecimals: 8, QuoteDecimals: 6, LotSize: "0.0001",
		BaseAsset: "BTC", QuoteAsset: "USDT",
	}}, "symbol", "BTC-USDT")
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func deliveries(offset int64, envs ...*eventv1.Envelope) []kafka.Delivery {
	out := make([]kafka.Delivery, len(envs))
	for i, env := range envs {
		out[i] = kafka.Delivery{Topic: event.TopicOrderCommands, Partition: 0, Offset: offset + int64(i), Envelope: env}
	}
	return out
}

func newEngine(t *testing.T, store *memStore) *Engine {
	t.Helper()
	e := New(store, event.NewFactory("matching-engine", "test"), slog.New(slog.DiscardHandler), prometheus.NewRegistry(), 3)
	if err := e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	return e
}

func kinds(outs []ports.Output) []string {
	var s []string
	for _, o := range outs {
		s = append(s, o.Topic+" "+string(proto.MessageName(mustPayload(o.Envelope)).Name()))
	}
	return s
}

func mustPayload(env *eventv1.Envelope) proto.Message {
	m, err := env.GetPayload().UnmarshalNew()
	if err != nil {
		panic(err)
	}
	return m
}

func TestBatchesAreLoggedPublishedAndSnapshotted(t *testing.T) {
	store := &memStore{snapshots: map[int32]ports.Snapshot{}}
	e := newEngine(t, store)
	ctx := context.Background()
	batch := deliveries(10,
		place(t, "s1", "alice", orderv1.Side_SIDE_SELL, "60000", "0.1"),
		place(t, "b1", "bob", orderv1.Side_SIDE_BUY, "60000", "0.1"))
	if err := e.Handle(ctx, batch); err != nil {
		t.Fatal(err)
	}
	want := []string{"order.events OrderOpened", "trade.events TradeExecuted", "order.events OrderFilled", "order.events OrderFilled"}
	if got := kinds(store.outbox); !slices.Equal(got, want) {
		t.Fatalf("outbox %v, want %v", got, want)
	}
	trade := mustPayload(store.outbox[1].Envelope).(*tradev1.TradeExecuted)
	if trade.GetPrice() != "60000" || trade.GetQuoteQuantity() != "6000" || trade.GetBuyerOrderId() != "b1" ||
		trade.GetSellerFee() != "6" || trade.GetBuyerFee() != "0.0002" || trade.GetBuyerLimitPrice() != "60000" || trade.GetSequence() != 2 {
		t.Fatalf("trade: %v", trade)
	}
	if len(store.wal) != 2 || store.wal[0].Offset != 10 || len(store.snapshots) != 0 {
		t.Fatalf("wal %d entries, %d snapshots", len(store.wal), len(store.snapshots))
	}
	// A redelivered batch changes nothing.
	if err := e.Handle(ctx, batch); err != nil || len(store.wal) != 2 || len(store.outbox) != 4 {
		t.Fatalf("redelivery: %v, wal %d, outbox %d", err, len(store.wal), len(store.outbox))
	}
	// The third command of the partition takes a snapshot.
	if err := e.Handle(ctx, deliveries(12, place(t, "s2", "alice", orderv1.Side_SIDE_SELL, "60100", "0.2"))); err != nil {
		t.Fatal(err)
	}
	if snap, ok := store.snapshots[0]; !ok || snap.Offset != 12 || len(snap.Books) != 1 || len(snap.Books[0].Orders) != 1 {
		t.Fatalf("snapshot: %+v", store.snapshots)
	}
}

func TestRecoveryRebuildsTheBooksWithoutPublishingAgain(t *testing.T) {
	store := &memStore{snapshots: map[int32]ports.Snapshot{}}
	ctx := context.Background()
	first := newEngine(t, store)
	for i, env := range []*eventv1.Envelope{
		place(t, "s1", "alice", orderv1.Side_SIDE_SELL, "60000", "0.1"),
		place(t, "s2", "alice", orderv1.Side_SIDE_SELL, "60010", "0.1"),
		place(t, "s3", "alice", orderv1.Side_SIDE_SELL, "60020", "0.1"), // snapshot here
		place(t, "s4", "alice", orderv1.Side_SIDE_SELL, "60030", "0.1"), // WAL only
	} {
		if err := first.Handle(ctx, deliveries(int64(i), env)); err != nil {
			t.Fatal(err)
		}
	}
	published := len(store.outbox)
	second := newEngine(t, store) // a restart
	if len(store.outbox) != published {
		t.Fatal("recovery published events again")
	}
	// The rebuilt book holds all four asks; a buy for 0.4 takes them all.
	if err := second.Handle(ctx, deliveries(4, place(t, "b1", "bob", orderv1.Side_SIDE_BUY, "60030", "0.4"))); err != nil {
		t.Fatal(err)
	}
	trades := 0
	for _, o := range store.outbox[published:] {
		if o.Topic == SpotTopics.Trades {
			trades++
		}
	}
	if trades != 4 {
		t.Fatalf("%d trades after recovery, want 4", trades)
	}
}

func TestAFailedSaveRebuildsBeforeTheRetry(t *testing.T) {
	store := &memStore{snapshots: map[int32]ports.Snapshot{}, failSave: 1}
	e := newEngine(t, store)
	ctx := context.Background()
	batch := deliveries(0,
		place(t, "s1", "alice", orderv1.Side_SIDE_SELL, "60000", "0.1"),
		place(t, "b1", "bob", orderv1.Side_SIDE_BUY, "60000", "0.1"))
	if err := e.Handle(ctx, batch); err == nil {
		t.Fatal("the failed save must surface for a retry")
	}
	if err := e.Handle(ctx, batch); err != nil {
		t.Fatal(err)
	}
	// Without the rebuild, the retry would have matched against a book
	// that already had the first attempt's changes.
	if got := kinds(store.outbox); len(got) != 4 || len(store.wal) != 2 {
		t.Fatalf("outbox %v, wal %d", got, len(store.wal))
	}
}

func TestInvalidCommandsAreSkipped(t *testing.T) {
	store := &memStore{snapshots: map[int32]ports.Snapshot{}}
	e := newEngine(t, store)
	odd, err := factory.New(context.Background(), wrapperspb.String("not a command"), "symbol", "BTC-USDT")
	if err != nil {
		t.Fatal(err)
	}
	bad := place(t, "x1", "alice", orderv1.Side_SIDE_SELL, "not a price", "0.1")
	good := place(t, "s1", "alice", orderv1.Side_SIDE_SELL, "60000", "0.1")
	if err := e.Handle(context.Background(), deliveries(0, odd, bad, good)); err != nil {
		t.Fatal(err)
	}
	if len(store.wal) != 1 || store.wal[0].Offset != 2 || len(store.outbox) != 1 {
		t.Fatalf("wal %+v, outbox %d", store.wal, len(store.outbox))
	}
}

func TestCommandsFromBeforeTheEngineTakeAssetsFromTheSymbol(t *testing.T) {
	store := &memStore{snapshots: map[int32]ports.Snapshot{}}
	e := newEngine(t, store)
	var envs []*eventv1.Envelope
	for _, o := range []struct {
		id, user string
		side     orderv1.Side
	}{{"s1", "alice", orderv1.Side_SIDE_SELL}, {"b1", "bob", orderv1.Side_SIDE_BUY}} {
		// No assets and no lot size, as the order service sent them in task 2.
		env, err := factory.New(context.Background(), &orderv1.PlaceOrder{Order: &orderv1.Order{
			OrderId: o.id, UserId: o.user, Symbol: "BTC-USDT", Side: o.side, Type: orderv1.OrderType_ORDER_TYPE_LIMIT,
			TimeInForce: orderv1.TimeInForce_TIME_IN_FORCE_GTC, Price: "60000", Quantity: "0.1",
			MakerFeeRate: "0.001", TakerFeeRate: "0.002", BaseDecimals: 8, QuoteDecimals: 6,
		}}, "symbol", "BTC-USDT")
		if err != nil {
			t.Fatal(err)
		}
		envs = append(envs, env)
	}
	if err := e.Handle(context.Background(), deliveries(0, envs...)); err != nil {
		t.Fatal(err)
	}
	trade := mustPayload(store.outbox[1].Envelope).(*tradev1.TradeExecuted)
	if trade.GetBaseAsset() != "BTC" || trade.GetQuoteAsset() != "USDT" || trade.GetQuantity() != "0.1" {
		t.Fatalf("trade: %v", trade)
	}
}

type fakePublisher struct{ recs []kafka.Record }

func (p *fakePublisher) Publish(_ context.Context, recs ...kafka.Record) error {
	p.recs = append(p.recs, recs...)
	return nil
}

func TestDepthsOfChangedBooksArePublished(t *testing.T) {
	store := &memStore{snapshots: map[int32]ports.Snapshot{}}
	e := newEngine(t, store)
	e.Depths(DepthLevels, false) // the recovery marked everything changed
	ctx := context.Background()
	if err := e.Handle(ctx, deliveries(0,
		place(t, "s1", "alice", orderv1.Side_SIDE_SELL, "60100", "0.1"),
		place(t, "s2", "alice", orderv1.Side_SIDE_SELL, "60100", "0.2"),
		place(t, "b1", "bob", orderv1.Side_SIDE_BUY, "60000", "0.1"))); err != nil {
		t.Fatal(err)
	}
	depths := e.Depths(DepthLevels, false)
	if len(depths) != 1 || depths[0].Symbol != "BTC-USDT" || len(depths[0].Asks) != 1 || depths[0].Asks[0].Quantity.String() != "0.3" {
		t.Fatalf("depths: %+v", depths)
	}
	if again := e.Depths(DepthLevels, false); len(again) != 0 {
		t.Fatalf("an unchanged book was exported again: %+v", again)
	}
	if all := e.Depths(DepthLevels, true); len(all) != 1 {
		t.Fatalf("a refresh exports every book: %+v", all)
	}

	pub := &fakePublisher{}
	x := NewDepthExporter(e, pub, event.NewFactory("matching-engine", "test"), prometheus.NewRegistry())
	if err := x.Export(ctx, depths); err != nil {
		t.Fatal(err)
	}
	if len(pub.recs) != 1 || pub.recs[0].Topic != event.TopicMarketDepthInternal || pub.recs[0].Key != "BTC-USDT" {
		t.Fatalf("records: %+v", pub.recs)
	}
	var env eventv1.Envelope
	if err := proto.Unmarshal(pub.recs[0].Envelope, &env); err != nil {
		t.Fatal(err)
	}
	var snap marketv1.DepthSnapshot
	if err := env.GetPayload().UnmarshalTo(&snap); err != nil {
		t.Fatal(err)
	}
	if snap.GetSequence() != depths[0].Seq || len(snap.GetBids()) != 1 || snap.GetBids()[0].GetPrice() != "60000" || snap.GetAsks()[0].GetQuantity() != "0.3" {
		t.Fatalf("snapshot: %v", &snap)
	}
}

// The contracts' shard is the same engine publishing to its own topics.
func TestTheDerivativesShardPublishesToItsTopics(t *testing.T) {
	store := &memStore{snapshots: map[int32]ports.Snapshot{}}
	e := newEngine(t, store)
	e.Topics = DerivativesTopics
	batch := deliveries(0,
		place(t, "s1", "alice", orderv1.Side_SIDE_SELL, "60000", "0.1"),
		place(t, "b1", "bob", orderv1.Side_SIDE_BUY, "60000", "0.1"))
	if err := e.Handle(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	topics := map[string]int{}
	for _, o := range store.outbox {
		topics[o.Topic]++
	}
	if topics[event.TopicDerivOrder] != 3 || topics[event.TopicDerivTrade] != 1 || len(topics) != 2 {
		t.Fatalf("topics %v", topics)
	}
}

// references builds a reference book delivery published at when: HOUSE
// sells 0.5 at 60010 and buys 0.5 at 59990, with room for 1 either way.
func reference(t *testing.T, when time.Time, askPrice string) *eventv1.Envelope {
	t.Helper()
	env, err := event.NewFactory("market-maker", "test").New(context.Background(), &orderv1.ReferenceBookUpdate{
		Symbol: "BTC-USDT", HouseUserId: "house", BuyRoom: "1", SellRoom: "1",
		Bids: []*orderv1.ReferenceLevel{{Price: "59990", Quantity: "0.5"}},
		Asks: []*orderv1.ReferenceLevel{{Price: askPrice, Quantity: "0.5"}},
	}, "symbol", "BTC-USDT", event.WithOccurredAt(when))
	if err != nil {
		t.Fatal(err)
	}
	return env
}

// placeAt is place issued at when, trading only with HOUSE.
func placeAt(t *testing.T, when time.Time, id, user string, side orderv1.Side, price, qty string) *eventv1.Envelope {
	t.Helper()
	cmd := mustPayload(place(t, id, user, side, price, qty)).(*orderv1.PlaceOrder)
	cmd.HouseOnly = true
	env, err := factory.New(context.Background(), cmd, "symbol", "BTC-USDT", event.WithOccurredAt(when))
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func refDeliveries(offset int64, envs ...*eventv1.Envelope) []kafka.Delivery {
	out := deliveries(offset, envs...)
	for i := range out {
		out[i].Topic = event.TopicOrderReferences
	}
	return out
}

func TestReferenceBooksAreLoggedAndTradeAgainstHouse(t *testing.T) {
	store := &memStore{snapshots: map[int32]ports.Snapshot{}}
	e := newEngine(t, store)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := e.Handle(ctx, refDeliveries(0, reference(t, now, "60010"))); err != nil {
		t.Fatal(err)
	}
	if err := e.Handle(ctx, deliveries(0, placeAt(t, now.Add(time.Second), "b1", "bob", orderv1.Side_SIDE_BUY, "60010", "0.2"))); err != nil {
		t.Fatal(err)
	}
	trade := mustPayload(store.outbox[0].Envelope).(*tradev1.TradeExecuted)
	if trade.GetHouseSide() != orderv1.Side_SIDE_SELL || trade.GetSellerUserId() != "house" || trade.GetSellerOrderId() != "" ||
		trade.GetBuyerOrderId() != "b1" || trade.GetPrice() != "60010" || trade.GetSellerFee() != "0" {
		t.Fatalf("trade: %v", trade)
	}
	// Two sources, one WAL in the order applied.
	if len(store.wal) != 2 || store.wal[0].Source != ports.SourceReferences || store.wal[0].Seq != 0 ||
		store.wal[1].Source != ports.SourceCommands || store.wal[1].Seq != 1 || store.wal[1].Offset != 0 {
		t.Fatalf("wal %+v", store.wal)
	}
	// A redelivered book is skipped by its own offset.
	if err := e.Handle(ctx, refDeliveries(0, reference(t, now, "60010"))); err != nil || len(store.wal) != 2 {
		t.Fatalf("redelivery: %v, wal %d", err, len(store.wal))
	}
	// An order issued more than 5 seconds after the book does not use it.
	late := placeAt(t, now.Add(6*time.Second), "b2", "bob", orderv1.Side_SIDE_BUY, "60010", "0.1")
	if err := e.Handle(ctx, deliveries(1, late)); err != nil {
		t.Fatal(err)
	}
	if last := kinds(store.outbox[len(store.outbox)-1:]); last[0] != "order.events OrderOpened" {
		t.Fatalf("the late order rests: %v", kinds(store.outbox))
	}
	// The next book at 60005 reaches it: it fills at its limit against HOUSE.
	if err := e.Handle(ctx, refDeliveries(1, reference(t, now.Add(7*time.Second), "60005"))); err != nil {
		t.Fatal(err)
	}
	trade = mustPayload(store.outbox[len(store.outbox)-2].Envelope).(*tradev1.TradeExecuted)
	if trade.GetBuyerOrderId() != "b2" || trade.GetPrice() != "60010" || !trade.GetBuyerIsMaker() || trade.GetTakerSide() != orderv1.Side_SIDE_SELL {
		t.Fatalf("triggered trade: %v", trade)
	}
}

// A restart replays commands and reference books alike: an engine that
// stops half way ends with the same books and events as one that did not.
func TestRecoveryReplaysReferenceBooks(t *testing.T) {
	now := time.Now().UTC()
	steps := []kafka.Delivery{
		refDeliveries(0, reference(t, now, "60010"))[0],
		deliveries(0, placeAt(t, now, "b1", "bob", orderv1.Side_SIDE_BUY, "60010", "0.3"))[0],
		deliveries(1, placeAt(t, now, "b2", "bob", orderv1.Side_SIDE_BUY, "60000", "0.4"))[0],
		refDeliveries(1, reference(t, now.Add(time.Second), "59999"))[0], // reaches b2
		deliveries(2, placeAt(t, now.Add(time.Second), "s1", "alice", orderv1.Side_SIDE_SELL, "59990", "0.6"))[0],
		refDeliveries(2, reference(t, now.Add(2*time.Second), "60020"))[0],
		deliveries(3, placeAt(t, now.Add(2*time.Second), "b3", "bob", orderv1.Side_SIDE_BUY, "60020", "0.2"))[0],
	}
	run := func(stopAt int) []string {
		store := &memStore{snapshots: map[int32]ports.Snapshot{}}
		e := newEngine(t, store)
		for i, d := range steps {
			if i == stopAt {
				e = newEngine(t, store) // a restart: snapshot (every 3) and WAL
			}
			if err := e.Handle(context.Background(), []kafka.Delivery{d}); err != nil {
				t.Fatal(err)
			}
		}
		var out []string
		for _, o := range store.outbox {
			m := mustPayload(o.Envelope)
			if tr, ok := m.(*tradev1.TradeExecuted); ok {
				out = append(out, tr.GetTradeId()+" "+tr.GetPrice()+" "+tr.GetQuantity())
			}
		}
		depth := e.Depths(DepthLevels, true)
		for _, dp := range depth {
			for _, l := range append(dp.Bids, dp.Asks...) {
				out = append(out, "rest "+l.Price.String()+" "+l.Quantity.String())
			}
		}
		return out
	}
	want := run(-1)
	if len(want) < 4 {
		t.Fatalf("too few trades to compare: %v", want)
	}
	for stop := 1; stop < len(steps); stop++ {
		if got := run(stop); !slices.Equal(got, want) {
			t.Fatalf("restart before step %d:\n got %v\nwant %v", stop, got, want)
		}
	}
}

// An engine that starts catches up on the reference books published while
// none ran before it reads a command: an order of the gap trades with
// HOUSE on the newest book, even when the group hands it over first.
func TestStartCatchesUpOnReferenceBooks(t *testing.T) {
	store := &memStore{snapshots: map[int32]ports.Snapshot{}}
	e := newEngine(t, store)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := e.Handle(ctx, refDeliveries(0, reference(t, now, "60010"))); err != nil {
		t.Fatal(err)
	}
	// The engine stops for 20 seconds; books 1 and 2 come meanwhile.
	gap := refDeliveries(1, reference(t, now.Add(10*time.Second), "60010"), reference(t, now.Add(20*time.Second), "60010"))
	e = newEngine(t, store)
	var asked []map[int32]int64
	read := func(ctx context.Context, from map[int32]int64, handle kafka.BatchHandler) (int, error) {
		asked = append(asked, from)
		var batch []kafka.Delivery
		for _, d := range gap {
			if d.Offset >= from[d.Partition] {
				batch = append(batch, d)
			}
		}
		return len(batch), handle(ctx, batch)
	}
	if err := e.CatchUp(ctx, read); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || len(asked[0]) != 1 || asked[0][0] != 1 {
		t.Fatalf("read from %v", asked)
	}
	// The group hands over an order of the gap first, then the books again.
	order := placeAt(t, now.Add(19*time.Second), "b1", "bob", orderv1.Side_SIDE_BUY, "60010", "0.2")
	if err := e.Handle(ctx, append(deliveries(0, order), gap...)); err != nil {
		t.Fatal(err)
	}
	var trades []*tradev1.TradeExecuted
	for _, o := range store.outbox {
		if tr, ok := mustPayload(o.Envelope).(*tradev1.TradeExecuted); ok {
			trades = append(trades, tr)
		}
	}
	// It takes the book as it comes; without the catch-up it would rest
	// until a book of the gap reached it.
	if len(trades) != 1 || trades[0].GetBuyerOrderId() != "b1" || trades[0].GetSellerUserId() != "house" || trades[0].GetBuyerIsMaker() {
		t.Fatalf("trades %v", trades)
	}
	// Three books and the order in the WAL; the books handed over again were skipped.
	if len(store.wal) != 4 || store.wal[2].Source != ports.SourceReferences || store.wal[2].Offset != 2 || store.wal[3].Source != ports.SourceCommands {
		t.Fatalf("wal %+v", store.wal)
	}
	// Caught up, the next start reads from book 3.
	asked = nil
	if err := newEngine(t, store).CatchUp(ctx, read); err != nil || len(asked) != 1 || asked[0][0] != 3 {
		t.Fatalf("second start: %v, read from %v", err, asked)
	}
}
