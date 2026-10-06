package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
)

// Sides of an order.
const (
	SideBuy  = "BUY"
	SideSell = "SELL"
)

// OrderInput is an order on a margin account as spot-trading-service is
// about to freeze it (margin.proto OrderCheck).
type OrderInput struct {
	UserID  string
	OrderID string
	// Account is MARGIN_CROSS, or MARGIN_ISOLATED of the order's pair.
	Account domain.Account
	Symbol  string
	Side    string
	// FreezeAsset and FreezeAmount are what the order freezes: the quote
	// of a buy, the base of a sell.
	FreezeAsset  string
	FreezeAmount decimal.Decimal
	SideEffect   domain.SideEffect
	// Price is the limit, zero for a market order; Quantity the base
	// quantity, zero for a market buy by quote amount.
	Price    decimal.Decimal
	Quantity decimal.Decimal
}

// OrderOutcome is what an order on a margin account needs and leaves:
// what AUTO_BORROW borrows (or would) of the frozen asset, and the margin
// level after that borrow and the order filled (nil without debts).
type OrderOutcome struct {
	Borrow      decimal.Decimal
	BorrowID    string
	MarginLevel *decimal.Decimal
}

// CheckOrder answers whether an order may go on a margin account,
// changing nothing (design §5.1): the switch is on (and margin.auto_borrow
// for AUTO_BORROW), the account takes the pair's two assets and is open,
// every asset of it priced, the freeze fits the free balance plus, with
// AUTO_BORROW, what the account may borrow, and the margin level after
// the borrow and the fill stays at or above the warning level.
func (s *Service) CheckOrder(ctx context.Context, in OrderInput) (OrderOutcome, error) {
	plan, err := s.planOrder(ctx, in)
	if err != nil {
		return OrderOutcome{}, err
	}
	return OrderOutcome{Borrow: plan.borrow, MarginLevel: plan.level}, nil
}

// ReserveOrder runs CheckOrder's checks and, with AUTO_BORROW, borrows
// what the free balance lacks (the first hour's interest with it, under
// the key order:<order_id>), then records the answer: a repeat for the
// order returns it, a zero borrow included (review CH ④); the same order
// with other content is COMMON_IDEMPOTENCY_CONFLICT. A repeat after an
// answer that never came (the ledger out, a crash) finds the order's
// borrow first and finishes it rather than checking the order again
// against balances it already changed (review CR ②, C11). The borrow
// stays when the freeze or the order fails afterwards.
func (s *Service) ReserveOrder(ctx context.Context, in OrderInput) (OrderOutcome, error) {
	read := s.Store.Read()
	if prior, ok, err := read.Reservations().Get(ctx, in.OrderID); err != nil {
		return OrderOutcome{}, err
	} else if ok {
		if prior.UserID != in.UserID || (prior.RequestHash != nil && !bytes.Equal(prior.RequestHash, in.hash())) {
			return OrderOutcome{}, apperr.New(apperr.KindConflict, apperr.CodeIdempotencyConflict, "the order was reserved with other content")
		}
		return OrderOutcome{Borrow: prior.Borrowed, BorrowID: prior.BorrowID, MarginLevel: prior.MarginLevel}, nil
	}
	var out OrderOutcome
	if b, ok, err := read.Borrows().ByKey(ctx, in.UserID, orderKey(in.OrderID)); err != nil {
		return OrderOutcome{}, err
	} else if ok {
		if out, err = s.resumeOrder(ctx, in, b); err != nil {
			return OrderOutcome{}, err
		}
	} else {
		plan, err := s.planOrder(ctx, in)
		if err != nil {
			return OrderOutcome{}, err
		}
		out = OrderOutcome{Borrow: plan.borrow, MarginLevel: plan.level}
		if plan.borrow.IsPositive() {
			if out.BorrowID, err = s.borrowFor(ctx, in, plan.borrow); err != nil {
				return OrderOutcome{}, err
			}
		}
	}
	stored, err := read.Reservations().Insert(ctx, ports.Reservation{
		OrderID: in.OrderID, UserID: in.UserID, AccountType: in.Account.Type, Symbol: in.Symbol, SideEffect: in.SideEffect,
		Borrowed: out.Borrow, BorrowID: out.BorrowID, MarginLevel: out.MarginLevel, RequestHash: in.hash(), CreatedAt: s.Now(),
	})
	if err != nil {
		return OrderOutcome{}, err
	}
	return OrderOutcome{Borrow: stored.Borrowed, BorrowID: stored.BorrowID, MarginLevel: stored.MarginLevel}, nil
}

