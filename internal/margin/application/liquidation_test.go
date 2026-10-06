package application_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/margin/application"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
)

// trading executes a liquidation's market orders against HOUSE at the
// price, on the fake ledger, in whole lots of 0.0001: each attempt is an
// order of its own, and the same attempt again answers the order as it
// stands. fill is the share of an order that executes (nil: all of it;
// whatever executes, at least a lot); pending is how many reads answer an
// order NEW before it executes; fail answers every call while set.
type trading struct {
	mu       sync.Mutex
	ledger   *ledger
	prices   *prices
	canceled int
	fill     func(o ports.LiquidationOrder) decimal.Decimal
	pending  int
	fail     error
	orders   map[string]*placed
	sent     []ports.LiquidationOrder
}

type placed struct {
	state ports.OrderState
	reads int
}

var lot = d("0.0001")

func (t *trading) CancelAccount(context.Context, string, domain.Account) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.canceled++
	return nil
}

func (t *trading) PlaceLiquidation(_ context.Context, user string, a domain.Account, o ports.LiquidationOrder) (ports.OrderState, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fail != nil {
		return ports.OrderState{}, t.fail
	}
	key := fmt.Sprintf("%s|%s|%s|%d", o.LiquidationID, o.Symbol, o.Side, o.Attempt)
	if t.orders == nil {
		t.orders = map[string]*placed{}
	}
	p, ok := t.orders[key]
	if !ok {
		p = &placed{state: ports.OrderState{
			OrderID: uuid.Must(uuid.NewV7()).String(), Status: "NEW", FilledQuantity: decimal.Zero,
			FilledQuote: decimal.Zero,
		}}
		t.orders[key] = p
		t.sent = append(t.sent, o)
	}
	if p.state.Final() {
		return p.state, nil
	}
	if p.reads++; p.reads <= t.pending {
		return p.state, nil
	}
	t.execute(user, a, o, &p.state)
	return p.state, nil
}

// execute fills an order as fill says, on the fake ledger.
func (t *trading) execute(user string, a domain.Account, o ports.LiquidationOrder, st *ports.OrderState) {
	share := decimal.NewFromInt(1)
	if t.fill != nil {
		share = t.fill(o)
	}
	base, quote, _ := strings.Cut(o.Symbol, "-")
	all := t.prices.Prices()
	pb, _ := all.Of(base)
	pq, _ := all.Of(quote)
	price := pb.Value.DivRound(pq.Value, 18)
	var qty decimal.Decimal
	if o.Side == "SELL" {
		qty = o.Quantity
	} else {
		qty = o.QuoteAmount.DivRound(price, 18).Div(lot).Floor().Mul(lot)
	}
	if share.LessThan(decimal.NewFromInt(1)) {
		qty = decimal.Min(qty, decimal.Max(lot, qty.Mul(share).Div(lot).Floor().Mul(lot)))
		if share.IsZero() {
			qty = decimal.Zero
		}
	}
	value := qty.Mul(price).RoundFloor(6)
	t.ledger.mu.Lock()
	acct := t.ledger.margin[user][a]
	get := func(asset string) *balances {
		if acct[asset] == nil {
			acct[asset] = &balances{}
		}
		return acct[asset]
	}
	if o.Side == "SELL" {
		get(base).free = get(base).free.Sub(qty)
		get(quote).free = get(quote).free.Add(value)
	} else {
		get(quote).free = get(quote).free.Sub(value)
		get(base).free = get(base).free.Add(qty)
	}
	t.ledger.mu.Unlock()
	st.FilledQuantity, st.FilledQuote, st.Status = qty, value, "FILLED"
	if qty.IsZero() {
		st.Status = "EXPIRED"
	}
}

// pairs answers the pairs as instruments does, some of them halted.
type pairs struct {
	instruments
	mu     sync.Mutex
	halted map[string]bool
}

func (p *pairs) Pair(ctx context.Context, symbol string) (ports.PairInfo, error) {
	info, err := p.instruments.Pair(ctx, symbol)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.halted[symbol] {
		info.Status = "HALT"
	}
	return info, err
}

func (p *pairs) halt(symbol string, on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.halted == nil {
		p.halted = map[string]bool{}
	}
	p.halted[symbol] = on
}

// liquidationRig is a rig whose liquidations trade on the fake trading
// service, and a monitor whose passes advance them.
type liquidationRig struct {
	*rig
	trading *trading
	pairs   *pairs
	monitor *application.Monitor
}

