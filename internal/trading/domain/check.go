package domain

import (
	"fmt"
	"regexp"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

// Pair is what the order checks need of a trading pair and its assets.
type Pair struct {
	Symbol, Base, Quote string
	TickSize, LotSize   decimal.Decimal
	MinQuantity         decimal.Decimal
	MaxQuantity         decimal.Decimal
	MinNotional         decimal.Decimal
	// PriceBand is the largest deviation from the anchor price, as a
	// fraction ("0.1" is 10%).
	PriceBand     decimal.Decimal
	MakerFeeRate  decimal.Decimal
	TakerFeeRate  decimal.Decimal
	Status        string
	BaseDecimals  int32
	QuoteDecimals int32
	// Tradable is false when either asset has trading disabled or is
	// risk-restricted.
	Tradable bool
	// Reference is the reference market's symbol the pair follows
	// (ADR-0010), "" when it follows none.
	Reference string
}

// PairTrading is the pair status that accepts new orders.
const PairTrading = "TRADING"

// Request is a new order as the client asks for it; zero amounts are
// absent ones.
type Request struct {
	UserID        string
	ClientOrderID string
	Symbol        string
	Side          Side
	Type          Type
	TimeInForce   TimeInForce
	STP           STP
	Price         decimal.Decimal
	Quantity      decimal.Decimal
	QuoteAmount   decimal.Decimal
	AccountType   AccountType
	SideEffect    SideEffect
}

// Defaults fills in what a request may leave out: the time in force of
// its type (GTC for limit, IOC for market orders), the self-trade mode,
// the SPOT account and no side effect.
func (r Request) Defaults() Request {
	if r.TimeInForce == "" {
		r.TimeInForce = GTC
		if r.Type == TypeMarket {
			r.TimeInForce = IOC
		}
	}
	if r.STP == "" {
		r.STP = CancelNewest
	}
	if r.AccountType == "" {
		r.AccountType = AccountSpot
	}
	if r.SideEffect == "" {
		r.SideEffect = SideEffectNone
	}
	return r
}

var clientOrderID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,36}$`)

// NewOrder checks a request against the pair and returns the order to
// store: NEW, its freeze pending. anchor is the price that the price band
// and market protection are measured from (the last trade, or a reference
// price), zero when there is none yet: then the band is not checked and
// market orders are unbounded (§11.2).
func NewOrder(id string, req Request, pair Pair, anchor decimal.Decimal, now time.Time) (Order, error) {
	req = req.Defaults()
	if pair.Status != PairTrading || !pair.Tradable {
		return Order{}, ErrNotTrading
	}
	o := Order{
		ID: id, UserID: req.UserID, ClientOrderID: req.ClientOrderID, Symbol: pair.Symbol,
		Side: req.Side, Type: req.Type, TimeInForce: req.TimeInForce, STP: req.STP,
		Price: req.Price, Quantity: req.Quantity, QuoteAmount: req.QuoteAmount,
		AccountType: req.AccountType, SideEffect: req.SideEffect,
		Status: StatusNew, FreezeState: FreezePending,
		FilledQuantity: decimal.Zero, FilledQuote: decimal.Zero,
		MakerFeeRate: pair.MakerFeeRate, TakerFeeRate: pair.TakerFeeRate,
		BaseDecimals: pair.BaseDecimals, QuoteDecimals: pair.QuoteDecimals,
		TickSize: pair.TickSize, LotSize: pair.LotSize, BaseAsset: pair.Base, QuoteAsset: pair.Quote,
		CreatedAt: now, UpdatedAt: now,
	}
	if o.ClientOrderID == "" {
		o.ClientOrderID = id
	}
	if !clientOrderID.MatchString(o.ClientOrderID) {
		return Order{}, apperr.Invalid("client_order_id must be 1 to 36 letters, digits, dashes or underscores")
	}
	if o.Side != SideBuy && o.Side != SideSell {
		return Order{}, apperr.Invalid("side must be BUY or SELL")
	}
	if o.STP != CancelNewest && o.STP != CancelOldest && o.STP != CancelBoth {
		return Order{}, apperr.Invalid("self_trade_prevention must be CANCEL_NEWEST, CANCEL_OLDEST or CANCEL_BOTH")
	}
	if o.AccountType != AccountSpot && !o.AccountType.Margin() {
		return Order{}, apperr.Invalid("account must be SPOT, MARGIN_CROSS or MARGIN_ISOLATED")
	}
	switch o.SideEffect {
	case SideEffectNone:
	case SideEffectAutoBorrow, SideEffectAutoRepay:
		if !o.AccountType.Margin() {
			return Order{}, apperr.Invalid("side_effect is for orders on a margin account")
		}
	default:
		return Order{}, apperr.Invalid("side_effect must be NONE, AUTO_BORROW or AUTO_REPAY")
	}
	var err error
	switch o.Type {
	case TypeLimit:
		err = o.checkLimit(pair, anchor)
	case TypeMarket:
		err = o.checkMarket(pair, anchor)
	default:
		err = apperr.Invalid("type must be LIMIT or MARKET")
	}
	if err != nil {
		return Order{}, err
	}
	o.FrozenAsset, o.FrozenAmount = o.freeze(pair)
	return o, nil
}

func (o *Order) checkLimit(pair Pair, anchor decimal.Decimal) error {
	switch o.TimeInForce {
	case GTC, IOC, FOK, PostOnly:
	default:
		return apperr.Invalid("time_in_force must be GTC, IOC, FOK or POST_ONLY")
	}
	if !o.QuoteAmount.IsZero() {
		return apperr.Invalid("quote_amount is for market buys; a limit order takes price and quantity")
	}
	if !o.Price.IsPositive() {
		return apperr.Invalid("a limit order needs a positive price")
	}
	if !o.Price.Mod(pair.TickSize).IsZero() {
		return precision(fmt.Sprintf("the price must be a multiple of the tick size %s", pair.TickSize))
	}
	if err := checkQuantity(o.Quantity, pair); err != nil {
		return err
	}
	if o.Price.Mul(o.Quantity).LessThan(pair.MinNotional) {
		return ErrMinNotional.WithDetail("min_notional", pair.MinNotional.String())
	}
	if anchor.IsPositive() && o.Price.Sub(anchor).Abs().GreaterThan(anchor.Mul(pair.PriceBand)) {
		return ErrOutOfBand.WithDetail("anchor_price", anchor.String()).WithDetail("price_band", pair.PriceBand.String())
	}
	return nil
}

func (o *Order) checkMarket(pair Pair, anchor decimal.Decimal) error {
	if o.TimeInForce != IOC && o.TimeInForce != FOK {
		return apperr.Invalid("a market order takes time_in_force IOC or FOK")
	}
	if !o.Price.IsZero() {
		return apperr.Invalid("a market order has no price")
	}
	if o.Side == SideBuy {
		if !o.Quantity.IsZero() {
			return apperr.Invalid("a market buy spends quote_amount; it takes no quantity")
		}
		if !o.QuoteAmount.IsPositive() {
			return apperr.Invalid("a market buy needs a positive quote_amount")
		}
		if !o.QuoteAmount.Equal(o.QuoteAmount.Truncate(pair.QuoteDecimals)) {
			return precision(fmt.Sprintf("quote_amount has more than %d decimals", pair.QuoteDecimals))
		}
		if o.QuoteAmount.LessThan(pair.MinNotional) {
			return ErrMinNotional.WithDetail("min_notional", pair.MinNotional.String())
		}
		if anchor.IsPositive() {
			// The highest price a buy may pay, on the tick grid.
			o.ProtectionPrice = floorTo(anchor.Mul(decimal.NewFromInt(1).Add(pair.PriceBand)), pair.TickSize)
		}
		return nil
	}
	if !o.QuoteAmount.IsZero() {
		return apperr.Invalid("a market sell sells quantity; it takes no quote_amount")
	}
	if err := checkQuantity(o.Quantity, pair); err != nil {
		return err
	}
	if anchor.IsPositive() {
		if anchor.Mul(o.Quantity).LessThan(pair.MinNotional) {
			return ErrMinNotional.WithDetail("min_notional", pair.MinNotional.String())
		}
		// The lowest price a sell may accept, on the tick grid: the band
		// below the anchor, never under half of it (a band of 100%, as on
		// ETH-BTC, would leave the sell no protection at all).
		o.ProtectionPrice = ceilTo(anchor.Mul(decimal.Max(decimal.NewFromInt(1).Sub(pair.PriceBand), minSellProtection)), pair.TickSize)
	}
	return nil
}

// minSellProtection is the least share of the anchor a market sell
// accepts, whatever the pair's price band.
var minSellProtection = decimal.RequireFromString("0.5")

func checkQuantity(q decimal.Decimal, pair Pair) error {
	if !q.IsPositive() {
		return apperr.Invalid("the order needs a positive quantity")
	}
	if !q.Mod(pair.LotSize).IsZero() {
		return precision(fmt.Sprintf("the quantity must be a multiple of the lot size %s", pair.LotSize))
	}
	if q.LessThan(pair.MinQuantity) || q.GreaterThan(pair.MaxQuantity) {
		return quantityRange(fmt.Sprintf("the quantity must be between %s and %s", pair.MinQuantity, pair.MaxQuantity))
	}
	return nil
}

// freeze returns what the order locks (§5.6): buys the quote they may
// spend, a limit price times quantity rounded up to the quote asset's
// decimals, sells the base they offer.
func (o *Order) freeze(pair Pair) (string, decimal.Decimal) {
	switch {
	case o.Side == SideSell:
		return pair.Base, o.Quantity
	case o.Type == TypeMarket:
		return pair.Quote, o.QuoteAmount
	default:
		return pair.Quote, o.Price.Mul(o.Quantity).RoundCeil(pair.QuoteDecimals)
	}
}

func floorTo(v, step decimal.Decimal) decimal.Decimal {
	return v.Div(step).Floor().Mul(step)
}

func ceilTo(v, step decimal.Decimal) decimal.Decimal {
	return v.Div(step).Ceil().Mul(step)
}
