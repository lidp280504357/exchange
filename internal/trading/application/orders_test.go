package application

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	orderv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/order/v1"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/trading/domain"
	"github.com/lidp280504357/exchange/internal/trading/ports"
)

type emitted struct {
	topic string
	msg   proto.Message
}

type memStore struct {
	mu     sync.Mutex
	orders map[string]domain.Order
	fills  map[string]domain.Fill // by trade and order
	events []emitted
}

func (s *memStore) Tx(_ context.Context, fn func(ports.Repos) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	orders, events := maps.Clone(s.orders), slices.Clone(s.events)
	if err := fn(memRepos{s}); err != nil {
		s.orders, s.events = orders, events
		return err
	}
	return nil
}

func (s *memStore) Read() ports.Repos { return memRepos{s} }

type memRepos struct{ s *memStore }

func (r memRepos) Orders() ports.OrderRepo { return memOrders(r) }

func (r memRepos) Fills() ports.FillRepo { return memFills(r) }

func (r memRepos) Emit(_ context.Context, topic string, msg proto.Message, _, _ string) error {
	r.s.events = append(r.s.events, emitted{topic, msg})
	return nil
}

type memOrders memRepos

func (r memOrders) LockUser(context.Context, string) error { return nil }

func (r memOrders) Insert(_ context.Context, o domain.Order) error {
	for _, p := range r.s.orders {
		if p.UserID == o.UserID && p.ClientOrderID == o.ClientOrderID {
			return errors.New("duplicate client_order_id")
		}
	}
	r.s.orders[o.ID] = o
	return nil
}

func (r memOrders) Get(_ context.Context, id string) (domain.Order, error) {
	o, ok := r.s.orders[id]
	if !ok {
		return domain.Order{}, domain.ErrOrderNotFound
	}
	return o, nil
}

func (r memOrders) GetForUpdate(ctx context.Context, id string) (domain.Order, error) {
	return r.Get(ctx, id)
}

func (r memOrders) ByClientID(_ context.Context, userID, clientOrderID string) (domain.Order, error) {
	for _, o := range r.s.orders {
		if o.UserID == userID && o.ClientOrderID == clientOrderID {
			return o, nil
		}
	}
	return domain.Order{}, domain.ErrOrderNotFound
}

func (r memOrders) Update(_ context.Context, o domain.Order) error {
	r.s.orders[o.ID] = o
	return nil
}

func (r memOrders) CountActive(_ context.Context, userID, symbol string) (int, int, error) {
	onSymbol, total := 0, 0
	for _, o := range r.s.orders {
		if o.UserID == userID && o.Status.Active() {
			total++
			if o.Symbol == symbol {
				onSymbol++
			}
		}
	}
	return onSymbol, total, nil
}