// resumeOrder finishes an order's borrow found on a repeat: a PENDING one
// goes to the ledger again under its key, a FAILED one answers its
// refusal, a DONE one is the answer — the order was checked when it was
// made, so only the margin level it leaves is worked out again.
func (s *Service) resumeOrder(ctx context.Context, in OrderInput, b ports.Borrow) (OrderOutcome, error) {
	switch b.Status {
	case ports.OpPending:
		if _, err := s.postBorrow(ctx, b); err != nil {
			return OrderOutcome{}, err
		}
	case ports.OpFailed:
		return OrderOutcome{}, failure(b.Failure)
	}
	return OrderOutcome{Borrow: b.Amount, BorrowID: b.ID, MarginLevel: s.levelAfter(ctx, in)}, nil
}

// levelAfter works out the margin level an order leaves, with what the
// account holds now (its borrow in it); nil when it cannot (review CY
// C17 ②: a repeat answers the borrow made, whatever the gates say now).
func (s *Service) levelAfter(ctx context.Context, in OrderInput) *decimal.Decimal {
	read := s.Store.Read()
	pair, err := s.Instruments.Pair(ctx, in.Symbol)
	if err != nil {
		return nil
	}
	cat, err := s.catalog(ctx, read)
	if err != nil {
		return nil
	}
	st, err := s.standingOf(ctx, read, in.UserID)
	if err != nil {
		return nil
	}
	prices := s.Prices.Prices()
	v := domain.Value(filled(st.of(in.Account), in, pair, decimal.Zero, cat, prices), cat.Assets, prices)
	if level, ok := v.Level(); ok {
		return &level
	}
	return nil
}

// orderKey is the key an order's borrow goes under.
func orderKey(orderID string) string { return "order:" + orderID }

// hash digests what ReserveOrder was asked, telling a repeat from another
// request for the same order.
func (in OrderInput) hash() []byte {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%s|%s|%s|%s|%s|%s|%s|%s", in.UserID, in.Account.Key(), in.Symbol, in.Side,
		in.FreezeAsset, in.FreezeAmount.String(), in.SideEffect, in.Price.String(), in.Quantity.String(), in.OrderID))
	return sum[:]
}

// borrowFor borrows for an order (AUTO_BORROW) under the order's key and
// returns the borrow's ID.
func (s *Service) borrowFor(ctx context.Context, in OrderInput, amount decimal.Decimal) (string, error) {
	key := orderKey(in.OrderID)
	if _, err := s.Borrow(ctx, BorrowInput{
		UserID: in.UserID, IdemKey: key, Account: in.Account, Asset: in.FreezeAsset, Amount: amount, OrderID: in.OrderID,
	}); err != nil {
		return "", err
	}
	b, ok, err := s.Store.Read().Borrows().ByKey(ctx, in.UserID, key)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("the borrow of order %s vanished", in.OrderID)
	}
	return b.ID, nil
}

type orderPlan struct {
	borrow decimal.Decimal
	level  *decimal.Decimal
}

