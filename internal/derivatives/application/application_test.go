package application_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/derivatives/adapters/marks"
	"github.com/lidp280504357/exchange/internal/derivatives/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/derivatives/application"
	"github.com/lidp280504357/exchange/internal/derivatives/domain"
	"github.com/lidp280504357/exchange/internal/derivatives/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
	"github.com/lidp280504357/exchange/migrations"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var perp = domain.Contract{
	Symbol: "BTC-USDT-PERP", Base: "BTC", Quote: "USDT", TickSize: d("0.1"), LotSize: d("0.001"), MinQuantity: d("0.001"),
	MaxQuantity: d("100"), MinNotional: d("5"), PriceBand: d("0.05"), FundingIntervalHours: 8,
	Tiers: []domain.RiskTier{
		{MaxNotional: d("50000"), MaxLeverage: 50, MMR: d("0.004")},
		{MaxNotional: d("250000"), MaxLeverage: 20, MMR: d("0.01")},
	},
	MakerFeeRate: d("0.0002"), TakerFeeRate: d("0.0005"), Status: domain.StatusTrading, QuoteDecimals: 6, BaseDecimals: 8,
}

type instruments struct{}

func (instruments) Contract(_ context.Context, symbol string) (domain.Contract, error) {
	if symbol != perp.Symbol {
		return domain.Contract{}, apperr.NotFound("no such contract")
	}
	return perp, nil
}

func (instruments) Contracts(context.Context) ([]domain.Contract, error) {
	return []domain.Contract{perp}, nil
}

// rates is market-data-service's settled funding rates.
type rates map[string][2]decimal.Decimal

func (r rates) Rate(_ context.Context, symbol string, at time.Time) (decimal.Decimal, decimal.Decimal, bool, error) {
	v, ok := r[fmt.Sprintf("%s|%d", symbol, at.Unix())]
	return v[0], v[1], ok, nil
}

type eligible struct{}

func (eligible) Check(context.Context, string, string, string) (bool, string, error) {
	return true, "", nil
}

// ledger is an in-memory FUTURES ledger with the ledger's semantics:
// idempotent keys, exact freezes, capped charges with the insurance fund
// behind them.
type ledger struct {
	mu                  sync.Mutex
	available, frozen   map[string]decimal.Decimal
	pnlClearing, feeRev decimal.Decimal
	insurance, funding  decimal.Decimal
	done                map[string][]domain.Outcome
}

func newLedger() *ledger {
	return &ledger{
		available: map[string]decimal.Decimal{}, frozen: map[string]decimal.Decimal{}, insurance: d("1000000"),
		done: map[string][]domain.Outcome{},
	}
}

func (l *ledger) Freeze(_ context.Context, key, user, _ string, amount decimal.Decimal, _ string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.done[key]; ok {
		return nil
	}
	if l.available[user].LessThan(amount) {
		return apperr.New(apperr.KindUnprocessable, "LEDGER_INSUFFICIENT_BALANCE", "insufficient balance")
	}
	l.available[user], l.frozen[user] = l.available[user].Sub(amount), l.frozen[user].Add(amount)
	l.done[key] = nil
	return nil
}

func (l *ledger) Unfreeze(_ context.Context, key, user, _ string, amount decimal.Decimal, _ string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.done[key]; ok {
		return nil
	}
	if l.frozen[user].LessThan(amount) {
		return apperr.New(apperr.KindUnprocessable, "LEDGER_INSUFFICIENT_BALANCE", "insufficient frozen")
	}
	l.frozen[user], l.available[user] = l.frozen[user].Sub(amount), l.available[user].Add(amount)
	l.done[key] = nil
	return nil
}

