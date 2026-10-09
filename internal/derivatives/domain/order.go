package domain

import (
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

// Side of an order.
type Side string

// Sides.
const (
	Buy  Side = "BUY"
	Sell Side = "SELL"
)

// Type of an order: a market order goes to the engine as a limit order at
// its protection price, IOC or FOK (§7.3 decision: the engine's market
// buys spend a quote amount, which contracts do not have).
type Type string

// Types.
const (
	Limit  Type = "LIMIT"
	Market Type = "MARKET"
)

// TimeInForce of an order.
type TimeInForce string

// Times in force.
const (
	GTC      TimeInForce = "GTC"
	IOC      TimeInForce = "IOC"
	FOK      TimeInForce = "FOK"
	PostOnly TimeInForce = "POST_ONLY"
)

// Kind tells the user's orders from the liquidation engine's.
type Kind string

// Kinds: the user's, the liquidation engine's, the synthetic orders of
// an auto-deleveraging, and the orders a take-profit or stop-loss placed.
const (
	KindUser        Kind = "USER"
	KindLiquidation Kind = "LIQUIDATION"
	KindADL         Kind = "ADL"
	KindTakeProfit  Kind = "TAKE_PROFIT"
	KindStopLoss    Kind = "STOP_LOSS"
	// KindAdmin closes a position for the admin console (design
	// 2026-10-02 §4.1, force close).
	KindAdmin Kind = "ADMIN"
	// KindHouse is HOUSE's side of a trade against its reference liquidity
	// (ADR-0015): never stored, only the shape its fills are worked out in.
	KindHouse Kind = "HOUSE"
)

// HouseOrderID stands for the order HOUSE does not have on its fills.
const HouseOrderID = "00000000-0000-0000-0000-000000000000"

// HouseOrder is the order HOUSE's side of a fill is worked out as
// (ADR-0015): one-way, cross margin at the contract's top leverage, no
// fees, nothing reserved (opening freezes margin from its available
// balance as far as it goes).
func HouseOrder(c Contract, user string, side Side, qty decimal.Decimal) Order {
	return Order{
		ID: HouseOrderID, UserID: user, Symbol: c.Symbol, Side: side, PositionSide: SideBoth, Type: Limit, TimeInForce: IOC,
		Qty: qty, Kind: KindHouse, Leverage: c.MaxLeverage(), MarginMode: Cross, MakerFee: decimal.Zero, TakerFee: decimal.Zero,
		LotSize: c.LotSize, Status: StatusNew, FreezeState: FreezeNone,
		Consumed: decimal.Zero, Filled: decimal.Zero, FilledQuote: decimal.Zero, Fee: decimal.Zero, RealizedPnL: decimal.Zero,
	}
}

// Status of an order (appendix B, as spot).
type Status string

// Statuses.
const (
	StatusNew             Status = "NEW"
	StatusOpen            Status = "OPEN"
	StatusPartiallyFilled Status = "PARTIALLY_FILLED"
	StatusFilled          Status = "FILLED"
	StatusCanceled        Status = "CANCELED"
	StatusRejected        Status = "REJECTED"
	StatusExpired         Status = "EXPIRED"
)

// ActiveStatuses may still fill.
var ActiveStatuses = []Status{StatusNew, StatusOpen, StatusPartiallyFilled}

var transitions = map[Status][]Status{
	StatusNew:             {StatusOpen, StatusPartiallyFilled, StatusFilled, StatusCanceled, StatusRejected},
	StatusOpen:            {StatusPartiallyFilled, StatusFilled, StatusCanceled, StatusExpired},
	StatusPartiallyFilled: {StatusPartiallyFilled, StatusFilled, StatusCanceled, StatusExpired},
}

// Active reports whether the order may still fill.
func (s Status) Active() bool { return slices.Contains(ActiveStatuses, s) }

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	return s.Active() || s == StatusFilled || s == StatusCanceled || s == StatusRejected || s == StatusExpired
}

// Terminal reports whether the order is finished.
func (s Status) Terminal() bool { return s.Valid() && !s.Active() }

// FreezeState tracks the ledger freeze of an order's reservation: PENDING
// between storing the order and recording the freeze (a crash in between
// is repaired by retrying it), FROZEN once done (also for orders that
// reserve nothing), NONE when rejected.
type FreezeState string

// Freeze states.
const (
	FreezePending FreezeState = "PENDING"
	FreezeDone    FreezeState = "FROZEN"
	FreezeNone    FreezeState = "NONE"
)