func newLiquidationRig(t *testing.T) *liquidationRig {
	t.Helper()
	r := &liquidationRig{rig: newRig(t)}
	r.trading = &trading{ledger: r.ledger, prices: r.prices}
	r.pairs = &pairs{}
	r.svc.Trading, r.svc.Instruments = r.trading, r.pairs
	r.monitor = &application.Monitor{Svc: r.svc, Liquidate: r.svc.AutoLiquidate}
	return r
}

// pass runs n monitor passes, step apart.
func (r *liquidationRig) pass(t *testing.T, n int, step time.Duration) {
	t.Helper()
	for range n {
		r.at(r.now().Add(step))
		if _, err := r.monitor.Pass(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

// open gives a new user a cross account: in its BTC, borrowed USDT, then
// what it holds set to holds (as if it had traded) — what it owes stays.
func (r *liquidationRig) open(t *testing.T, in decimal.Decimal, borrow string, amount decimal.Decimal, holds map[string]string) string {
	t.Helper()
	ctx := context.Background()
	u := uuid.Must(uuid.NewV7()).String()
	r.ledger.fund(u, "BTC", in)
	cross := domain.Cross()
	if _, err := r.svc.Transfer(ctx, application.TransferInput{
		UserID: u, IdemKey: "t", Direction: domain.DirectionIn, Account: cross, Asset: "BTC", Amount: in,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: u, IdemKey: "b", Account: cross, Asset: borrow, Amount: amount}); err != nil {
		t.Fatal(err)
	}
	r.ledger.mu.Lock()
	acct := r.ledger.margin[u][cross]
	for asset, b := range acct {
		b.free = decimal.Zero
		acct[asset] = b
	}
	for asset, v := range holds {
		if acct[asset] == nil {
			acct[asset] = &balances{}
		}
		acct[asset].free = d(v)
	}
	r.ledger.mu.Unlock()
	return u
}

// liquidate starts an administrator's liquidation of the user's cross
// account.
func (r *liquidationRig) liquidate(t *testing.T, user string) ports.Liquidation {
	t.Helper()
	l, err := r.svc.StartLiquidation(context.Background(), user, domain.Cross(), ports.TriggerManual, uuid.Must(uuid.NewV7()).String(), "ops@example.com")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func (r *liquidationRig) get(t *testing.T, id string) ports.Liquidation {
	t.Helper()
	l, ok, err := r.store.Read().Liquidations().Get(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("liquidation %s: %v", id, err)
	}
	return l
}

func (r *liquidationRig) orders(t *testing.T, id string) []ports.LiquidationOrder {
	t.Helper()
	list, err := r.store.Read().Liquidations().Orders(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

// fund is what the insurance fund holds of USDT.
func (r *liquidationRig) fund() decimal.Decimal {
	r.ledger.mu.Lock()
	defer r.ledger.mu.Unlock()
	return r.ledger.insurance["USDT"]
}

// TestLiquidation checks the liquidation (design §4.5, review DD C19): an
// account holding BTC bought with borrowed USDT, at its liquidation level
// twice with margin.liquidation on, is liquidated: as much of its BTC sold
// to HOUSE as its debt and the fee need, 2% more, the fee (2% of what was
// sold) to the insurance fund, the debt repaid, the rest left to it; an
// administrator's liquidation of an account whose BTC does not cover its
// debt sells all of it and has the insurance fund pay what is left.
func TestLiquidation(t *testing.T) {
	r := newLiquidationRig(t)
	ctx := context.Background()
	cross := domain.Cross()
	// Each user: 0.1 BTC in, 2000 USDT borrowed and spent on 0.0667 BTC.
	open := func() string { return r.open(t, d("0.1"), "USDT", d("2000"), map[string]string{"BTC": "0.1667"}) }
	auto, short := open(), open()
	// BTC at 13,000: 0.1667 x 13000 x 0.95 / 2000.02 = 1.029, under 1.10.
	r.prices.setBTC("13000", true)
	r.pass(t, 2, 3*time.Second)
	if l, _, err := r.svc.Liquidations(ctx, auto, nil, "", 10); err != nil || len(l) != 0 {
		t.Fatalf("liquidated with margin.liquidation off: %+v %v", l, err)
	}
	r.features.set(flags.KeyMarginLiquidation, true)
	r.svc.Features = r.features
	r.pass(t, 8, 3*time.Second)
	list, _, err := r.svc.Liquidations(ctx, auto, nil, "", 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("liquidations %+v %v", list, err)
	}
	l := list[0]
	// It owes 2000.02: (2000.02 / 0.98) x 1.02 = 2081.65 to bring, 0.1602
	// BTC sold for 2082.6; a fee of 2% of that, 2000.02 repaid.
	if l.Status != ports.LiquidationCompleted || l.Trigger != ports.TriggerAuto || !l.Traded.Equal(d("2082.6")) ||
		!l.Fee.Equal(d("41.652")) || !l.InsuranceCovered.IsZero() || len(l.Repaid) != 1 || !l.Repaid[0].Amount.Equal(d("2000.02")) {
		t.Fatalf("the liquidation %+v", l)
	}
	if b := r.ledger.owed(auto, cross, "USDT"); !b.free.Equal(d("40.928")) || !b.borrowed.IsZero() || !b.interest.IsZero() {
		t.Fatalf("the USDT left %+v", b)
	}
	if b := r.ledger.owed(auto, cross, "BTC"); !b.free.Equal(d("0.0065")) {
		t.Fatalf("the BTC left %+v", b)
	}
	if orders := r.orders(t, l.ID); len(orders) != 1 || orders[0].Attempt != 1 || orders[0].Status != ports.OrderDone ||
		orders[0].OrderStatus != "FILLED" || !orders[0].Quantity.Equal(d("0.1602")) || !orders[0].FilledQuote.Equal(d("2082.6")) {
		t.Fatalf("the orders %+v", orders)
	}
	if st, _, err := r.store.Read().Accounts().Get(ctx, auto, cross); err != nil || st.Status != domain.StatusNormal {
		t.Fatalf("the account after %+v %v", st, err)
	}
	if loan, err := r.store.Read().Loans().Get(ctx, auto, cross, "USDT"); err != nil || !loan.Principal.IsZero() || !loan.Interest.IsZero() {
		t.Fatalf("the loan after %+v %v", loan, err)
	}

	// The other account was liquidated too at 13,000; the next one: an
	// administrator's, at 9,000, where its BTC does not cover its debt.
	other := open()
	r.prices.setBTC("9000", true)
	approval := uuid.Must(uuid.NewV7()).String()
	started, err := r.svc.StartLiquidation(ctx, other, cross, ports.TriggerManual, approval, "ops@example.com")
	if err != nil || started.Status != ports.LiquidationStarted {
		t.Fatalf("manual %+v %v", started, err)
	}
	if again, err := r.svc.StartLiquidation(ctx, other, cross, ports.TriggerManual, approval, "ops@example.com"); err != nil || again.ID != started.ID {
		t.Fatalf("the same approval again %+v %v", again, err)
	}
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: other, IdemKey: "b-2", Account: cross, Asset: "USDT", Amount: d("1")}); code(err) != "MARGIN_FROZEN" {
		t.Fatalf("borrowing while liquidated: %v", err)
	}
	r.pass(t, 8, 3*time.Second)
	got := r.get(t, started.ID)
	// All of it sold for 1500.3, the fee (30.006) first, then 1470.294
	// repaid; the fund paid the 529.726 left.
	if got.Status != ports.LiquidationCompleted || !got.Traded.Equal(d("1500.3")) || !got.Fee.Equal(d("30.006")) ||
		!got.InsuranceCovered.Equal(d("529.726")) {
		t.Fatalf("the shortfall %+v", got)
	}
	if b := r.ledger.owed(other, cross, "USDT"); !b.free.IsZero() || !b.borrowed.IsZero() || !b.interest.IsZero() {
		t.Fatalf("after the fund %+v", b)
	}
	if _, err := r.svc.StartLiquidation(ctx, other, cross, ports.TriggerManual, uuid.Must(uuid.NewV7()).String(), "ops@example.com"); code(err) != "MARGIN_NOTHING_OWED" {
		t.Fatalf("nothing owed: %v", err)
	}
	if list, _, err := r.svc.Liquidations(ctx, short, nil, "", 10); err != nil || len(list) != 1 {
		t.Fatalf("the second account at 13,000 %+v %v", list, err)
	}
}

// TestLiquidationFeeBeforeBuy checks an account owing an asset it does
// not hold (review DD C19 ④⑤): as much BTC sold as buying back its ETH and
// the fee on both need, 2% more; the fee charged on what was sold and what
// will be bought before the buy, which spends the rest; the ETH repaid,
// the insurance fund not used.
func TestLiquidationFeeBeforeBuy(t *testing.T) {
	r := newLiquidationRig(t)
	cross := domain.Cross()
	// 0.2 BTC in, 2 ETH borrowed and sold (as if); ETH up to 2700: 0.2 x
	// 30000 x 0.95 / 5400 = 1.055.
	u := r.open(t, d("0.2"), "ETH", d("2"), map[string]string{"BTC": "0.2"})
	r.prices.mu.Lock()
	r.prices.p["ETH"] = domain.Price{Value: d("2700"), Fresh: true}
	r.prices.mu.Unlock()
	before := r.fund()
	l := r.liquidate(t, u)
	r.pass(t, 4, 3*time.Second)
	got := r.get(t, l.ID)
	if got.Status != ports.LiquidationCompleted || !got.InsuranceCovered.IsZero() {
		t.Fatalf("the liquidation %+v", got)
	}
	orders := r.orders(t, l.ID)
	if len(orders) != 2 || orders[0].Side != "SELL" || !orders[0].Quantity.Equal(d("0.195")) || orders[1].Side != "BUY" {
		t.Fatalf("the orders %+v", orders)
	}
	// The fee: 2% of the 5850 the BTC brought and of what the buy spends.
	want := orders[0].FilledQuote.Add(orders[1].QuoteAmount).Mul(d("0.02")).RoundFloor(6)
	if !got.Fee.Equal(want) || !r.fund().Equal(before.Add(want)) {
		t.Fatalf("the fee %s, want %s (the fund %s)", got.Fee, want, r.fund())
	}
	if b := r.ledger.owed(u, cross, "ETH"); !b.borrowed.IsZero() || !b.interest.IsZero() || !b.free.IsPositive() {
		t.Fatalf("the ETH after %+v", b)
	}
	if b := r.ledger.owed(u, cross, "BTC"); !b.free.Equal(d("0.005")) {
		t.Fatalf("the BTC left %+v", b)
	}
}

// TestLiquidationSellsUntilCovered checks orders that execute short
// (review DD C19 ①②): each round sells again what is still needed as the
// next attempt, at once after a partial fill and after a growing wait
// after one that executed nothing; what counts is what the trading
// service reports; the insurance fund pays nothing while there is BTC to
// sell.
func TestLiquidationSellsUntilCovered(t *testing.T) {
	r := newLiquidationRig(t)
	cross := domain.Cross()
	u := r.open(t, d("0.1"), "USDT", d("2000"), map[string]string{"BTC": "0.1667"})
	r.prices.setBTC("13000", true)
	// The first two orders execute nothing, the next ones half.
	r.trading.fill = func(o ports.LiquidationOrder) decimal.Decimal {
		if o.Attempt <= 2 {
			return decimal.Zero
		}
		return d("0.5")
	}
	before := r.fund()
	l := r.liquidate(t, u)
	r.pass(t, 1, time.Second)
	got := r.get(t, l.ID)
	if got.Step != application.StepSell || !strings.Contains(got.Note, "executed nothing") {
		t.Fatalf("after two orders that executed nothing %+v", got)
	}
	r.pass(t, 1, time.Second) // the backoff after the second: 2 seconds
	if n := len(r.orders(t, l.ID)); n != 2 {
		t.Fatalf("%d orders within the backoff", n)
	}
	r.pass(t, 10, 3*time.Second)
	got = r.get(t, l.ID)
	if got.Status != ports.LiquidationCompleted || !got.InsuranceCovered.IsZero() || !r.fund().Equal(before.Add(got.Fee)) {
		t.Fatalf("the liquidation %+v", got)
	}
	orders := r.orders(t, l.ID)
	traded := decimal.Zero
	for i, o := range orders {
		if o.Symbol != "BTC-USDT" || o.Side != "SELL" || o.Attempt != i+1 || o.Status != ports.OrderDone {
			t.Fatalf("order %d %+v", i, o)
		}
		traded = traded.Add(o.FilledQuote)
	}
	if len(orders) < 4 || !got.Traded.Equal(traded) || !orders[0].FilledQuantity.IsZero() || orders[0].OrderStatus != "EXPIRED" {
		t.Fatalf("%d orders, traded %s of %s: %+v", len(orders), got.Traded, traded, orders)
	}
	if b := r.ledger.owed(u, cross, "USDT"); !b.borrowed.IsZero() || !b.interest.IsZero() {
		t.Fatalf("the debt after %+v", b)
	}
	if b := r.ledger.owed(u, cross, "BTC"); !b.free.IsPositive() {
		t.Fatalf("all BTC sold %+v", b)
	}
}

// TestLiquidationWaitsForThePair checks a liquidation whose pair is
// halted (review DD C19 ①): no order, the step waits and says why, the
// insurance fund pays nothing; once the pair trades again it goes on.
func TestLiquidationWaitsForThePair(t *testing.T) {
	r := newLiquidationRig(t)
	u := r.open(t, d("0.1"), "USDT", d("2000"), map[string]string{"BTC": "0.1667"})
	r.prices.setBTC("13000", true)
	r.pairs.halt("BTC-USDT", true)
	before := r.fund()
	l := r.liquidate(t, u)
	r.pass(t, 5, time.Minute)
	got := r.get(t, l.ID)
	if got.Step != application.StepSell || got.Note != "waiting for BTC-USDT to trade (HALT)" || len(r.orders(t, l.ID)) != 0 ||
		!r.fund().Equal(before) {
		t.Fatalf("halted %+v", got)
	}
	r.pairs.halt("BTC-USDT", false)
	r.pass(t, 2, 3*time.Second)
	if got := r.get(t, l.ID); got.Status != ports.LiquidationCompleted || !got.InsuranceCovered.IsZero() || !got.Traded.Equal(d("2082.6")) {
		t.Fatalf("after the halt %+v", got)
	}
}

// TestLiquidationReadsTheOrderUntilItEnds checks that an order the
// trading service took is read again until it executes no more, even with
// nothing locked in the account (review DD C19 ②), and that its failures
// other than a refusal (404 here) only wait.
func TestLiquidationReadsTheOrderUntilItEnds(t *testing.T) {
	r := newLiquidationRig(t)
	u := r.open(t, d("0.1"), "USDT", d("2000"), map[string]string{"BTC": "0.1667"})
	r.prices.setBTC("13000", true)
	r.trading.fail = apperr.NotFound("no such route")
	l := r.liquidate(t, u)
	r.pass(t, 2, 3*time.Second)
	got := r.get(t, l.ID)
	if orders := r.orders(t, l.ID); got.Step != application.StepSell || !strings.HasPrefix(got.Note, "sending the orders") ||
		len(orders) != 1 || orders[0].Status != ports.OrderPlanned {
		t.Fatalf("with the trading service answering 404 %+v %+v", got, orders)
	}
	r.trading.mu.Lock()
	r.trading.fail, r.trading.pending = nil, 3
	r.trading.mu.Unlock()
	r.pass(t, 2, 3*time.Second)
	got = r.get(t, l.ID)
	if orders := r.orders(t, l.ID); got.Step != application.StepSell || got.Note != "waiting for the orders to execute" ||
		len(orders) != 1 || orders[0].Status != ports.OrderSent || orders[0].OrderStatus != "NEW" {
		t.Fatalf("taken, not executed %+v %+v", got, orders)
	}
	r.pass(t, 2, 3*time.Second)
	if got := r.get(t, l.ID); got.Status != ports.LiquidationCompleted || !got.Traded.Equal(d("2082.6")) || !got.InsuranceCovered.IsZero() {
		t.Fatalf("executed %+v", got)
	}
}

// TestLiquidationRepaysAgain checks a refused repayment (review DD C19
// ③): made again under a new key with what is owed and held then; the
// insurance fund covers nothing the account could pay.
func TestLiquidationRepaysAgain(t *testing.T) {
	r := newLiquidationRig(t)
	ctx := context.Background()
	cross := domain.Cross()
	u := r.open(t, d("0.1"), "USDT", d("2000"), map[string]string{"BTC": "0.1667"})
	r.prices.setBTC("13000", true)
	refused := 0
	r.ledger.mu.Lock()
	r.ledger.refuse = func(p ports.Posting) error {
		if p.Moves[0].Type == domain.MoveLiquidationRepay && refused == 0 {
			refused++
			return apperr.New(apperr.KindUnprocessable, "LEDGER_INSUFFICIENT_BALANCE", "insufficient free")
		}
		return nil
	}
	r.ledger.mu.Unlock()
	l := r.liquidate(t, u)
	r.pass(t, 4, 3*time.Second)
	got := r.get(t, l.ID)
	if got.Status != ports.LiquidationCompleted || !got.InsuranceCovered.IsZero() || refused != 1 {
		t.Fatalf("the liquidation %+v (refused %d)", got, refused)
	}
	repays, err := r.store.Read().Repays().OfLiquidation(ctx, l.ID)
	if err != nil || len(repays) != 2 || repays[0].Status != ports.OpFailed || repays[1].Status != ports.OpDone ||
		repays[1].IdemKey != fmt.Sprintf("liquidation:%s:repay:USDT:1", l.ID) || repays[1].CoveredBy != "" {
		t.Fatalf("the repayments %+v %v", repays, err)
	}
	if b := r.ledger.owed(u, cross, "USDT"); !b.borrowed.IsZero() || !b.interest.IsZero() {
		t.Fatalf("the debt after %+v", b)
	}
}