func (l *ledger) Settle(_ context.Context, r ports.SettleRequest) ([]domain.Outcome, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if out, ok := l.done[r.IdemKey]; ok {
		return out, nil
	}
	avail, frozen, pnl, fee, ins, fund := l.available[r.UserID], l.frozen[r.UserID], l.pnlClearing, l.feeRev, l.insurance, l.funding
	out := make([]domain.Outcome, len(r.Moves))
	for i, m := range r.Moves {
		out[i] = domain.Outcome{User: decimal.Zero, Insurance: decimal.Zero, Waived: decimal.Zero}
		bal := &avail
		if m.Frozen {
			bal = &frozen
		}
		take := decimal.Min(m.Amount, decimal.Max(*bal, decimal.Zero))
		if m.Limit != nil {
			take = decimal.Min(take, *m.Limit)
		}
		switch m.Type {
		case domain.MoveUnfreeze:
			frozen, avail = frozen.Sub(m.Amount), avail.Add(m.Amount)
		case domain.MoveFreeze:
			v := m.Amount
			if m.Partial {
				v = decimal.Min(m.Amount, avail)
			}
			out[i].Waived = m.Amount.Sub(v)
			avail, frozen = avail.Sub(v), frozen.Add(v)
		case domain.MoveFee:
			*bal, fee = bal.Sub(take), fee.Add(take)
			out[i].User, out[i].Waived = take.Neg(), m.Amount.Sub(take)
		case domain.MoveProfit:
			*bal, pnl = bal.Add(m.Amount), pnl.Sub(m.Amount)
		case domain.MoveLoss:
			*bal, pnl = bal.Sub(take), pnl.Add(m.Amount)
			out[i].User, out[i].Insurance = take.Neg(), m.Amount.Sub(take)
			ins = ins.Sub(out[i].Insurance)
		case domain.MoveInsurance:
			*bal, ins = bal.Sub(m.Amount), ins.Add(m.Amount)
		case domain.MoveFundingPay:
			*bal, fund = bal.Sub(take), fund.Add(m.Amount)
			out[i].User, out[i].Insurance = take.Neg(), m.Amount.Sub(take)
			ins = ins.Sub(out[i].Insurance)
		case domain.MoveFundingReceive:
			*bal, fund = bal.Add(m.Amount), fund.Sub(m.Amount)
			out[i].User = m.Amount
		default:
			return nil, fmt.Errorf("move %s", m.Type)
		}
		if avail.IsNegative() || frozen.IsNegative() || ins.IsNegative() || fund.IsNegative() {
			return nil, apperr.New(apperr.KindUnprocessable, "LEDGER_INSUFFICIENT_BALANCE", "insufficient balance")
		}
	}
	l.available[r.UserID], l.frozen[r.UserID], l.pnlClearing, l.feeRev, l.insurance, l.funding = avail, frozen, pnl, fee, ins, fund
	l.done[r.IdemKey] = out
	return out, nil
}

func (l *ledger) Balance(_ context.Context, user, _ string) (ports.Balance, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return ports.Balance{Available: l.available[user], Frozen: l.frozen[user]}, nil
}

func (l *ledger) PnLClearing(context.Context, string) (decimal.Decimal, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pnlClearing, nil
}

type rig struct {
	svc    *application.Service
	store  ports.Store
	ledger *ledger
	book   *marks.Book
	rates  rates
	seq    int64
}

// setup runs on PostgreSQL when TEST_POSTGRES_DSN is set (task
// test:integration), in memory otherwise.
func setup(t *testing.T) *rig {
	t.Helper()
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	r := &rig{ledger: newLedger(), book: marks.New(), rates: rates{}}
	if os.Getenv("TEST_POSTGRES_DSN") == "" {
		r.store = newMemStore()
	} else {
		db := testenv.Postgres(t)
		if err := migrate.UpPlatform(ctx, db, log); err != nil {
			t.Fatal(err)
		}
		if err := migrate.Up(ctx, db, migrations.Derivatives(), log); err != nil {
			t.Fatal(err)
		}
		r.store = postgres.NewStore(db, event.NewFactory("derivatives-service", "test"))
	}
	r.book.Set(perp.Symbol, d("60000"), time.Now())
	r.svc = &application.Service{
		Store: r.store, Ledger: r.ledger, Instruments: instruments{}, Eligibility: eligible{}, Marks: r.book, Rates: r.rates,
		Log: log, Now: time.Now, Metrics: application.NewMetrics(prometheus.NewRegistry()),
	}
	return r
}

func (r *rig) fund(user, amount string) {
	r.ledger.available[user] = r.ledger.available[user].Add(d(amount))
}