// Order is a contract order.
type Order struct {
	ID            string
	ClientOrderID string
	UserID        string
	Symbol        string
	Side          Side
	PositionSide  PositionSide
	Type          Type
	TimeInForce   TimeInForce
	// Price is the limit price, or a market order's protection price.
	Price      decimal.Decimal
	Qty        decimal.Decimal
	ReduceOnly bool
	Kind       Kind
	Leverage   int32
	MarginMode MarginMode
	MakerFee   decimal.Decimal
	TakerFee   decimal.Decimal
	LotSize    decimal.Decimal
	// MarginPerLot and FeePerLot are what an opening order reserved per
	// lot: price x lot / leverage and price x lot x taker rate, rounded
	// up, a sell's price no lower than the mark when it was placed. Both
	// zero for an order that only closes.
	MarginPerLot decimal.Decimal
	FeePerLot    decimal.Decimal
	// Consumed is the quantity whose reservation fills used; the rest is
	// released once the order is finished (Released).
	Consumed        decimal.Decimal
	Released        bool
	Status          Status
	FreezeState     FreezeState
	CancelRequested bool
	CancelReason    string
	RejectReason    string
	Filled          decimal.Decimal
	FilledQuote     decimal.Decimal
	Fee             decimal.Decimal
	RealizedPnL     decimal.Decimal
	Sequence        int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Reserving reports whether the order reserved margin.
func (o Order) Reserving() bool { return o.MarginPerLot.IsPositive() || o.FeePerLot.IsPositive() }

// Reservation is what qty of the order reserved.
func (o Order) Reservation(qty decimal.Decimal) (margin, fee decimal.Decimal) {
	lots := qty.Div(o.LotSize)
	return o.MarginPerLot.Mul(lots), o.FeePerLot.Mul(lots)
}

// Unreleased is what the order still has frozen: the reservation of the
// quantity fills did not consume yet, less what was released when it
// finished (the reservation of the quantity it did not fill).
func (o Order) Unreleased() decimal.Decimal {
	open := o.Qty.Sub(o.Consumed)
	if o.Released {
		open = o.Filled.Sub(o.Consumed)
	}
	m, f := o.Reservation(decimal.Max(open, decimal.Zero))
	return m.Add(f)
}

// ToRelease is what a finished order unfreezes: the reservation of the
// quantity the engine did not fill. It does not depend on whether the
// fills were applied yet, so order and trade events may come in any order.
func (o Order) ToRelease() decimal.Decimal {
	m, f := o.Reservation(o.Qty.Sub(o.Filled))
	return m.Add(f)
}

// Opening reports whether a hedge-mode order opens (BUY LONG, SELL SHORT).
func (o Order) Opening() bool {
	return (o.PositionSide == SideLong && o.Side == Buy) || (o.PositionSide == SideShort && o.Side == Sell)
}

// Closing reports whether the order may only reduce: reduce-only, or a
// hedge-mode order against its side.
func (o Order) Closing() bool {
	return o.ReduceOnly || (o.PositionSide != SideBoth && !o.Opening())
}

// Request is a new order as the client asks for it.
type Request struct {
	UserID        string
	ClientOrderID string
	Symbol        string
	Side          Side
	PositionSide  PositionSide
	Type          Type
	TimeInForce   TimeInForce
	Price         decimal.Decimal
	Qty           decimal.Decimal
	ReduceOnly    bool
	// Kind is set for the orders a take-profit or stop-loss places; the
	// user's own are USER.
	Kind Kind
	// Conditional is the take-profit or stop-loss placing the order: Place
	// refuses it once that has ended (ErrConditionalEnded) and ends it
	// TRIGGERED with the order in the order's transaction (review C69).
	Conditional string
}

// Defaults fills in the time in force (GTC, IOC for market orders).
func (r Request) Defaults(mode PositionMode) Request {
	if r.TimeInForce == "" {
		r.TimeInForce = GTC
		if r.Type == Market {
			r.TimeInForce = IOC
		}
	}
	if r.PositionSide == "" && mode == OneWay {
		r.PositionSide = SideBoth
	}
	return r
}

// SameAs reports whether a request repeating an order's client_order_id
// asks for the same order.
func (o Order) SameAs(r Request) bool {
	return o.Symbol == r.Symbol && o.Side == r.Side && o.Type == r.Type && o.Qty.Equal(r.Qty) &&
		o.ReduceOnly == r.ReduceOnly && (o.Type == Market || o.Price.Equal(r.Price)) &&
		(r.TimeInForce == "" || o.TimeInForce == r.TimeInForce) && (r.PositionSide == "" || o.PositionSide == r.PositionSide)
}

var clientOrderID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,36}$`)

// Errors of order checks.
var (
	ErrMinNotional    = apperr.New(apperr.KindUnprocessable, "ORDER_MIN_NOTIONAL", "the order is below the minimum notional")
	ErrOutOfBand      = apperr.New(apperr.KindUnprocessable, "ORDER_PRICE_OUT_OF_BAND", "the price is too far from the mark price")
	ErrTooManyOpen    = apperr.New(apperr.KindUnprocessable, "ORDER_TOO_MANY_OPEN", "too many active orders")
	ErrAlreadyFilled  = apperr.New(apperr.KindConflict, "ORDER_ALREADY_FILLED", "the order is already filled")
	ErrOrderFinished  = apperr.New(apperr.KindConflict, apperr.CodeConflict, "the order is already finished")
	ErrOrderNotFound  = apperr.NotFound("no such order")
	ErrClientIDReused = apperr.New(apperr.KindConflict, apperr.CodeIdempotencyConflict, "client_order_id already names another order")
)

// Limits on active orders per user.
const (
	MaxActivePerSymbol = 200
	MaxActive          = 1000
)

// NewOrder checks a request against the contract, the user's settings and
// the mark price, and returns the order to store: NEW, its freeze pending
// when it reserves margin. It does not look at positions or balances; the
// caller checks reduce-only orders and margin with them.
func NewOrder(id string, req Request, c Contract, s Settings, mark decimal.Decimal, now time.Time) (Order, error) {
	req = req.Defaults(s.PositionMode)
	if c.Status != StatusTrading {
		return Order{}, ErrNotTrading
	}
	o := Order{
		ID: id, ClientOrderID: req.ClientOrderID, UserID: req.UserID, Symbol: c.Symbol, Side: req.Side,
		PositionSide: req.PositionSide, Type: req.Type, TimeInForce: req.TimeInForce, Price: req.Price, Qty: req.Qty,
		ReduceOnly: req.ReduceOnly, Kind: KindUser, Leverage: s.Leverage, MarginMode: s.MarginMode,
		MakerFee: c.MakerFeeRate, TakerFee: c.TakerFeeRate, LotSize: c.LotSize,
		MarginPerLot: decimal.Zero, FeePerLot: decimal.Zero, Consumed: decimal.Zero,
		Status: StatusNew, FreezeState: FreezePending, Filled: decimal.Zero, FilledQuote: decimal.Zero,
		Fee: decimal.Zero, RealizedPnL: decimal.Zero, CreatedAt: now, UpdatedAt: now,
	}
	if o.ClientOrderID == "" {
		o.ClientOrderID = id
	}
	if req.Kind != "" {
		o.Kind = req.Kind
	}
	switch {
	case !clientOrderID.MatchString(o.ClientOrderID):
		return Order{}, apperr.Invalid("client_order_id must be 1 to 36 letters, digits, dashes or underscores")
	case o.Side != Buy && o.Side != Sell:
		return Order{}, apperr.Invalid("side must be BUY or SELL")
	case s.PositionMode == OneWay && o.PositionSide != SideBoth, s.PositionMode == Hedge && o.PositionSide != SideLong && o.PositionSide != SideShort:
		return Order{}, ErrPositionSide
	case s.PositionMode == Hedge && o.ReduceOnly:
		return Order{}, apperr.Invalid("hedge mode has no reduce_only: an order against its position side reduces it")
	}
	if !mark.IsPositive() {
		return Order{}, ErrMarkUnavailable
	}
	if err := o.checkQuantity(c); err != nil {
		return Order{}, err
	}
	one := decimal.NewFromInt(1)
	switch o.Type {
	case Limit:
		switch o.TimeInForce {
		case GTC, IOC, FOK, PostOnly:
		default:
			return Order{}, apperr.Invalid("time_in_force must be GTC, IOC, FOK or POST_ONLY")
		}
		if !o.Price.IsPositive() {
			return Order{}, apperr.Invalid("a limit order needs a positive price")
		}
		if !o.Price.Mod(c.TickSize).IsZero() {
			return Order{}, apperr.New(apperr.KindInvalid, "INSTRUMENT_PRECISION", fmt.Sprintf("the price must be a multiple of the tick size %s", c.TickSize))
		}
		if o.Price.Sub(mark).Abs().GreaterThan(mark.Mul(c.PriceBand)) {
			return Order{}, ErrOutOfBand.WithDetail("mark_price", mark.String()).WithDetail("price_band", c.PriceBand.String())
		}
	case Market:
		if o.TimeInForce != IOC && o.TimeInForce != FOK {
			return Order{}, apperr.Invalid("a market order takes time_in_force IOC or FOK")
		}
		if !o.Price.IsZero() {
			return Order{}, apperr.Invalid("a market order has no price")
		}
		// The worst price it may fill at, on the tick grid, within the band.
		if o.Side == Buy {
			o.Price = floorTo(mark.Mul(one.Add(c.PriceBand)), c.TickSize)
		} else {
			o.Price = ceilTo(mark.Mul(one.Sub(c.PriceBand)), c.TickSize)
		}
		if !o.Price.IsPositive() {
			return Order{}, ErrMarkUnavailable
		}
	default:
		return Order{}, apperr.Invalid("type must be LIMIT or MARKET")
	}
	// The notional is measured at the order price, or at the mark for a
	// market order (its protection price is far off on purpose).
	at := o.Price
	if o.Type == Market {
		at = mark
	}
	notional := at.Mul(o.Qty)
	if c.Inverse() { // in dollars: the contracts' face value
		notional = o.Qty.Mul(c.ContractSize)
	}
	if notional.LessThan(c.MinNotional) && !o.Closing() {
		return Order{}, ErrMinNotional.WithDetail("min_notional", c.MinNotional.String())
	}
	if !o.Closing() {
		// A buy fills at its price or below, so its price covers the margin
		// at the fill. A sell fills at its price or above, near the mark
		// when it crosses: it reserves at the higher of the two, or a market
		// sell, whose protection price is the band below the mark, would
		// hold less than the initial margin (0.76% at 125x). An inverse
		// contract's coin value grows as the price falls, so the other way
		// round: a sell's price covers the coin at its fill, a buy reserves
		// at the lower of its price and the mark (coin-M §2.2).
		reserveAt := o.Price
		switch {
		case c.Inverse() && o.Side == Buy:
			reserveAt = decimal.Min(o.Price, mark)
		case !c.Inverse() && o.Side == Sell:
			reserveAt = decimal.Max(o.Price, mark)
		}
		lot := c.exact(c.LotSize, reserveAt)
		o.MarginPerLot = ceil(lot.Div(decimal.NewFromInt32(o.Leverage)), c.QuoteDecimals)
		o.FeePerLot = ceil(lot.Mul(c.TakerFeeRate), c.QuoteDecimals)
	} else {
		o.FreezeState = FreezeDone // nothing to freeze
	}
	return o, nil
}

func (o Order) checkQuantity(c Contract) error {
	switch {
	case !o.Qty.IsPositive():
		return apperr.Invalid("the order needs a positive quantity")
	case c.Inverse() && !o.Qty.IsInteger():
		return ErrContractsNotInteger
	case !o.Qty.Mod(c.LotSize).IsZero():
		return apperr.New(apperr.KindInvalid, "INSTRUMENT_PRECISION", fmt.Sprintf("the quantity must be a multiple of the lot size %s", c.LotSize))
	case o.Qty.LessThan(c.MinQuantity) || o.Qty.GreaterThan(c.MaxQuantity):
		return apperr.New(apperr.KindInvalid, "ORDER_QUANTITY_OUT_OF_RANGE",
			fmt.Sprintf("the quantity must be between %s and %s", c.MinQuantity, c.MaxQuantity))
	}
	return nil
}

// Update is a change of an order the engine reports.
type Update struct {
	OrderID     string
	Seq         int64
	Status      Status
	Filled      decimal.Decimal
	FilledQuote decimal.Decimal
	// Reason is the cancel reason, or the reject code of a REJECTED order.
	Reason string
}

// Apply records an engine update; older ones, or ones the state machine
// forbids, change nothing.
func (o *Order) Apply(u Update, now time.Time) bool {
	if u.Seq <= o.Sequence || (o.Status != u.Status && !slices.Contains(transitions[o.Status], u.Status)) {
		return false
	}
	o.Status, o.Sequence, o.UpdatedAt = u.Status, u.Seq, now
	if u.Status != StatusRejected {
		o.Filled, o.FilledQuote = u.Filled, u.FilledQuote
	}
	switch u.Status {
	case StatusCanceled:
		o.CancelReason = u.Reason
	case StatusRejected:
		o.RejectReason = u.Reason
	}
	return true
}
