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
		if o, ok := after[w.Partition]; !ok || w.Offset > o {
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
	if len(pub.recs) != 1 || pub.recs[0].Topic != event.TopicMarketDepth || pub.recs[0].Key != "BTC-USDT" {
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