func (r memOrders) Active(_ context.Context, userID, symbol string) ([]domain.Order, error) {
	var out []domain.Order
	for _, o := range r.s.orders {
		if o.UserID == userID && o.Status.Active() && (symbol == "" || o.Symbol == symbol) {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r memOrders) List(_ context.Context, userID string, f ports.ListFilter) ([]domain.Order, error) {
	var out []domain.Order
	for _, o := range r.s.orders {
		if o.UserID == userID && (f.Symbol == "" || o.Symbol == f.Symbol) &&
			(len(f.Statuses) == 0 || slices.Contains(f.Statuses, o.Status)) && (f.Before == "" || o.ID < f.Before) {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out[:min(len(out), f.Limit)], nil
}

func (r memOrders) PendingFreeze(_ context.Context, cutoff time.Time, limit int) ([]domain.Order, error) {
	var out []domain.Order
	for _, o := range r.s.orders {
		if o.FreezeState == domain.FreezePending && o.CreatedAt.Before(cutoff) {
			out = append(out, o)
		}
	}
	return out[:min(len(out), limit)], nil
}

// fakeLedger answers freezes with err (nil freezes), unfreezes with
// unfreezeErr, and records the calls.
type fakeLedger struct {
	err         error
	unfreezeErr error
	calls       []string
	releases    []string
}

func (l *fakeLedger) Unfreeze(_ context.Context, key, _, asset string, amount decimal.Decimal, _ string) error {
	if l.unfreezeErr != nil {
		return l.unfreezeErr
	}
	l.releases = append(l.releases, key+" "+amount.String()+" "+asset)
	return nil
}

func (l *fakeLedger) Freeze(_ context.Context, key, _, asset string, amount decimal.Decimal, _ string) error {
	l.calls = append(l.calls, key+" "+amount.String()+" "+asset)
	return l.err
}

type fakeInstruments struct{ pair domain.Pair }

func (f fakeInstruments) Pair(_ context.Context, symbol string) (domain.Pair, error) {
	if symbol != f.pair.Symbol {
		return domain.Pair{}, apperr.NotFound("no such pair")
	}
	return f.pair, nil
}

type fakeEligibility struct{ reason string }

func (f fakeEligibility) Check(context.Context, string, string, string) (bool, string, error) {
	return f.reason == "", f.reason, nil
}

type noAnchor struct{}

func (noAnchor) Anchor(context.Context, string) (decimal.Decimal, error) { return decimal.Zero, nil }

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newService() (*Service, *memStore, *fakeLedger, *clock) {
	store := &memStore{orders: map[string]domain.Order{}}
	led := &fakeLedger{}
	c := &clock{t: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	pair := domain.Pair{
		Symbol: "BTC-USDT", Base: "BTC", Quote: "USDT", TickSize: d("0.01"), LotSize: d("0.00001"),
		MinQuantity: d("0.00001"), MaxQuantity: d("100"), MinNotional: d("5"), PriceBand: d("0.1"),
		MakerFeeRate: d("0.001"), TakerFeeRate: d("0.001"), Status: domain.PairTrading,
		BaseDecimals: 8, QuoteDecimals: 6, Tradable: true,
	}
	return &Service{
		Store: store, Ledger: led, Instruments: fakeInstruments{pair}, Eligibility: fakeEligibility{},
		Prices: noAnchor{}, Log: slog.New(slog.DiscardHandler), Now: c.now,
	}, store, led, c
}

func buy(clientID string) domain.Request {
	return domain.Request{
		UserID: "u1", ClientOrderID: clientID, Symbol: "BTC-USDT", Side: domain.SideBuy,
		Type: domain.TypeLimit, Price: d("60000"), Quantity: d("0.001"),
	}
}

func topics(s *memStore) []string {
	var out []string
	for _, e := range s.events {
		out = append(out, e.topic+" "+string(proto.MessageName(e.msg).Name()))
	}
	return out
}

func TestPlaceFundsTheOrderAndHandsItToTheEngine(t *testing.T) {
	svc, store, led, _ := newService()
	o, err := svc.Place(context.Background(), buy("c1"))
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != domain.StatusNew || o.FreezeState != domain.FreezeDone || !o.FrozenAmount.Equal(d("60")) {
		t.Fatalf("order: %+v", o)
	}
	if len(led.calls) != 1 || led.calls[0] != "order:"+o.ID+" 60 USDT" {
		t.Fatalf("freezes: %v", led.calls)
	}
	want := []string{"order.events OrderAccepted", "order.commands PlaceOrder"}
	if got := topics(store); !slices.Equal(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	placed := store.events[1].msg.(*orderv1.PlaceOrder).GetOrder()
	if placed.GetOrderId() != o.ID || placed.GetSide() != orderv1.Side_SIDE_BUY || placed.GetPrice() != "60000" ||
		placed.GetQuoteAmount() != "" || placed.GetTakerFeeRate() != "0.001" || placed.GetQuoteDecimals() != 6 {
		t.Fatalf("command: %v", placed)
	}
}

func TestARefusedFreezeRejectsTheOrder(t *testing.T) {
	svc, store, led, _ := newService()
	led.err = apperr.New(apperr.KindUnprocessable, "LEDGER_INSUFFICIENT_BALANCE", "insufficient balance")
	o, err := svc.Place(context.Background(), buy("c1"))
	if !apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE") || apperr.From(err).Details["order_id"] != o.ID {
		t.Fatalf("got %v", err)
	}
	stored, _ := store.Read().Orders().Get(context.Background(), o.ID)
	if stored.Status != domain.StatusRejected || stored.RejectReason != "LEDGER_INSUFFICIENT_BALANCE" || stored.FreezeState != domain.FreezeNone {
		t.Fatalf("stored: %+v", stored)
	}
	if got := topics(store); !slices.Equal(got, []string{"order.events OrderRejected"}) {
		t.Fatalf("events %v", got)
	}
	// Repeating the request answers with the same rejection.
	if _, err := svc.Place(context.Background(), buy("c1")); !apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE") || len(led.calls) != 1 {
		t.Fatalf("repeat: %v, %d freezes", err, len(led.calls))
	}
}

func TestAnUnreachableLedgerLeavesTheOrderForRecovery(t *testing.T) {
	svc, store, led, c := newService()
	ctx := context.Background()
	led.err = apperr.Unavailable(errors.New("connection refused"))
	o, err := svc.Place(ctx, buy("c1"))
	if err != nil || o.FreezeState != domain.FreezePending || len(store.events) != 0 {
		t.Fatalf("pending: %+v %v %v", o, err, store.events)
	}
	if n, _ := svc.Recover(ctx); n != 0 {
		t.Fatal("recovery leaves orders younger than 10 seconds to the request")
	}
	led.err = nil
	c.t = c.t.Add(11 * time.Second)
	if n, err := svc.Recover(ctx); err != nil || n != 1 {
		t.Fatalf("recover: %d %v", n, err)
	}
	if n, _ := svc.Recover(ctx); n != 0 {
		t.Fatal("a recovered order is recovered once")
	}
	got, _ := svc.Get(ctx, "u1", o.ID)
	if got.FreezeState != domain.FreezeDone || len(led.calls) != 2 || led.calls[0] != led.calls[1] {
		t.Fatalf("recovered: %+v, freezes %v (same key twice)", got, led.calls)
	}
	if got := topics(store); !slices.Equal(got, []string{"order.events OrderAccepted", "order.commands PlaceOrder"}) {
		t.Fatalf("events %v", got)
	}
}

func TestClientOrderIDs(t *testing.T) {
	svc, _, led, _ := newService()
	ctx := context.Background()
	first, err := svc.Place(ctx, buy("c1"))
	if err != nil {
		t.Fatal(err)
	}
	again := buy("c1")
	again.Price = d("60000.00")
	if o, err := svc.Place(ctx, again); err != nil || o.ID != first.ID || len(led.calls) != 1 {
		t.Fatalf("repeat: %v %v, %d freezes", o.ID, err, len(led.calls))
	}
	other := buy("c1")
	other.Quantity = d("0.002")
	if _, err := svc.Place(ctx, other); !apperr.Is(err, apperr.CodeIdempotencyConflict) {
		t.Fatalf("another order under the same client_order_id: %v", err)
	}
	if o, err := svc.Place(ctx, buy("")); err != nil || o.ClientOrderID != o.ID {
		t.Fatalf("no client_order_id: %+v %v", o, err)
	}
}

func TestActiveOrderLimitsAndEligibility(t *testing.T) {
	svc, store, _, c := newService()
	ctx := context.Background()
	for range domain.MaxActivePerSymbol {
		store.orders[uuid.NewString()] = domain.Order{UserID: "u1", Symbol: "BTC-USDT", Status: domain.StatusOpen, CreatedAt: c.t}
	}
	if _, err := svc.Place(ctx, buy("c1")); !apperr.Is(err, "ORDER_TOO_MANY_OPEN") {
		t.Fatalf("got %v", err)
	}
	svc.Eligibility = fakeEligibility{"USER_FROZEN"}
	if _, err := svc.Place(ctx, buy("c2")); !apperr.Is(err, "USER_FROZEN") || apperr.From(err).Kind != apperr.KindForbidden {
		t.Fatalf("got %v", err)
	}
	if _, err := svc.Place(ctx, domain.Request{UserID: "u1", Symbol: "DOGE-USDT"}); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("unknown pair: %v", err)
	}
}

func TestCancel(t *testing.T) {
	svc, store, led, _ := newService()
	ctx := context.Background()
	o, _ := svc.Place(ctx, buy("c1"))
	got, err := svc.Cancel(ctx, "u1", o.ID)
	if err != nil || !got.CancelRequested {
		t.Fatalf("cancel: %+v %v", got, err)
	}
	if _, err := svc.Cancel(ctx, "u1", o.ID); err != nil {
		t.Fatalf("canceling again: %v", err)
	}
	if n := len(topics(store)); n != 3 || topics(store)[2] != "order.commands CancelOrder" {
		t.Fatalf("events %v", topics(store))
	}
	if _, err := svc.Cancel(ctx, "u2", o.ID); !errors.Is(err, domain.ErrOrderNotFound) {
		t.Fatalf("someone else's order: %v", err)
	}
	filled := store.orders[o.ID]
	filled.Status = domain.StatusFilled
	store.orders[o.ID] = filled
	if _, err := svc.Cancel(ctx, "u1", o.ID); !apperr.Is(err, "ORDER_ALREADY_FILLED") {
		t.Fatalf("filled: %v", err)
	}

	// An order canceled before its freeze is recorded gets its cancel
	// right after its PlaceOrder.
	led.err = apperr.Unavailable(errors.New("down"))
	pending, _ := svc.Place(ctx, buy("c2"))
	store.events = nil
	if _, err := svc.Cancel(ctx, "u1", pending.ID); err != nil || len(store.events) != 0 {
		t.Fatalf("pending cancel: %v %v", err, topics(store))
	}
	led.err = nil
	if _, err := svc.fund(ctx, store.orders[pending.ID]); err != nil {
		t.Fatal(err)
	}
	want := []string{"order.events OrderAccepted", "order.commands PlaceOrder", "order.commands CancelOrder"}
	if got := topics(store); !slices.Equal(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
}

func TestCancelAllAndList(t *testing.T) {
	svc, store, _, c := newService()
	ctx := context.Background()
	var ids []string
	for _, id := range []string{"c1", "c2", "c3"} {
		c.t = c.t.Add(time.Second)
		o, err := svc.Place(ctx, buy(id))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, o.ID)
	}
	if _, err := svc.Cancel(ctx, "u1", ids[0]); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.CancelAll(ctx, "u1", "BTC-USDT"); err != nil || n != 2 {
		t.Fatalf("cancel all: %d %v", n, err)
	}
	cancels := 0
	for _, e := range store.events {
		if _, ok := e.msg.(*orderv1.CancelOrder); ok {
			cancels++
		}
	}
	if cancels != 3 {
		t.Fatalf("%d cancel commands, want one per order", cancels)
	}
	page, next, err := svc.List(ctx, "u1", "", "ACTIVE", "", 2)
	if err != nil || len(page) != 2 || next != page[1].ID || page[0].ID != ids[2] {
		t.Fatalf("page 1: %d %q %v", len(page), next, err)
	}
	page, next, err = svc.List(ctx, "u1", "", "ACTIVE", next, 2)
	if err != nil || len(page) != 1 || next != "" || page[0].ID != ids[0] {
		t.Fatalf("page 2: %d %q %v", len(page), next, err)
	}
	if _, _, err := svc.List(ctx, "u1", "", "LIVE", "", 2); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("bad status: %v", err)
	}
}

func TestTheMarketMakerPaysNoFees(t *testing.T) {
	svc, store, _, _ := newService()
	svc.FeeFree = []string{"mm"}
	req := buy("c1")
	req.UserID = "mm"
	if _, err := svc.Place(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Place(context.Background(), buy("c2")); err != nil {
		t.Fatal(err)
	}
	mm := store.events[1].msg.(*orderv1.PlaceOrder).GetOrder()
	user := store.events[3].msg.(*orderv1.PlaceOrder).GetOrder()
	if mm.GetMakerFeeRate() != "0" || mm.GetTakerFeeRate() != "0" || user.GetTakerFeeRate() != "0.001" {
		t.Fatalf("fees: market maker %s/%s, user %s", mm.GetMakerFeeRate(), mm.GetTakerFeeRate(), user.GetTakerFeeRate())
	}
}
