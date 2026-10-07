package application

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	orderv1 "github.com/skill/exchange/api/gen/go/exchange/order/v1"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/trading/domain"
	"github.com/skill/exchange/internal/trading/ports"
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

func (r memOrders) ActiveUsers(_ context.Context, since time.Time) ([]string, error) {
	var out []string
	for _, o := range r.s.orders {
		if o.Status.Active() && !o.CreatedAt.Before(since) && !slices.Contains(out, o.UserID) {
			out = append(out, o.UserID)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (r memOrders) CountOpen(_ context.Context, except []string) (int, error) {
	n := 0
	for _, o := range r.s.orders {
		if o.Status.Active() && o.LiquidationID == "" && !slices.Contains(except, o.UserID) {
			n++
		}
	}
	return n, nil
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
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out[:min(len(out), limit)], nil
}

func (r memOrders) PendingStats(context.Context) (int, time.Time, error) {
	n, oldest := 0, time.Time{}
	for _, o := range r.s.orders {
		if o.FreezeState == domain.FreezePending {
			n++
			if oldest.IsZero() || o.CreatedAt.Before(oldest) {
				oldest = o.CreatedAt
			}
		}
	}
	return n, oldest, nil
}

// fakeLedger answers freezes with err (nil freezes), unfreezes with
// unfreezeErr, and records the calls and the accounts they named; debts
// are the margin accounts' by "type scope asset" (the user's ignored).
type fakeLedger struct {
	err         error
	unfreezeErr error
	calls       []string
	releases    []string
	accounts    []domain.Account
	debts       map[string]string
	debtErr     error // MarginDebt's answer when set
}

func (l *fakeLedger) MarginDebt(_ context.Context, a domain.Account, asset string) (decimal.Decimal, error) {
	if l.debtErr != nil {
		return decimal.Zero, l.debtErr
	}
	if v, ok := l.debts[string(a.Type)+" "+a.Scope+" "+asset]; ok {
		return d(v), nil
	}
	return decimal.Zero, nil
}

func (l *fakeLedger) MarginBorrowers(context.Context) (int, error) { return len(l.debts), nil }

func (l *fakeLedger) Unfreeze(_ context.Context, key string, a domain.Account, asset string, amount decimal.Decimal, _ string) error {
	if l.unfreezeErr != nil {
		return l.unfreezeErr
	}
	l.releases = append(l.releases, key+" "+amount.String()+" "+asset)
	l.accounts = append(l.accounts, a)
	return nil
}

func (l *fakeLedger) Freeze(_ context.Context, key string, a domain.Account, asset string, amount decimal.Decimal, _ string) error {
	l.calls = append(l.calls, key+" "+amount.String()+" "+asset)
	l.accounts = append(l.accounts, a)
	return l.err
}

// fakeMargin answers reservations with err, or borrows borrowed, and
// records the orders it was asked about.
type fakeMargin struct {
	err      error
	borrowed string
	orders   []domain.Order
}

func (m *fakeMargin) ReserveOrder(_ context.Context, o domain.Order) (ports.Reservation, error) {
	m.orders = append(m.orders, o)
	if m.err != nil {
		return ports.Reservation{}, m.err
	}
	if m.borrowed == "" {
		return ports.Reservation{Borrowed: decimal.Zero}, nil
	}
	return ports.Reservation{Borrowed: d(m.borrowed), BorrowID: "borrow-" + o.ID}, nil
}

// switches turns the named flags on for everyone.
type switches map[string]bool

func (f switches) Enabled(key string, _ flags.Subject) bool { return f[key] }

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

func (noAnchor) Anchor(context.Context, string, bool) (decimal.Decimal, error) {
	return decimal.Zero, nil
}

// fixedAnchor anchors every pair at one price.
type fixedAnchor struct{ price decimal.Decimal }

func (f fixedAnchor) Anchor(context.Context, string, bool) (decimal.Decimal, error) {
	return f.price, nil
}

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
		placed.GetQuoteAmount() != "" || placed.GetTakerFeeRate() != "0.001" || placed.GetQuoteDecimals() != 6 ||
		placed.GetAccountType() != "SPOT" || placed.GetSideEffect() != "NONE" {
		t.Fatalf("command: %v", placed)
	}
	if len(led.accounts) != 1 || led.accounts[0] != (domain.Account{UserID: "u1", Type: domain.AccountSpot}) {
		t.Fatalf("frozen on %v, want the user's SPOT account", led.accounts)
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

func marginBuy(clientID string, account domain.AccountType, effect domain.SideEffect) domain.Request {
	r := buy(clientID)
	r.AccountType, r.SideEffect = account, effect
	return r
}

func TestMarginOrdersWaitForTheirSwitches(t *testing.T) {
	svc, store, led, _ := newService()
	ctx := context.Background()
	margin := &fakeMargin{}
	svc.Margin = margin
	// margin.enabled off (and no flags at all): refused before anything is
	// stored; SPOT orders go on as before.
	for _, f := range []ports.Features{nil, switches{}} {
		if f != nil {
			svc.Features = f
		}
		if _, err := svc.Place(ctx, marginBuy("m1", domain.AccountMarginCross, domain.SideEffectNone)); !apperr.Is(err, "MARGIN_DISABLED") ||
			apperr.From(err).Kind != apperr.KindForbidden {
			t.Fatalf("switch off: %v", err)
		}
	}
	svc.Features = switches{flags.KeyMarginEnabled: true}
	_, err := svc.Place(ctx, marginBuy("m2", domain.AccountMarginCross, domain.SideEffectAutoBorrow))
	if !apperr.Is(err, "MARGIN_DISABLED") || apperr.From(err).Details["flag"] != flags.KeyMarginAutoBorrow {
		t.Fatalf("auto borrow off: %v", err)
	}
	if len(store.orders) != 0 || len(store.events) != 0 || len(margin.orders) != 0 || len(led.calls) != 0 {
		t.Fatalf("a refused margin order left %d orders, %d events, %d reservations, %d freezes",
			len(store.orders), len(store.events), len(margin.orders), len(led.calls))
	}
	// Without margin-service a margin order is refused even with the switch on.
	svc.Margin = nil
	if _, err := svc.Place(ctx, marginBuy("m3", domain.AccountMarginCross, domain.SideEffectNone)); !apperr.Is(err, "MARGIN_DISABLED") {
		t.Fatalf("no margin-service: %v", err)
	}
	if _, err := svc.Place(ctx, buy("s1")); err != nil {
		t.Fatalf("a SPOT order: %v", err)
	}
	// A side effect is for margin accounts; unknown values are refused
	// (the switch and the eligibility let the margin one through).
	svc.Margin = margin
	for _, r := range []domain.Request{
		marginBuy("x1", domain.AccountSpot, domain.SideEffectAutoBorrow),
		marginBuy("x2", "FUTURES", domain.SideEffectNone),
		marginBuy("x3", domain.AccountMarginCross, "AUTO_LEND"),
	} {
		if _, err := svc.Place(ctx, r); !apperr.Is(err, apperr.CodeInvalidArgument) {
			t.Fatalf("%s %s: %v", r.AccountType, r.SideEffect, err)
		}
	}
}

func TestMarginOrdersAreReservedThenFrozenOnTheirAccount(t *testing.T) {
	svc, store, led, _ := newService()
	ctx := context.Background()
	margin := &fakeMargin{borrowed: "20"}
	svc.Margin = margin
	svc.Features = switches{flags.KeyMarginEnabled: true, flags.KeyMarginAutoBorrow: true}
	o, err := svc.Place(ctx, marginBuy("m1", domain.AccountMarginIsolated, domain.SideEffectAutoBorrow))
	if err != nil || o.FreezeState != domain.FreezeDone || !o.Borrowed.Equal(d("20")) || o.BorrowID != "borrow-"+o.ID {
		t.Fatalf("place: %+v %v", o, err)
	}
	if len(margin.orders) != 1 || margin.orders[0].ID != o.ID || !margin.orders[0].FrozenAmount.Equal(d("60")) {
		t.Fatalf("reservations: %v", margin.orders)
	}
	want := domain.Account{UserID: "u1", Type: domain.AccountMarginIsolated, Scope: "BTC-USDT"}
	if len(led.accounts) != 1 || led.accounts[0] != want {
		t.Fatalf("frozen on %v, want %v", led.accounts, want)
	}
	placed := store.events[1].msg.(*orderv1.PlaceOrder).GetOrder()
	if placed.GetAccountType() != "MARGIN_ISOLATED" || placed.GetSideEffect() != "AUTO_BORROW" {
		t.Fatalf("command: %v", placed)
	}
	// The same client_order_id on another account is another order.
	if _, err := svc.Place(ctx, marginBuy("m1", domain.AccountMarginCross, domain.SideEffectAutoBorrow)); !apperr.Is(err, apperr.CodeIdempotencyConflict) {
		t.Fatalf("reused client_order_id: %v", err)
	}
	// What the finished order did not use goes back to the same account.
	if err := svc.OnUpdate(ctx, domain.Update{OrderID: o.ID, Seq: 1, Status: domain.StatusCanceled, Filled: decimal.Zero, FilledQuote: decimal.Zero}); err != nil {
		t.Fatal(err)
	}
	if len(led.releases) != 1 || len(led.accounts) != 2 || led.accounts[1] != want {
		t.Fatalf("released %v on %v", led.releases, led.accounts)
	}
}

func TestMarginRefusalsRejectTheOrderWithTheirDetails(t *testing.T) {
	for _, refusal := range []*apperr.Error{
		apperr.New(apperr.KindUnprocessable, "MARGIN_LIMIT", "more than the account may borrow").WithDetail("max_borrowable", "12.5"),
		apperr.New(apperr.KindUnavailable, "MARGIN_PRICE_UNAVAILABLE", "an asset was never priced").WithDetail("asset", "ASTRA"),
		apperr.New(apperr.KindForbidden, "MARGIN_DISABLED", "margin trading is not available"),
	} {
		svc, store, led, _ := newService()
		svc.Margin = &fakeMargin{err: refusal}
		svc.Features = switches{flags.KeyMarginEnabled: true}
		o, err := svc.Place(context.Background(), marginBuy("m1", domain.AccountMarginCross, domain.SideEffectNone))
		e := apperr.From(err)
		if e.Code != refusal.Code || e.Kind != refusal.Kind || e.Details["order_id"] != o.ID || len(e.Details) != len(refusal.Details)+1 {
			t.Fatalf("%s: got %v %v", refusal.Code, e, e.Details)
		}
		for k, v := range refusal.Details {
			if e.Details[k] != v {
				t.Fatalf("%s: detail %s = %v, want %v", refusal.Code, k, e.Details[k], v)
			}
		}
		stored, _ := store.Read().Orders().Get(context.Background(), o.ID)
		if stored.Status != domain.StatusRejected || stored.RejectReason != refusal.Code || len(led.calls) != 0 {
			t.Fatalf("%s: stored %+v, %d freezes", refusal.Code, stored, len(led.calls))
		}
		// The same request again gets the same answer: kind, code and details.
		_, again := svc.Place(context.Background(), marginBuy("m1", domain.AccountMarginCross, domain.SideEffectNone))
		r := apperr.From(again)
		if r.Code != refusal.Code || r.Kind != refusal.Kind || r.Details["order_id"] != o.ID || len(r.Details) != len(refusal.Details)+1 {
			t.Fatalf("%s repeated: %v %v", refusal.Code, r, r.Details)
		}
		for k, v := range refusal.Details {
			if r.Details[k] != v {
				t.Fatalf("%s repeated: detail %s = %v, want %v", refusal.Code, k, r.Details[k], v)
			}
		}
	}
}

// hangingLedger never answers: each call waits for its context.
type hangingLedger struct{ fakeLedger }

func (*hangingLedger) Freeze(ctx context.Context, _ string, _ domain.Account, _ string, _ decimal.Decimal, _ string) error {
	<-ctx.Done()
	return apperr.Unavailable(ctx.Err())
}

func TestAHangingLedgerLeavesTheOrderPendingInTime(t *testing.T) {
	svc, store, _, c := newService()
	svc.Ledger = &hangingLedger{}
	svc.CallTimeout = 20 * time.Millisecond
	ctx := context.Background()
	start := time.Now()
	o, err := svc.Place(ctx, buy("c1"))
	if err != nil || o.FreezeState != domain.FreezePending || time.Since(start) > 2*time.Second {
		t.Fatalf("pending after %v: %+v %v", time.Since(start), o, err)
	}
	// A recovery pass leaves it pending, counts nothing finished, and stops
	// at its deadline.
	c.t = c.t.Add(11 * time.Second)
	if n, err := svc.Recover(ctx); err != nil || n != 0 {
		t.Fatalf("recover: %d %v", n, err)
	}
	done, cancel := context.WithCancel(ctx)
	cancel()
	if n, err := svc.Recover(done); !errors.Is(err, context.Canceled) || n != 0 {
		t.Fatalf("a pass past its deadline: %d %v", n, err)
	}
	if got, _ := store.Read().Orders().Get(ctx, o.ID); got.FreezeState != domain.FreezePending {
		t.Fatalf("stored: %+v", got)
	}
}

func TestARefusedFreezeKeepsTheBorrowOnRecord(t *testing.T) {
	svc, store, led, _ := newService()
	svc.Margin = &fakeMargin{borrowed: "20"}
	svc.Features = switches{flags.KeyMarginEnabled: true, flags.KeyMarginAutoBorrow: true}
	led.err = apperr.New(apperr.KindUnprocessable, "LEDGER_INSUFFICIENT_BALANCE", "insufficient balance")
	o, err := svc.Place(context.Background(), marginBuy("m1", domain.AccountMarginCross, domain.SideEffectAutoBorrow))
	if !apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE") {
		t.Fatalf("place: %v", err)
	}
	stored, _ := store.Read().Orders().Get(context.Background(), o.ID)
	if stored.Status != domain.StatusRejected || !stored.Borrowed.Equal(d("20")) || stored.BorrowID != "borrow-"+o.ID {
		t.Fatalf("stored: %+v", stored)
	}
}

// Every coded answer of a dependency is final, for placing and recovering
// alike; only failures to answer leave the order to recovery.
func TestAnswersRejectAndFailuresWait(t *testing.T) {
	for _, tc := range []struct {
		err   *apperr.Error
		final bool
	}{
		{apperr.NotFound("no such account"), true},
		{apperr.New(apperr.KindUnauthenticated, apperr.CodeUnauthorized, "who"), true},
		{apperr.New(apperr.KindRateLimited, apperr.CodeRateLimited, "slow down"), true},
		{apperr.New(apperr.KindUnavailable, "MARGIN_PRICE_UNAVAILABLE", "never priced"), true},
		{apperr.Unavailable(errors.New("connection refused")), false},
		{apperr.Internal(errors.New("boom")), false},
	} {
		if got := refused(tc.err); got != tc.final {
			t.Errorf("%s (%v): refused %v, want %v", tc.err.Code, tc.err.Kind, got, tc.final)
		}
	}
}

// A refusal met while recovering finishes that order and the batch goes on.
func TestRecoveryGoesOnPastRefusals(t *testing.T) {
	svc, store, led, c := newService()
	ctx := context.Background()
	margin := &fakeMargin{err: apperr.Unavailable(errors.New("connection refused"))}
	svc.Margin = margin
	svc.Features = switches{flags.KeyMarginEnabled: true}
	first, _ := svc.Place(ctx, marginBuy("m1", domain.AccountMarginCross, domain.SideEffectNone))
	c.t = c.t.Add(time.Second)
	led.err = apperr.Unavailable(errors.New("connection refused"))
	second, _ := svc.Place(ctx, buy("s1"))
	if n, oldest, err := svc.Pending(ctx); err != nil || n != 2 || oldest != time.Second {
		t.Fatalf("pending: %d %v %v", n, oldest, err)
	}
	margin.err = apperr.New(apperr.KindUnavailable, "MARGIN_PRICE_UNAVAILABLE", "never priced")
	led.err = nil
	c.t = c.t.Add(11 * time.Second)
	if n, err := svc.Recover(ctx); err != nil || n != 2 {
		t.Fatalf("recover: %d %v", n, err)
	}
	a, _ := store.Read().Orders().Get(ctx, first.ID)
	b, _ := store.Read().Orders().Get(ctx, second.ID)
	if a.Status != domain.StatusRejected || a.RejectReason != "MARGIN_PRICE_UNAVAILABLE" || b.FreezeState != domain.FreezeDone {
		t.Fatalf("after recovery: %s %s, %s", a.Status, a.RejectReason, b.FreezeState)
	}
	if n, oldest, err := svc.Pending(ctx); err != nil || n != 0 || oldest != 0 {
		t.Fatalf("pending after: %d %v %v", n, oldest, err)
	}
}

func TestAnUnreachableMarginServiceLeavesTheOrderForRecovery(t *testing.T) {
	svc, store, led, c := newService()
	ctx := context.Background()
	margin := &fakeMargin{err: apperr.Unavailable(errors.New("connection refused"))}
	svc.Margin = margin
	svc.Features = switches{flags.KeyMarginEnabled: true}
	o, err := svc.Place(ctx, marginBuy("m1", domain.AccountMarginCross, domain.SideEffectNone))
	if err != nil || o.FreezeState != domain.FreezePending || len(led.calls) != 0 || len(store.events) != 0 {
		t.Fatalf("pending: %+v %v, %d freezes", o, err, len(led.calls))
	}
	margin.err = nil
	c.t = c.t.Add(11 * time.Second)
	if n, err := svc.Recover(ctx); err != nil || n != 1 {
		t.Fatalf("recover: %d %v", n, err)
	}
	got, _ := svc.Get(ctx, "u1", o.ID)
	if got.FreezeState != domain.FreezeDone || len(margin.orders) != 2 || margin.orders[1].ID != o.ID || len(led.calls) != 1 {
		t.Fatalf("recovered: %+v, %d reservations (the same order twice), %d freezes", got, len(margin.orders), len(led.calls))
	}
}

// featureEligibility refuses the features named in no with reason, and
// records what it was asked: feature:symbol.
type featureEligibility struct {
	no     map[string]bool
	reason string
	asked  []string
}

func (f *featureEligibility) Check(_ context.Context, _, feature, symbol string) (bool, string, error) {
	f.asked = append(f.asked, feature+":"+symbol)
	if f.no[feature] {
		return false, f.reason, nil
	}
	return true, "", nil
}

// Margin orders ask MARGIN_TRADE (review CO), spot orders SPOT_TRADE.
func TestMarginOrdersNeedTheMarginEligibility(t *testing.T) {
	svc, store, _, _ := newService()
	ctx := context.Background()
	svc.Margin = &fakeMargin{}
	svc.Features = switches{flags.KeyMarginEnabled: true}
	elig := &featureEligibility{no: map[string]bool{FeatureMarginTrade: true}, reason: "USER_RISK_REVIEW"}
	svc.Eligibility = elig
	_, err := svc.Place(ctx, marginBuy("m1", domain.AccountMarginCross, domain.SideEffectNone))
	if e := apperr.From(err); e.Code != "USER_RISK_REVIEW" || e.Kind != apperr.KindForbidden || len(store.orders) != 0 {
		t.Fatalf("a margin order without MARGIN_TRADE: %v, %d orders", err, len(store.orders))
	}
	if _, err := svc.Place(ctx, buy("s1")); err != nil {
		t.Fatalf("a spot order of the same user: %v", err)
	}
	// The cross account has no symbol (as margin-service asks it); spot
	// trading asks with the pair.
	if want := []string{FeatureMarginTrade + ":", FeatureSpotTrade + ":BTC-USDT"}; !slices.Equal(elig.asked, want) {
		t.Fatalf("asked %v, want %v", elig.asked, want)
	}
	// An isolated account asks with its pair.
	elig.asked = nil
	if _, err := svc.Place(ctx, marginBuy("i1", domain.AccountMarginIsolated, domain.SideEffectNone)); !apperr.Is(err, "USER_RISK_REVIEW") || len(store.orders) != 1 {
		t.Fatalf("an isolated order without MARGIN_TRADE: %v, %d orders (the spot one only)", err, len(store.orders))
	}
	if want := []string{FeatureMarginTrade + ":BTC-USDT"}; !slices.Equal(elig.asked, want) {
		t.Fatalf("asked %v, want %v", elig.asked, want)
	}
	// The switch comes first: off, it answers MARGIN_DISABLED without asking.
	elig.asked = nil
	svc.Features = switches{}
	if _, err := svc.Place(ctx, marginBuy("m2", domain.AccountMarginCross, domain.SideEffectNone)); !apperr.Is(err, "MARGIN_DISABLED") || len(elig.asked) != 0 {
		t.Fatalf("switch off: %v, asked %v", err, elig.asked)
	}
}

func liquidation(side domain.Side) Liquidation {
	l := Liquidation{
		LiquidationID: "0199b0a0-0000-7000-8000-000000000001", UserID: "0199b0a0-0000-7000-8000-0000000000aa",
		Account: domain.AccountMarginIsolated, Symbol: "BTC-USDT", Side: side,
	}
	if side == domain.SideSell {
		l.Quantity = d("0.00002") // 1.2 USDT at 60000: under the 5 USDT minimum
	} else {
		l.QuoteAmount = d("1")
	}
	return l
}

func TestLiquidationsCloseTheAccountWithoutAReservation(t *testing.T) {
	svc, store, led, _ := newService()
	ctx := context.Background()
	pair := svc.Instruments.(fakeInstruments).pair
	pair.Reference = "BTCUSDT"
	svc.Instruments = fakeInstruments{pair}
	margin := &fakeMargin{err: apperr.New(apperr.KindConflict, "MARGIN_FROZEN", "the account is being liquidated")}
	svc.Margin = margin
	svc.Prices = fixedAnchor{d("60000")}
	// No switch, no eligibility: the account is frozen; this is its way out.
	svc.Features = switches{}
	svc.Eligibility = fakeEligibility{"USER_FROZEN"}
	o, err := svc.Liquidate(ctx, liquidation(domain.SideSell))
	if err != nil || o.FreezeState != domain.FreezeDone || o.Type != domain.TypeMarket || !o.ProtectionPrice.IsZero() || o.LiquidationID == "" {
		t.Fatalf("liquidation: %+v %v", o, err)
	}
	if len(margin.orders) != 0 {
		t.Fatalf("a liquidation asked margin-service to reserve: %v", margin.orders)
	}
	want := domain.Account{UserID: o.UserID, Type: domain.AccountMarginIsolated, Scope: "BTC-USDT"}
	if len(led.accounts) != 1 || led.accounts[0] != want || led.calls[0] != "order:"+o.ID+" 0.00002 BTC" {
		t.Fatalf("froze %v on %v", led.calls, led.accounts)
	}
	placed := store.events[1].msg.(*orderv1.PlaceOrder).GetOrder()
	if placed.GetAccountType() != "MARGIN_ISOLATED" || placed.GetProtectionPrice() != "" {
		t.Fatalf("command: %v", placed)
	}
	// The same liquidation of the same pair and side is the same order.
	again, err := svc.Liquidate(ctx, liquidation(domain.SideSell))
	if err != nil || again.ID != o.ID || len(led.calls) != 1 {
		t.Fatalf("repeat: %+v %v, %d freezes", again, err, len(led.calls))
	}
	buy, err := svc.Liquidate(ctx, liquidation(domain.SideBuy))
	if err != nil || buy.ID == o.ID || !buy.QuoteAmount.Equal(d("1")) {
		t.Fatalf("a buy of the same liquidation: %+v %v", buy, err)
	}
	// Only margin accounts, by UUIDs.
	spot := liquidation(domain.SideSell)
	spot.Account, spot.LiquidationID = domain.AccountSpot, "0199b0a0-0000-7000-8000-000000000002"
	if _, err := svc.Liquidate(ctx, spot); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("a SPOT liquidation: %v", err)
	}
	bad := liquidation(domain.SideSell)
	bad.LiquidationID = "liq-1"
	if _, err := svc.Liquidate(ctx, bad); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("a liquidation ID that is no UUID: %v", err)
	}
	bad = liquidation(domain.SideSell)
	bad.Attempt = -1
	if _, err := svc.Liquidate(ctx, bad); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("attempt -1: %v", err)
	}
	// Without a reference market (the platform coin's pair) the order stays
	// within half and twice the anchor.
	pair.Reference = ""
	svc.Instruments = fakeInstruments{pair}
	for _, c := range []struct {
		side domain.Side
		want string
	}{{domain.SideSell, "30000"}, {domain.SideBuy, "120000"}} {
		l := liquidation(c.side)
		l.LiquidationID = "0199b0a0-0000-7000-8000-000000000003"
		o, err := svc.Liquidate(ctx, l)
		if err != nil || !o.ProtectionPrice.Equal(d(c.want)) {
			t.Fatalf("a %s liquidation without a reference market: protection %s (%v), want %s", c.side, o.ProtectionPrice, err, c.want)
		}
	}
}