func (r *rig) place(t *testing.T, user string, side domain.Side, price, qty string, reduceOnly bool) domain.Order {
	t.Helper()
	o, err := r.svc.Place(context.Background(), domain.Request{
		UserID: user, Symbol: perp.Symbol, Side: side, Type: domain.Limit, Price: d(price), Qty: d(qty), ReduceOnly: reduceOnly,
	})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// trade has the engine fill maker against taker completely, as the
// engine would report it: the trade, then both orders FILLED.
func (r *rig) trade(t *testing.T, maker, taker domain.Order, price string) {
	t.Helper()
	ctx := context.Background()
	buy, sell := maker, taker
	if maker.Side == domain.Sell {
		buy, sell = taker, maker
	}
	r.seq++
	tr := application.Trade{
		ID: uuid.Must(uuid.NewV7()).String(), Symbol: perp.Symbol, Seq: r.seq, Price: d(price), Qty: maker.Qty,
		BuyerOrderID: buy.ID, BuyerUserID: buy.UserID, SellerOrderID: sell.ID, SellerUserID: sell.UserID,
		BuyerIsMaker: buy.ID == maker.ID, At: time.Now(),
	}
	if err := r.svc.OnTrade(ctx, tr); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.OnTrade(ctx, tr); err != nil { // redelivered
		t.Fatal(err)
	}
	quote := d(price).Mul(maker.Qty)
	for _, o := range []domain.Order{maker, taker} {
		r.seq++
		if err := r.svc.OnUpdate(ctx, domain.Update{OrderID: o.ID, Seq: r.seq, Status: domain.StatusFilled, Filled: o.Qty, FilledQuote: quote}); err != nil {
			t.Fatal(err)
		}
	}
}

func (r *rig) position(t *testing.T, user string) domain.Position {
	t.Helper()
	list, err := r.svc.Positions(context.Background(), user, perp.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) == 0 {
		return domain.Position{}
	}
	return list[0].Position
}

func (r *rig) reconcile(t *testing.T) {
	t.Helper()
	results, err := (&application.Reconciler{Svc: r.svc, Asset: "USDT"}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, res := range results {
		if len(res.Mismatches) > 0 {
			t.Fatalf("%s: %+v", res.Check, res.Mismatches)
		}
	}
	// Every user's frozen balance is their orders' reservations and their
	// positions' margin.
	for user, frozen := range r.ledger.frozen {
		sum, err := r.svc.Account(context.Background(), user, "USDT")
		if err != nil {
			t.Fatal(err)
		}
		if !sum.OrderMargin.Add(sum.PositionMargin).Equal(frozen) {
			t.Fatalf("%s frozen %s, orders %s + positions %s", user, frozen, sum.OrderMargin, sum.PositionMargin)
		}
	}
}

func TestOpenAndCloseBetweenTwoUsers(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	r.fund(alice, "10000")
	r.fund(bob, "10000")
	ten, twenty := int32(10), int32(20)
	isolated := domain.Isolated
	if _, err := r.svc.UpdateSettings(ctx, alice, perp.Symbol, application.SettingsChange{Leverage: &ten}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.UpdateSettings(ctx, bob, perp.Symbol, application.SettingsChange{MarginMode: &isolated, Leverage: &twenty}); err != nil {
		t.Fatal(err)
	}

	// Alice (cross, 10x) bids 0.1 at 60000: 600 of margin and a 3 fee
	// reserve; Bob (isolated, 20x) sells into it.
	bid := r.place(t, alice, domain.Buy, "60000", "0.1", false)
	if bid.Status != domain.StatusNew || bid.FreezeState != domain.FreezeDone || !bid.Unreleased().Equal(d("603")) {
		t.Fatalf("bid %+v", bid)
	}
	ask := r.place(t, bob, domain.Sell, "60000", "0.1", false)
	r.trade(t, bid, ask, "60000")
	long, short := r.position(t, alice), r.position(t, bob)
	if !long.Qty.Equal(d("0.1")) || !long.Margin.Equal(d("600")) || long.MarginMode != domain.Cross ||
		!short.Qty.Equal(d("-0.1")) || !short.Margin.Equal(d("300")) || short.MarginMode != domain.Isolated {
		t.Fatalf("positions %+v %+v", long, short)
	}
	// Maker fee 1.2 for Alice, taker fee 3 for Bob, both out of their
	// reserves; the rest was released.
	if !r.ledger.available[alice].Equal(d("9398.8")) || !r.ledger.available[bob].Equal(d("9697")) || !r.ledger.feeRev.Equal(d("4.2")) {
		t.Fatalf("balances %s %s fees %s", r.ledger.available[alice], r.ledger.available[bob], r.ledger.feeRev)
	}
	r.reconcile(t)

	// The mark moves up: Alice's cross profit counts for nothing that can
	// leave, Bob's loss would.
	r.book.Set(perp.Symbol, d("61000"), time.Now())
	if pnl, err := r.svc.CrossUnrealizedPnL(ctx, alice); err != nil || !pnl.Equal(d("100")) {
		t.Fatalf("cross result %s %v", pnl, err)
	}
	// Bob adds 50 of margin, then closes at 61000; Alice closes with a
	// reduce-only order.
	if p, err := r.svc.AdjustMargin(ctx, bob, perp.Symbol, "", d("50")); err != nil || !p.Margin.Equal(d("350")) {
		t.Fatalf("add margin %+v %v", p, err)
	}
	closeLong := r.place(t, alice, domain.Sell, "61000", "0.1", true)
	if closeLong.Reserving() {
		t.Fatal("a reduce-only order reserves nothing")
	}
	if _, err := r.svc.Place(ctx, domain.Request{
		UserID: alice, Symbol: perp.Symbol, Side: domain.Sell, Type: domain.Limit, Price: d("61000"), Qty: d("0.1"), ReduceOnly: true,
	}); apperr.From(err).Code != "DERIV_REDUCE_ONLY_REJECTED" {
		t.Fatalf("a second reduce-only order over the same position: %v", err)
	}
	closeShort := r.place(t, bob, domain.Buy, "61000", "0.1", false)
	r.trade(t, closeLong, closeShort, "61000")
	if p := r.position(t, alice); !p.Flat() {
		t.Fatalf("alice still holds %+v", p)
	}
	// Alice: +100 profit, 1.22 maker fee. Bob: −100 loss out of the 350
	// margin, 3.05 taker fee out of his order's reserve.
	if !r.ledger.pnlClearing.IsZero() || !r.ledger.available[alice].Equal(d("10097.58")) || !r.ledger.available[bob].Equal(d("9893.95")) {
		t.Fatalf("after closing: clearing %s alice %s bob %s", r.ledger.pnlClearing, r.ledger.available[alice], r.ledger.available[bob])
	}
	r.reconcile(t)

	fills, _, err := r.svc.Fills(ctx, alice, "", "", 10)
	if err != nil || len(fills) != 2 || !fills[0].RealizedPnL.Equal(d("100")) || !fills[0].ClosedQty.Equal(d("0.1")) {
		t.Fatalf("fills %+v %v", fills, err)
	}
}

func TestSettingsAndCancels(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	r.fund(alice, "5000")
	r.fund(bob, "5000")
	resting := r.place(t, alice, domain.Buy, "59000", "0.05", false)
	hedge := domain.Hedge
	if _, err := r.svc.UpdateSettings(ctx, alice, perp.Symbol, application.SettingsChange{PositionMode: &hedge}); apperr.From(err).Code != "DERIV_SETTINGS_LOCKED" {
		t.Fatalf("mode change with an open order: %v", err)
	}
	// Canceled by the engine: the whole reservation comes back.
	if _, err := r.svc.Cancel(ctx, alice, resting.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.OnUpdate(ctx, domain.Update{
		OrderID: resting.ID, Seq: 1, Status: domain.StatusCanceled, Filled: decimal.Zero,
		FilledQuote: decimal.Zero, Reason: "USER",
	}); err != nil {
		t.Fatal(err)
	}
	if !r.ledger.available[alice].Equal(d("5000")) || !r.ledger.frozen[alice].IsZero() {
		t.Fatalf("after the cancel: %s/%s", r.ledger.available[alice], r.ledger.frozen[alice])
	}
	if s, err := r.svc.UpdateSettings(ctx, alice, perp.Symbol, application.SettingsChange{PositionMode: &hedge}); err != nil || s.PositionMode != domain.Hedge {
		t.Fatalf("mode change when flat: %+v %v", s, err)
	}

	// At 20x a position may reach 250000 of notional: not 5 BTC; and 2
	// BTC needs 6000 + 60 of margin.
	if _, err := r.svc.Place(ctx, domain.Request{
		UserID: bob, Symbol: perp.Symbol, Side: domain.Buy, Type: domain.Limit, Price: d("60000"), Qty: d("5"),
	}); apperr.From(err).Code != "DERIV_RISK_LIMIT_EXCEEDED" {
		t.Fatalf("5 BTC at 20x: %v", err)
	}
	if _, err := r.svc.Place(ctx, domain.Request{
		UserID: bob, Symbol: perp.Symbol, Side: domain.Buy, Type: domain.Limit, Price: d("60000"), Qty: d("2"),
	}); apperr.From(err).Code != "DERIV_INSUFFICIENT_MARGIN" {
		t.Fatalf("2 BTC with 5000: %v", err)
	}

	// Leverage 10 → 5 on an open cross position freezes 600 more.
	long := r.place(t, bob, domain.Buy, "60000", "0.1", false)
	short, err := r.svc.Place(ctx, domain.Request{
		UserID: alice, Symbol: perp.Symbol, Side: domain.Sell, PositionSide: domain.SideShort, Type: domain.Limit,
		Price: d("60000"), Qty: d("0.1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	r.trade(t, long, short, "60000")
	if p := r.position(t, alice); p.Side != domain.SideShort || !p.Qty.Equal(d("-0.1")) {
		t.Fatalf("alice's hedge short %+v", p)
	}
	lev := int32(5)
	if _, err := r.svc.UpdateSettings(ctx, bob, perp.Symbol, application.SettingsChange{Leverage: &lev}); err != nil {
		t.Fatal(err)
	}
	if p := r.position(t, bob); !p.Margin.Equal(d("1200")) || p.Leverage != 5 {
		t.Fatalf("after 5x: %+v", p)
	}
	r.reconcile(t)
}

func TestRefusedSettlementsWaitForTheLedger(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	r.fund(alice, "10000")
	r.fund(bob, "10000")
	fifty, isolated := int32(50), domain.Isolated
	if _, err := r.svc.UpdateSettings(ctx, bob, perp.Symbol, application.SettingsChange{MarginMode: &isolated, Leverage: &fifty}); err != nil {
		t.Fatal(err)
	}
	long := r.place(t, alice, domain.Buy, "60000", "0.1", false)
	short := r.place(t, bob, domain.Sell, "60000", "0.1", false)
	r.trade(t, long, short, "60000")
	if p := r.position(t, bob); !p.Margin.Equal(d("120")) {
		t.Fatalf("bob's isolated short %+v", p)
	}

	// The price runs to 63000: Bob's buy-back loses 300 on 120 of margin,
	// and the insurance fund is empty. The positions move on, the money
	// waits.
	r.ledger.insurance = decimal.Zero
	r.book.Set(perp.Symbol, d("62000"), time.Now())
	closeShort := r.place(t, bob, domain.Buy, "63000", "0.1", false)
	closeLong := r.place(t, alice, domain.Sell, "63000", "0.1", true)
	r.trade(t, closeShort, closeLong, "63000")
	if p := r.position(t, bob); !p.Flat() {
		t.Fatalf("bob still holds %+v", p)
	}
	results, err := (&application.Reconciler{Svc: r.svc, Asset: "USDT"}).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(results[1].Mismatches) != 1 || len(results[2].Mismatches) != 1 {
		t.Fatalf("while parked: %+v", results)
	}
	fills, _, err := r.svc.Fills(ctx, bob, "", "", 10)
	if err != nil || fills[0].Settled || !fills[0].RealizedPnL.Equal(d("-300")) {
		t.Fatalf("bob's fills %+v %v", fills, err)
	}

	// Refilled, the retry books it.
	if n, err := r.svc.RetryPending(ctx); err != nil || n != 0 {
		t.Fatalf("still short: %d %v", n, err)
	}
	r.ledger.insurance = d("1000")
	if n, err := r.svc.RetryPending(ctx); err != nil || n != 1 {
		t.Fatalf("retry: %d %v", n, err)
	}
	if !r.ledger.insurance.Equal(d("820")) {
		t.Fatalf("insurance paid %s", d("1000").Sub(r.ledger.insurance))
	}
	r.reconcile(t)
}

func TestOrderEventsMayComeBeforeTheirTrades(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	r.fund(alice, "10000")
	r.fund(bob, "10000")
	bid := r.place(t, alice, domain.Buy, "60000", "0.3", false)
	ask := r.place(t, bob, domain.Sell, "60000", "0.1", false)
	// The engine fills 0.1 of the bid and Alice cancels the rest; the
	// cancel arrives before the trade.
	if err := r.svc.OnUpdate(ctx, domain.Update{
		OrderID: bid.ID, Seq: 5, Status: domain.StatusCanceled, Filled: d("0.1"),
		FilledQuote: d("6000"), Reason: "USER",
	}); err != nil {
		t.Fatal(err)
	}
	// Released: the reservation of 0.2; 0.1's reservation (at 20x: 300 +
	// 3) stays frozen for the fill to come.
	if !r.ledger.frozen[alice].Equal(d("303")) {
		t.Fatalf("frozen before the trade %s", r.ledger.frozen[alice])
	}
	r.seq = 10
	if err := r.svc.OnTrade(ctx, application.Trade{
		ID: uuid.Must(uuid.NewV7()).String(), Symbol: perp.Symbol, Seq: 4, Price: d("60000"), Qty: d("0.1"),
		BuyerOrderID: bid.ID, BuyerUserID: alice, SellerOrderID: ask.ID, SellerUserID: bob, BuyerIsMaker: true, At: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.OnUpdate(ctx, domain.Update{OrderID: ask.ID, Seq: 6, Status: domain.StatusFilled, Filled: d("0.1"), FilledQuote: d("6000")}); err != nil {
		t.Fatal(err)
	}
	if p := r.position(t, alice); !p.Margin.Equal(d("300")) || !p.Qty.Equal(d("0.1")) {
		t.Fatalf("alice %+v", p)
	}
	r.reconcile(t)
}

func TestTheMarkPriceGatesOrders(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	alice := uuid.NewString()
	r.fund(alice, "10000")
	r.book = marks.New()
	r.svc.Marks = r.book
	r.book.Set(perp.Symbol, d("60000"), time.Now().Add(-time.Minute))
	if _, err := r.svc.Place(ctx, domain.Request{
		UserID: alice, Symbol: perp.Symbol, Side: domain.Buy, Type: domain.Market, Qty: d("0.1"),
	}); apperr.From(err).Code != "DERIV_MARK_PRICE_UNAVAILABLE" {
		t.Fatalf("a stale mark: %v", err)
	}
	r.book.Set(perp.Symbol, d("60000"), time.Now())
	if err := r.svc.OnDegraded(ctx, perp.Symbol, "INDEX_SOURCES"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Place(ctx, domain.Request{
		UserID: alice, Symbol: perp.Symbol, Side: domain.Buy, Type: domain.Market, Qty: d("0.1"),
	}); apperr.From(err).Code != "DERIV_REDUCE_ONLY_MODE" {
		t.Fatalf("a degraded contract: %v", err)
	}
	if ok, err := r.svc.LiftReduceOnly(ctx, perp.Symbol, "test"); err != nil || !ok {
		t.Fatalf("lift %v %v", ok, err)
	}
	o, err := r.svc.Place(ctx, domain.Request{UserID: alice, Symbol: perp.Symbol, Side: domain.Buy, Type: domain.Market, Qty: d("0.1")})
	if err != nil || !o.Price.Equal(d("63000")) || o.TimeInForce != domain.IOC {
		t.Fatalf("a market buy %+v %v", o, err)
	}
}

func TestFundingSettlesAtTheSettlementMark(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	r.fund(alice, "10000")
	r.fund(bob, "10000")
	twenty, isolated := int32(20), domain.Isolated
	if _, err := r.svc.UpdateSettings(ctx, bob, perp.Symbol, application.SettingsChange{MarginMode: &isolated, Leverage: &twenty}); err != nil {
		t.Fatal(err)
	}
	long := r.place(t, alice, domain.Buy, "60000", "0.1", false)
	short := r.place(t, bob, domain.Sell, "60000", "0.1", false)
	r.trade(t, long, short, "60000")

	// 08:00:03: the positions of the 08:00 funding are taken; the rate
	// comes a little later.
	eight := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	r.svc.Now = func() time.Time { return eight.Add(3 * time.Second) }
	if n, err := r.svc.SnapshotFunding(ctx); err != nil || n != 1 {
		t.Fatalf("snapshot: %d %v", n, err)
	}
	if n, err := r.svc.SnapshotFunding(ctx); err != nil || n != 0 {
		t.Fatalf("a second snapshot: %d %v", n, err)
	}
	if n, err := r.svc.SettleFunding(ctx); err != nil || n != 0 {
		t.Fatalf("before the rate: %d %v", n, err)
	}
	r.rates[fmt.Sprintf("%s|%d", perp.Symbol, eight.Unix())] = [2]decimal.Decimal{d("0.0001"), d("60010")}
	if n, err := r.svc.SettleFunding(ctx); err != nil || n != 1 {
		t.Fatalf("settle: %d %v", n, err)
	}
	// 0.1 x 60010 x 0.0001 = 0.6001: the long pays it from available, the
	// isolated short receives it into its margin.
	if p := r.position(t, alice); !p.Funding.Equal(d("-0.6001")) {
		t.Fatalf("alice %+v", p)
	}
	if p := r.position(t, bob); !p.Funding.Equal(d("0.6001")) || !p.Margin.Equal(d("300.6001")) {
		t.Fatalf("bob %+v", p)
	}
	if !r.ledger.funding.IsZero() {
		t.Fatalf("FUNDING_CLEARING %s", r.ledger.funding)
	}
	list, _, err := r.svc.FundingPayments(ctx, alice, "", "", 10)
	if err != nil || len(list) != 1 || !list[0].Amount.Equal(d("-0.6001")) || !list[0].Rate.Equal(d("0.0001")) || !list[0].Mark.Equal(d("60010")) {
		t.Fatalf("alice's funding %+v %v", list, err)
	}
	if n, err := r.svc.SettleFunding(ctx); err != nil || n != 0 {
		t.Fatalf("settled twice: %d %v", n, err)
	}
	r.svc.Now = time.Now
	r.reconcile(t)
}

// liquidationSetup opens Bob's isolated 50x long of 0.5 against Alice's
// cross short, both at 60000: Bob's margin is 600, his bankruptcy price
// 58800; at the first tier (mmr 0.004) he is warned at or below about
// 59083 and liquidated at or below about 59036.
func liquidationSetup(t *testing.T) (r *rig, alice, bob string) {
	t.Helper()
	r = setup(t)
	ctx := context.Background()
	alice, bob = uuid.NewString(), uuid.NewString()
	r.fund(alice, "10000")
	r.fund(bob, "1000")
	fifty, isolated := int32(50), domain.Isolated
	if _, err := r.svc.UpdateSettings(ctx, bob, perp.Symbol, application.SettingsChange{MarginMode: &isolated, Leverage: &fifty}); err != nil {
		t.Fatal(err)
	}
	long := r.place(t, bob, domain.Buy, "60000", "0.5", false)
	short := r.place(t, alice, domain.Sell, "60000", "0.5", false)
	r.trade(t, long, short, "60000")
	if p := r.position(t, bob); !p.Margin.Equal(d("600")) {
		t.Fatalf("bob %+v", p)
	}
	return r, alice, bob
}

func (r *rig) monitor(t *testing.T, mark string) {
	t.Helper()
	r.book.Set(perp.Symbol, d(mark), time.Now())
	if err := r.svc.Monitor(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// liquidationOrder returns the user's active liquidation order.
func (r *rig) liquidationOrder(t *testing.T, user string) (domain.Order, bool) {
	t.Helper()
	list, _, err := r.svc.List(context.Background(), user, perp.Symbol, "ACTIVE", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range list {
		if o.Kind == domain.KindLiquidation {
			return o, true
		}
	}
	return domain.Order{}, false
}

func TestALiquidationOrderClosesTheIsolatedLong(t *testing.T) {
	r, alice, bob := liquidationSetup(t)
	ctx := context.Background()
	r.monitor(t, "59050")
	if p := r.position(t, bob); p.WarnedAt.IsZero() || p.Liquidating {
		t.Fatalf("warned: %+v", p)
	}
	r.monitor(t, "59000")
	if p := r.position(t, bob); !p.Liquidating {
		t.Fatalf("taken over: %+v", p)
	}
	if _, err := r.svc.Place(ctx, domain.Request{
		UserID: bob, Symbol: perp.Symbol, Side: domain.Sell, Type: domain.Limit, Price: d("59000"), Qty: d("0.5"), ReduceOnly: true,
	}); apperr.From(err).Code != "DERIV_POSITION_LIQUIDATING" {
		t.Fatalf("bob's own close: %v", err)
	}
	r.monitor(t, "59000")
	liq, ok := r.liquidationOrder(t, bob)
	if !ok || liq.Side != domain.Sell || !liq.Price.Equal(d("58506")) || liq.TimeInForce != domain.IOC {
		t.Fatalf("liquidation order %+v %v", liq, ok)
	}
	r.monitor(t, "59000") // waits for it
	if p := r.position(t, bob); p.LiquidationAttempts != 1 {
		t.Fatalf("attempts %d", p.LiquidationAttempts)
	}
	// Alice's reduce-only bid at 58600 takes it: Bob loses 700 on 600 of
	// margin; the insurance fund pays 100 and the fee is waived.
	bid := r.place(t, alice, domain.Buy, "58600", "0.5", true)
	r.trade(t, bid, liq, "58600")
	if p := r.position(t, bob); !p.Flat() || p.Liquidating {
		t.Fatalf("bob after the liquidation %+v", p)
	}
	fills, _, err := r.svc.Fills(ctx, bob, "", "", 10)
	if err != nil || !fills[0].Liquidation || !fills[0].Insurance.Equal(d("100")) || !fills[0].Fee.IsZero() {
		t.Fatalf("bob's liquidation fill %+v %v", fills[0], err)
	}
	// Bob keeps what was not his margin: 1000 − 600 − the 6 maker fee.
	if !r.ledger.insurance.Equal(d("999900")) || !r.ledger.frozen[bob].IsZero() || !r.ledger.available[bob].Equal(d("394")) {
		t.Fatalf("insurance %s, bob %s/%s", r.ledger.insurance, r.ledger.available[bob], r.ledger.frozen[bob])
	}
	r.reconcile(t)
}

func TestAnUnfillableLiquidationIsDeleveraged(t *testing.T) {
	r, alice, bob := liquidationSetup(t)
	ctx := context.Background()
	r.monitor(t, "59000") // taken over
	for i := range domain.MaxLiquidationAttempts {
		r.monitor(t, "59000")
		liq, ok := r.liquidationOrder(t, bob)
		if !ok {
			t.Fatalf("attempt %d: no liquidation order", i+1)
		}
		r.seq++
		if err := r.svc.OnUpdate(ctx, domain.Update{
			OrderID: liq.ID, Seq: r.seq, Status: domain.StatusCanceled, Filled: decimal.Zero,
			FilledQuote: decimal.Zero, Reason: "IOC",
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Nothing filled three times: Alice's short, the only counterparty, is
	// closed against Bob's long at his bankruptcy price 58800.
	r.monitor(t, "59000")
	if p := r.position(t, bob); !p.Flat() {
		t.Fatalf("bob %+v", p)
	}
	if p := r.position(t, alice); !p.Flat() {
		t.Fatalf("alice %+v", p)
	}
	theirs, _, err := r.svc.Fills(ctx, alice, "", "", 10)
	if err != nil || !theirs[0].RealizedPnL.Equal(d("600")) || !theirs[0].Fee.IsZero() {
		t.Fatalf("alice's deleveraged fill %+v %v", theirs[0], err)
	}
	fills, _, err := r.svc.Fills(ctx, bob, "", "", 10)
	if err != nil || !fills[0].RealizedPnL.Equal(d("-600")) || !fills[0].Insurance.IsZero() || !fills[0].Price.Equal(d("58800")) {
		t.Fatalf("bob's deleveraged fill %+v %v", fills[0], err)
	}
	r.reconcile(t)
}

func TestACrossAccountIsLiquidatedTogether(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	r.fund(alice, "700")
	r.fund(bob, "10000")
	fifty := int32(50)
	if _, err := r.svc.UpdateSettings(ctx, alice, perp.Symbol, application.SettingsChange{Leverage: &fifty}); err != nil {
		t.Fatal(err)
	}
	long := r.place(t, bob, domain.Buy, "60000", "0.5", false)
	short := r.place(t, alice, domain.Sell, "60000", "0.5", false)
	r.trade(t, long, short, "60000")
	// Alice's cross equity: 85 available + 600 margin, less her loss;
	// maintenance 0.4% of the notional.
	r.monitor(t, "61100")
	if at, err := r.store.Read().Cross().WarnedAt(ctx, alice); err != nil || at.IsZero() {
		t.Fatalf("cross warning %v %v", at, err)
	}
	r.monitor(t, "61200")
	if p := r.position(t, alice); !p.Liquidating {
		t.Fatalf("taken over %+v", p)
	}
	r.monitor(t, "61200")
	liq, ok := r.liquidationOrder(t, alice)
	if !ok || liq.Side != domain.Buy || !liq.Price.Equal(d("61506")) {
		t.Fatalf("liquidation order %+v %v", liq, ok)
	}
	ask := r.place(t, bob, domain.Sell, "61300", "0.5", true)
	r.trade(t, ask, liq, "61300")
	if p := r.position(t, alice); !p.Flat() {
		t.Fatalf("alice %+v", p)
	}
	// 685 − the 650 loss − the 15.325 fee.
	if !r.ledger.available[alice].Equal(d("19.675")) || !r.ledger.frozen[alice].IsZero() {
		t.Fatalf("alice %s/%s", r.ledger.available[alice], r.ledger.frozen[alice])
	}
	r.reconcile(t)
}