// planOrder runs the checks of an order and works out its borrow and the
// margin level it leaves.
func (s *Service) planOrder(ctx context.Context, in OrderInput) (orderPlan, error) {
	if err := s.enabled(in.UserID); err != nil {
		return orderPlan{}, err
	}
	if in.SideEffect == domain.SideEffectAutoBorrow && !s.Features.Enabled(flags.KeyMarginAutoBorrow, flags.Subject{UserID: in.UserID}) {
		return orderPlan{}, domain.ErrDisabled.WithDetail("flag", flags.KeyMarginAutoBorrow)
	}
	if !in.Account.IsCross() && in.Account.Symbol != in.Symbol {
		return orderPlan{}, apperr.Invalid(fmt.Sprintf("an order on %s goes on the isolated account of %s", in.Symbol, in.Symbol))
	}
	if !in.FreezeAmount.IsPositive() {
		return orderPlan{}, apperr.Invalid("the freeze amount must be positive")
	}
	pair, err := s.Instruments.Pair(ctx, in.Symbol)
	if err != nil {
		return orderPlan{}, err
	}
	want := pair.Quote
	if in.Side == SideSell {
		want = pair.Base
	} else if in.Side != SideBuy {
		return orderPlan{}, apperr.Invalid("side must be BUY or SELL")
	}
	if in.FreezeAsset != want {
		return orderPlan{}, apperr.Invalid(fmt.Sprintf("a %s of %s freezes %s, not %s", in.Side, in.Symbol, want, in.FreezeAsset))
	}
	read := s.Store.Read()
	cat, err := s.catalog(ctx, read)
	if err != nil {
		return orderPlan{}, err
	}
	terms, err := cat.Terms(in.Account)
	if err != nil {
		return orderPlan{}, err
	}
	for _, asset := range []string{pair.Base, pair.Quote} {
		if _, err := cat.Asset(in.Account, asset); err != nil {
			return orderPlan{}, err
		}
	}
	if st, ok, err := read.Accounts().Get(ctx, in.UserID, in.Account); err != nil {
		return orderPlan{}, err
	} else if ok && !st.Status.Open() {
		return orderPlan{}, domain.ErrFrozen.WithDetail("status", string(st.Status))
	}
	st, err := s.standingOf(ctx, read, in.UserID)
	if err != nil {
		return orderPlan{}, err
	}
	holdings := st.of(in.Account)
	prices := s.Prices.Prices()
	if v := domain.Value(holdings, cat.Assets, prices); !v.Complete() {
		return orderPlan{}, domain.ErrPriceUnavailable.WithDetail("asset", v.Unpriced[0])
	}
	for _, asset := range []string{pair.Base, pair.Quote} {
		if _, ok := prices.Of(asset); !ok {
			return orderPlan{}, domain.ErrPriceUnavailable.WithDetail("asset", asset)
		}
	}
	plan := orderPlan{borrow: decimal.Zero}
	free := holding(holdings, in.FreezeAsset).Free
	if need := in.FreezeAmount.Sub(free); need.IsPositive() {
		if in.SideEffect != domain.SideEffectAutoBorrow {
			return orderPlan{}, ErrInsufficient.WithDetail("asset", in.FreezeAsset).WithDetail("free", free.String())
		}
		if err := s.eligible(ctx, in.UserID, in.Account.Symbol); err != nil {
			return orderPlan{}, err
		}
		room, err := s.room(ctx, read, cat, st, in.Account, in.FreezeAsset, nil)
		if err != nil {
			return orderPlan{}, err
		}
		t := cat.Assets[in.FreezeAsset]
		need = need.RoundCeil(t.Decimals)
		if most, limit := domain.MaxBorrow(room); need.GreaterThan(most) {
			return orderPlan{}, borrowRefusal(room, need, most, limit)
		}
		plan.borrow = need
	}
	after := filled(holdings, in, pair, plan.borrow, cat, prices)
	v := domain.Value(after, cat.Assets, prices)
	if level, ok := v.Level(); ok {
		plan.level = &level
		if level.LessThan(terms.WarnLevel) {
			return orderPlan{}, domain.ErrLevelTooLow.WithDetail("margin_level", level.String()).
				WithDetail("warn_level", terms.WarnLevel.String())
		}
	}
	return plan, nil
}

// filled is the account's holdings after the order borrowed what it
// lacks (the first hour's interest counted at the asset's fixed or
// current rate is left out: a rounding against the warning level) and
// filled: a buy spends its freeze and gets its quantity (a market buy by
// quote amount at the market price), a sell spends its quantity and gets
// it at its price (a market sell at the market price).
func filled(holdings []domain.Holding, in OrderInput, pair ports.PairInfo, borrow decimal.Decimal, cat Catalog,
	prices domain.Prices,
) []domain.Holding {
	byAsset := map[string]domain.Holding{}
	for _, h := range holdings {
		byAsset[h.Asset] = h
	}
	change := func(asset string, free, borrowed decimal.Decimal) {
		h, ok := byAsset[asset]
		if !ok {
			h = domain.Holding{Asset: asset}
		}
		h.Free, h.Borrowed = h.Free.Add(free), h.Borrowed.Add(borrowed)
		byAsset[asset] = h
	}
	change(in.FreezeAsset, borrow, borrow)
	base, _ := prices.Of(pair.Base)
	quote, _ := prices.Of(pair.Quote)
	// The pair's price in its quote, from the two USDT values.
	market := base.Value.DivRound(quote.Value, 18)
	price := in.Price
	if !price.IsPositive() {
		price = market
	}
	if in.Side == SideBuy {
		qty := in.Quantity
		if !qty.IsPositive() && price.IsPositive() {
			qty = in.FreezeAmount.DivRound(price, cat.Assets[pair.Base].Decimals+2)
		}
		change(pair.Quote, in.FreezeAmount.Neg(), decimal.Zero)
		change(pair.Base, qty, decimal.Zero)
	} else {
		change(pair.Base, in.FreezeAmount.Neg(), decimal.Zero)
		change(pair.Quote, in.FreezeAmount.Mul(price), decimal.Zero)
	}
	out := make([]domain.Holding, 0, len(byAsset))
	for _, h := range byAsset {
		if h.Free.IsNegative() { // a freeze beyond the free balance fails at the ledger anyway
			h.Free = decimal.Zero
		}
		out = append(out, h)
	}
	return out
}