func TestLiquidationAttemptsAreOrdersOfTheirOwn(t *testing.T) {
	svc, _, led, _ := newService()
	ctx := context.Background()
	svc.Margin = &fakeMargin{}
	svc.Prices = fixedAnchor{d("60000")}
	svc.Features = switches{}
	// The first attempt is the order of the key before attempts: given or not.
	first, err := svc.Liquidate(ctx, liquidation(domain.SideSell))
	if err != nil {
		t.Fatal(err)
	}
	one := liquidation(domain.SideSell)
	one.Attempt = 1
	if again, err := svc.Liquidate(ctx, one); err != nil || again.ID != first.ID {
		t.Fatalf("attempt 1: %+v %v", again, err)
	}
	// A rejected attempt stays rejected; the next one is a new order.
	led.err = apperr.New(apperr.KindConflict, "LEDGER_INSUFFICIENT_BALANCE", "not enough")
	second := liquidation(domain.SideSell)
	second.Attempt = 2
	refused, err := svc.Liquidate(ctx, second)
	if !apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE") || refused.Status != domain.StatusRejected || refused.ID == first.ID {
		t.Fatalf("attempt 2 refused: %+v %v", refused, err)
	}
	if _, err := svc.Liquidate(ctx, second); !apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE") {
		t.Fatalf("attempt 2 again: %v", err)
	}
	led.err = nil
	third := liquidation(domain.SideSell)
	third.Attempt = 3
	o, err := svc.Liquidate(ctx, third)
	if err != nil || o.ID == refused.ID || o.ID == first.ID || o.ClientOrderID == refused.ClientOrderID || o.FreezeState != domain.FreezeDone {
		t.Fatalf("attempt 3: %+v %v", o, err)
	}
}

func TestCancelAccountCancelsOnlyThatAccount(t *testing.T) {
	svc, store, _, _ := newService()
	ctx := context.Background()
	svc.Margin = &fakeMargin{}
	svc.Features = switches{flags.KeyMarginEnabled: true}
	user := "0199b0a0-0000-7000-8000-0000000000bb"
	place := func(id string, account domain.AccountType) domain.Order {
		r := marginBuy(id, account, domain.SideEffectNone)
		r.UserID = user
		o, err := svc.Place(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	spot := place("s1", domain.AccountSpot)
	cross := place("c1", domain.AccountMarginCross)
	isolated := place("i1", domain.AccountMarginIsolated)
	if n, err := svc.CancelAccount(ctx, user, domain.AccountMarginCross, ""); err != nil || n != 1 {
		t.Fatalf("cross: %d %v", n, err)
	}
	for _, c := range []struct {
		o    domain.Order
		want bool
	}{{spot, false}, {cross, true}, {isolated, false}} {
		got, _ := store.Read().Orders().Get(ctx, c.o.ID)
		if got.CancelRequested != c.want {
			t.Fatalf("%s %s: cancel requested %v", c.o.ClientOrderID, c.o.AccountType, got.CancelRequested)
		}
	}
	if n, err := svc.CancelAccount(ctx, user, domain.AccountMarginIsolated, "BTC-USDT"); err != nil || n != 1 {
		t.Fatalf("isolated: %d %v", n, err)
	}
	for _, bad := range []struct {
		account domain.AccountType
		symbol  string
	}{{domain.AccountSpot, ""}, {domain.AccountMarginIsolated, ""}, {domain.AccountMarginCross, "BTC-USDT"}} {
		if _, err := svc.CancelAccount(ctx, user, bad.account, bad.symbol); !apperr.Is(err, apperr.CodeInvalidArgument) {
			t.Fatalf("%s %q: %v", bad.account, bad.symbol, err)
		}
	}
}

// lines is the product lines' flags with the named ones closed; from the
// check numbered closing on (counting from 1) spot is closed too, as if an
// operator closed it while an order was on its way in.
type lines struct {
	closed          map[string]bool
	closedAt        time.Time // the closed lines' UpdatedAt
	closing, checks int
	refreshes       int
	refreshErr      error // Refresh's answer when set
}

func (l *lines) Closed(key string) bool {
	l.checks++
	return l.closed[key] || (key == flags.KeyProductSpot && l.closing > 0 && l.checks >= l.closing)
}

func (l *lines) Get(key string) (flags.Flag, bool) {
	closed, ok := l.closed[key]
	return flags.Flag{Key: key, Enabled: !closed, UpdatedAt: l.closedAt}, ok
}

func (l *lines) Refresh(context.Context) error {
	l.refreshes++
	return l.refreshErr
}

// The product switches (design 2026-10-07, §1 #3 and #7): with spot
// trading closed new orders are refused and nothing is stored, on margin
// accounts too but for repayments (AUTO_REPAY bringing an asset the
// account owes); the market-making accounts quote on and cancels go on.
// CancelOpen cancels the open orders of both kinds of account, each
// audited, once the line is closed; the sweep keeps the repayments.
func TestAClosedSpotLineTakesNoOrdersAndItsOrdersAreCanceled(t *testing.T) {
	svc, store, led, c := newService()
	ctx := context.Background()
	svc.Margin = &fakeMargin{}
	svc.Features = switches{flags.KeyMarginEnabled: true}
	products := &lines{closed: map[string]bool{}}
	svc.Products = products
	maker := "0199b0a0-0000-7000-8000-0000000000c3"
	svc.FeeFree = []string{maker}
	users := []string{"0199b0a0-0000-7000-8000-0000000000c1", "0199b0a0-0000-7000-8000-0000000000c2"}
	place := func(user, id string, account domain.AccountType, effect domain.SideEffect, side domain.Side) (domain.Order, error) {
		r := marginBuy(id, account, effect)
		r.UserID, r.Side = user, side
		return svc.Place(ctx, r)
	}
	var spot []domain.Order
	for _, u := range users {
		for _, id := range []string{"a", "b"} {
			o, err := place(u, id, domain.AccountSpot, domain.SideEffectNone, domain.SideBuy)
			if err != nil {
				t.Fatal(err)
			}
			spot = append(spot, o)
		}
	}
	margin, err := place(users[0], "m", domain.AccountMarginCross, domain.SideEffectNone, domain.SideBuy)
	if err != nil {
		t.Fatal(err)
	}
	// One already being canceled is not asked again.
	if _, err := svc.Cancel(ctx, users[1], spot[3].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CancelOpen(ctx, "ops@example.com", "closing spot"); !apperr.Is(err, apperr.CodeConflict) {
		t.Fatalf("cancel-open while the line is open: %v", err)
	}

	products.closed[flags.KeyProductSpot] = true
	stored := len(store.orders)
	for _, c := range []struct {
		id      string
		account domain.AccountType
		effect  domain.SideEffect
	}{
		{"c", domain.AccountSpot, domain.SideEffectNone},
		{"m2", domain.AccountMarginCross, domain.SideEffectNone},
		{"m3", domain.AccountMarginCross, domain.SideEffectAutoRepay}, // owes nothing
	} {
		_, err := place(users[0], c.id, c.account, c.effect, domain.SideBuy)
		if e := apperr.From(err); e.Code != flags.CodeProductClosed || e.Details["product"] != "spot" || len(store.orders) != stored {
			t.Fatalf("%s while closed: %v %v, %d orders", c.id, err, e.Details, len(store.orders))
		}
	}
	quote, err := place(maker, "q", domain.AccountSpot, domain.SideEffectNone, domain.SideBuy)
	if err != nil {
		t.Fatalf("a market maker's order while spot is closed: %v", err)
	}
	// The four spot orders (the one being canceled is open until the engine
	// answers) and the margin one, not the market maker's.
	if closed, open, borrowers, err := svc.SpotLine(ctx); err != nil || !closed || open != 5 || borrowers != 0 {
		t.Fatalf("spot line: closed %v, %d open orders, %d borrowers, %v", closed, open, borrowers, err)
	}
	if _, err := svc.CancelOpen(ctx, "", "closing spot"); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("no actor: %v", err)
	}
	audits := 0
	for _, e := range store.events {
		if e.topic == event.TopicAudit {
			audits++
		}
	}
	canceled, err := svc.CancelOpen(ctx, "ops@example.com", "closing spot")
	if err != nil || len(canceled) != 4 || products.refreshes != 2 { // one each time past the arguments
		t.Fatalf("cancel-open: %+v %v, %d refreshes", canceled, err, products.refreshes)
	}
	for i, o := range append(slices.Clone(spot), margin) {
		got, _ := store.Read().Orders().Get(ctx, o.ID)
		if !got.CancelRequested {
			t.Fatalf("order %d (%s) not canceled", i, o.AccountType)
		}
		if i != 3 && !slices.ContainsFunc(canceled, func(c CanceledOrder) bool {
			return c.OrderID == o.ID && c.UserID == o.UserID && c.Symbol == "BTC-USDT"
		}) {
			t.Fatalf("order %d missing from %+v", i, canceled)
		}
	}
	if got, _ := store.Read().Orders().Get(ctx, quote.ID); got.CancelRequested {
		t.Fatal("the market maker's order was canceled")
	}
	var actions []*auditv1.AdminActionPerformed
	for _, e := range store.events {
		if a, ok := e.msg.(*auditv1.AdminActionPerformed); ok && e.topic == event.TopicAudit {
			actions = append(actions, a)
		}
	}
	if len(actions)-audits != 4 {
		t.Fatalf("%d audit events, want 4 more than %d", len(actions), audits)
	}
	for _, a := range actions[audits:] {
		if a.GetAction() != "admin.orders.canceled" || a.GetActor() != "ops@example.com" || a.GetReason() != "closing spot" ||
			!slices.Contains(users, strings.TrimPrefix(a.GetTarget(), "user:")) || !strings.Contains(a.GetDetails(), `"product":"spot"`) ||
			!strings.Contains(a.GetDetails(), `"account_type":"`) {
			t.Fatalf("audit %+v", a)
		}
	}

	// Repayments go on: a buy bringing the BTC the account owes; a sell
	// bringing USDT, which it does not owe, is refused.
	led.debts = map[string]string{"MARGIN_CROSS  BTC": "0.002"}
	repay, err := place(users[0], "r", domain.AccountMarginCross, domain.SideEffectAutoRepay, domain.SideBuy)
	if err != nil {
		t.Fatalf("a repayment while spot is closed: %v", err)
	}
	if _, err := place(users[0], "r2", domain.AccountMarginCross, domain.SideEffectAutoRepay, domain.SideSell); !apperr.Is(err, flags.CodeProductClosed) {
		t.Fatalf("a sell bringing what the account does not owe: %v", err)
	}
	if n, err := svc.SweepClosed(ctx); err != nil || n != 0 {
		t.Fatalf("sweep: %d %v", n, err)
	}
	// Run again, cancel-open keeps it too (review B155).
	if again, err := svc.CancelOpen(ctx, "ops@example.com", "closing spot again"); err != nil || len(again) != 0 {
		t.Fatalf("cancel-open again: %+v %v", again, err)
	}
	if got, _ := store.Read().Orders().Get(ctx, repay.ID); got.CancelRequested {
		t.Fatal("the repayment was canceled")
	}
	// The borrowers are counted at most every 15 seconds.
	if _, _, borrowers, err := svc.SpotLine(ctx); err != nil || borrowers != 0 {
		t.Fatalf("borrowers within 15 s: %d %v", borrowers, err)
	}
	c.t = c.t.Add(borrowersEvery)
	if _, _, borrowers, err := svc.SpotLine(ctx); err != nil || borrowers != 1 {
		t.Fatalf("borrowers: %d %v", borrowers, err)
	}
	// Cancels go on while the line is closed.
	if n, err := svc.CancelAll(ctx, users[0], ""); err != nil || n != 1 {
		t.Fatalf("cancel all: %d %v", n, err)
	}
}

// While spot is closed the 5-second sweep takes the SPOT orders stored
// from ten seconds before it closed on (an order a stale copy of the flag
// let in), as system:spot-trading-service; older ones are CancelOpen's.
func TestTheSweepTakesWhatSlippedInAsSpotClosed(t *testing.T) {
	svc, store, led, c := newService()
	ctx := context.Background()
	svc.Margin = &fakeMargin{}
	svc.Features = switches{flags.KeyMarginEnabled: true}
	products := &lines{closed: map[string]bool{}}
	svc.Products = products
	old, err := svc.Place(ctx, buy("old"))
	if err != nil {
		t.Fatal(err)
	}
	if n, err := svc.SweepClosed(ctx); err != nil || n != 0 {
		t.Fatalf("sweep while open: %d %v", n, err)
	}
	c.t = c.t.Add(time.Minute)
	// Let in by a copy of the flag that was behind: a spot order, a
	// repayment (the account owes the BTC a buy brings) and an AUTO_REPAY
	// sell bringing USDT, which it does not owe (review B155).
	led.debts = map[string]string{"MARGIN_CROSS  BTC": "0.002"}
	place := func(id string, account domain.AccountType, effect domain.SideEffect, side domain.Side) domain.Order {
		r := marginBuy(id, account, effect)
		r.Side = side
		o, err := svc.Place(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	late := place("late", domain.AccountSpot, domain.SideEffectNone, domain.SideBuy)
	repay := place("repay", domain.AccountMarginCross, domain.SideEffectAutoRepay, domain.SideBuy)
	owesNothing := place("sell", domain.AccountMarginCross, domain.SideEffectAutoRepay, domain.SideSell)
	audits := len(store.events)
	products.closed[flags.KeyProductSpot], products.closedAt = true, c.t.Add(5*time.Second)
	if n, err := svc.SweepClosed(ctx); err != nil || n != 2 {
		t.Fatalf("sweep: %d %v", n, err)
	}
	for _, o := range []struct {
		order    domain.Order
		canceled bool
	}{{old, false}, {late, true}, {repay, false}, {owesNothing, true}} {
		if got, _ := store.Read().Orders().Get(ctx, o.order.ID); got.CancelRequested != o.canceled {
			t.Fatalf("%s: canceled %v", o.order.ClientOrderID, got.CancelRequested)
		}
	}
	for _, e := range store.events[audits:] {
		if a, ok := e.msg.(*auditv1.AdminActionPerformed); ok && (a.GetActor() != SweepActor || a.GetAction() != "admin.orders.canceled") {
			t.Fatalf("audit %+v", a)
		}
	}
	if n, err := svc.SweepClosed(ctx); err != nil || n != 0 {
		t.Fatalf("sweep again: %d %v", n, err)
	}
	// Without an answer from the ledger the repayment is canceled too, its
	// audit saying the debt was not read (B156).
	led.debtErr = apperr.Unavailable(errors.New("ledger down"))
	if n, err := svc.SweepClosed(ctx); err != nil || n != 1 {
		t.Fatalf("sweep without the ledger: %d %v", n, err)
	}
	if got, _ := store.Read().Orders().Get(ctx, repay.ID); !got.CancelRequested {
		t.Fatal("the repayment whose debt was not read stayed")
	}
	last, _ := store.events[len(store.events)-1].msg.(*auditv1.AdminActionPerformed)
	if !strings.Contains(last.GetDetails(), repay.ID) || !strings.Contains(last.GetDetails(), `"ledger":"unread"`) {
		t.Fatalf("audit %+v", last)
	}
}

// An order checked just before the line closed is checked again under the
// user's lock and not stored (CancelOpen's second pass takes the ones
// stored before that).
func TestAnOrderOnItsWayInWhenSpotClosesIsRefused(t *testing.T) {
	svc, store, _, _ := newService()
	svc.Products = &lines{closed: map[string]bool{}, closing: 2}
	_, err := svc.Place(context.Background(), buy("c1"))
	if !apperr.Is(err, flags.CodeProductClosed) || len(store.orders) != 0 || len(store.events) != 0 {
		t.Fatalf("%v, %d orders, %d events", err, len(store.orders), len(store.events))
	}
	// A repayment too: its debt was not checked, the line open when it
	// came in (review B155); a retry goes through the check.
	svc, store, led, _ := newService()
	svc.Margin = &fakeMargin{}
	svc.Features = switches{flags.KeyMarginEnabled: true}
	led.debts = map[string]string{"MARGIN_CROSS  BTC": "1"}
	svc.Products = &lines{closed: map[string]bool{}, closing: 2}
	_, err = svc.Place(context.Background(), marginBuy("r1", domain.AccountMarginCross, domain.SideEffectAutoRepay))
	if !apperr.Is(err, flags.CodeProductClosed) || len(store.orders) != 0 {
		t.Fatalf("a repayment: %v, %d orders", err, len(store.orders))
	}
	if _, err := svc.Place(context.Background(), marginBuy("r1", domain.AccountMarginCross, domain.SideEffectAutoRepay)); err != nil {
		t.Fatalf("the repayment again: %v", err)
	}
}

// recordAnchor records, for each anchor asked, whether the pair follows a
// reference market.
type recordAnchor struct{ followed []bool }

func (r *recordAnchor) Anchor(_ context.Context, _ string, followed bool) (decimal.Decimal, error) {
	r.followed = append(r.followed, followed)
	return decimal.Zero, nil
}

// Place asks for the anchor of a pair following a reference market as
// such (the reference price first) and of one that does not (the platform
// coin: a recent trade first) as not (review B150, B153).
func TestPlaceTellsTheAnchorWhetherThePairFollowsAReference(t *testing.T) {
	svc, _, _, _ := newService()
	ctx := context.Background()
	prices := &recordAnchor{}
	svc.Prices = prices
	if _, err := svc.Place(ctx, buy("plain")); err != nil {
		t.Fatal(err)
	}
	inst := svc.Instruments.(fakeInstruments)
	inst.pair.Reference = "BTCUSDT"
	svc.Instruments = inst
	if _, err := svc.Place(ctx, buy("followed")); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(prices.followed, []bool{false, true}) {
		t.Fatalf("followed: %v", prices.followed)
	}
}

// failActive is a store whose transactions cannot read one user's active
// orders (a lock that times out, say).
type failActive struct {
	*memStore
	user string
}

func (s failActive) Tx(ctx context.Context, fn func(ports.Repos) error) error {
	return s.memStore.Tx(ctx, func(r ports.Repos) error { return fn(failRepos{r, s.user}) })
}

type failRepos struct {
	ports.Repos
	user string
}

func (r failRepos) Orders() ports.OrderRepo { return failOrders{r.Repos.Orders(), r.user} }

type failOrders struct {
	ports.OrderRepo
	user string
}

func (o failOrders) Active(ctx context.Context, user, symbol string) ([]domain.Order, error) {
	if user == o.user {
		return nil, errors.New("lock timeout")
	}
	return o.OrderRepo.Active(ctx, user, symbol)
}

// A user whose orders cannot be canceled leaves the others to go on; the
// call then fails with 503, saying how many it canceled and for how many
// users it could not, and a retry takes what is left (review C60, as
// derivatives-service).
func TestCancelOpenGoesOnPastAUserItCannotCancelFor(t *testing.T) {
	svc, store, _, _ := newService()
	ctx := context.Background()
	products := &lines{closed: map[string]bool{}}
	svc.Products = products
	users := []string{"0199b0a0-0000-7000-8000-0000000000d1", "0199b0a0-0000-7000-8000-0000000000d2"}
	for _, u := range users {
		r := buy("o")
		r.UserID = u
		if _, err := svc.Place(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	products.closed[flags.KeyProductSpot] = true
	svc.Store = failActive{store, users[0]}
	canceled, err := svc.CancelOpen(ctx, "ops@example.com", "closing spot")
	if e := apperr.From(err); e.Kind != apperr.KindUnavailable || e.Details["canceled"] != 1 || e.Details["failed_users"] != 1 ||
		len(canceled) != 1 || canceled[0].UserID != users[1] {
		t.Fatalf("cancel-open past a failing user: %+v %v %v", canceled, err, e.Details)
	}
	svc.Store = store
	canceled, err = svc.CancelOpen(ctx, "ops@example.com", "closing spot")
	if err != nil || len(canceled) != 1 || canceled[0].UserID != users[0] {
		t.Fatalf("the retry: %+v %v", canceled, err)
	}
	// Flags that cannot be read are a 503, as derivatives-service answers (B159).
	products.refreshErr = errors.New("config database down")
	if _, err := svc.CancelOpen(ctx, "ops@example.com", "closing spot"); apperr.From(err).Kind != apperr.KindUnavailable {
		t.Fatalf("flags not read: %v", err)
	}
}

// A market buy by quantity (B157) is accepted as the market order it is
// and reaches the engine as a limit buy at its protection price, its IOC
// kept, frozen at that price.
func TestAMarketBuyByQuantityReachesTheEngineAsALimitAtItsProtection(t *testing.T) {
	svc, store, led, _ := newService()
	svc.Prices = fixedAnchor{d("60000")}
	r := buy("by-qty")
	r.Type, r.Price = domain.TypeMarket, decimal.Zero
	o, err := svc.Place(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	// 60000 x 1.1 = 66000 (the protection); 66000 x 0.001 = 66.
	if len(led.calls) != 1 || led.calls[0] != "order:"+o.ID+" 66 USDT" {
		t.Fatalf("freezes: %v", led.calls)
	}
	var accepted *orderv1.OrderAccepted
	var place *orderv1.PlaceOrder
	for _, e := range store.events {
		switch m := e.msg.(type) {
		case *orderv1.OrderAccepted:
			accepted = m
		case *orderv1.PlaceOrder:
			place = m
		}
	}
	if accepted.GetOrder().GetType() != orderv1.OrderType_ORDER_TYPE_MARKET || accepted.GetOrder().GetQuantity() != "0.001" {
		t.Fatalf("accepted %+v", accepted.GetOrder())
	}
	eng := place.GetOrder()
	if eng.GetType() != orderv1.OrderType_ORDER_TYPE_LIMIT || eng.GetPrice() != "66000" || eng.GetQuantity() != "0.001" ||
		eng.GetTimeInForce() != orderv1.TimeInForce_TIME_IN_FORCE_IOC || eng.GetQuoteAmount() != "" || eng.GetProtectionPrice() != "" {
		t.Fatalf("to the engine %+v", eng)
	}
}
