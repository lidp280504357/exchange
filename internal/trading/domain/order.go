// Package domain holds spot orders (requirements §5.6, §11.2): their
// checks against the trading pair, the funds they freeze and their state
// machine (appendix B).
package domain

import (
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

// Side of an order.
type Side string

// Sides.
const (
	SideBuy  Side = "BUY"
	SideSell Side = "SELL"
)

// Type of an order.
type Type string

// Types.
const (
	TypeLimit  Type = "LIMIT"
	TypeMarket Type = "MARKET"
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

// STP is the self-trade prevention mode.
type STP string

// Self-trade prevention modes; CancelNewest is the default (§5.6).
const (
	CancelNewest STP = "CANCEL_NEWEST"
	CancelOldest STP = "CANCEL_OLDEST"
	CancelBoth   STP = "CANCEL_BOTH"
)

// Status of an order.
type Status string

// Statuses (appendix B).
const (
	StatusNew             Status = "NEW"
	StatusOpen            Status = "OPEN"
	StatusPartiallyFilled Status = "PARTIALLY_FILLED"
	StatusFilled          Status = "FILLED"
	StatusCanceled        Status = "CANCELED"
	StatusRejected        Status = "REJECTED"
	StatusExpired         Status = "EXPIRED"
)

// ActiveStatuses are the statuses of orders that may still fill.
var ActiveStatuses = []Status{StatusNew, StatusOpen, StatusPartiallyFilled}

// transitions is the order state machine. Appendix B, plus NEW straight to
// PARTIALLY_FILLED or FILLED for orders that take liquidity on arrival.
var transitions = map[Status][]Status{
	StatusNew:             {StatusOpen, StatusPartiallyFilled, StatusFilled, StatusCanceled, StatusRejected},
	StatusOpen:            {StatusPartiallyFilled, StatusFilled, StatusCanceled, StatusExpired},
	StatusPartiallyFilled: {StatusPartiallyFilled, StatusFilled, StatusCanceled, StatusExpired},
}

// CanTransition reports whether an order may move from one status to
// another.
func CanTransition(from, to Status) bool { return slices.Contains(transitions[from], to) }

// Active reports whether the order may still fill.
func (s Status) Active() bool { return slices.Contains(ActiveStatuses, s) }

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	return s.Active() || s == StatusFilled || s == StatusCanceled || s == StatusRejected || s == StatusExpired
}

// AccountType is the account an order trades from (margin design
// 2026-10-06 §5.1): the user's SPOT account, the cross margin account, or
// the isolated margin account of the order's pair.
type AccountType string

// The account types.
const (
	AccountSpot           AccountType = "SPOT"
	AccountMarginCross    AccountType = "MARGIN_CROSS"
	AccountMarginIsolated AccountType = "MARGIN_ISOLATED"
)

// Margin reports whether the account is a margin account.
func (a AccountType) Margin() bool { return a == AccountMarginCross || a == AccountMarginIsolated }

// SideEffect is what an order on a margin account does besides trading:
// AUTO_BORROW borrows what the free balance lacks before the freeze,
// AUTO_REPAY repays the debt of what its fills bring (in the ledger's
// settlement).
type SideEffect string

// The side effects.
const (
	SideEffectNone       SideEffect = "NONE"
	SideEffectAutoBorrow SideEffect = "AUTO_BORROW"
	SideEffectAutoRepay  SideEffect = "AUTO_REPAY"
)

// Account is where an order's funds are frozen and its fills settle.
type Account struct {
	UserID string
	Type   AccountType
	// Scope is the pair of an isolated margin account, "" otherwise.
	Scope string
}

// FreezeState tracks the ledger freeze of an order (§11.1 step 5): an
// order stays PENDING between being stored and its freeze being recorded,
// so a crash in between is repaired by retrying the freeze.
type FreezeState string

// Freeze states.
const (
	FreezePending FreezeState = "PENDING"
	FreezeDone    FreezeState = "FROZEN"
	FreezeNone    FreezeState = "NONE" // rejected: nothing is frozen
)

// Order is a spot order.
type Order struct {
	ID              string
	UserID          string
	ClientOrderID   string
	Symbol          string
	Side            Side
	Type            Type
	TimeInForce     TimeInForce
	STP             STP
	Price           decimal.Decimal // LIMIT
	Quantity        decimal.Decimal // LIMIT, MARKET sells
	QuoteAmount     decimal.Decimal // MARKET buys
	Status          Status
	RejectReason    string
	FilledQuantity  decimal.Decimal
	FilledQuote     decimal.Decimal
	FrozenAsset     string
	FrozenAmount    decimal.Decimal
	FreezeState     FreezeState
	MakerFeeRate    decimal.Decimal
	TakerFeeRate    decimal.Decimal
	BaseDecimals    int32
	QuoteDecimals   int32
	ProtectionPrice decimal.Decimal // MARKET; zero when there was no anchor
	// AccountType and SideEffect: SPOT and NONE unless the order trades on
	// a margin account.
	AccountType AccountType
	SideEffect  SideEffect
	// Borrowed is what margin-service borrowed for the order before its
	// freeze (AUTO_BORROW), BorrowID that borrow; zero and "" otherwise.
	Borrowed decimal.Decimal
	BorrowID string
	// Steps and assets of the pair, handed to the engine.
	TickSize        decimal.Decimal
	LotSize         decimal.Decimal
	BaseAsset       string
	QuoteAsset      string
	CancelRequested bool
	// CancelReason says why the engine canceled the order (USER, IOC, FOK,
	// SELF_TRADE, NO_LIQUIDITY).
	CancelReason string
	// Released is set once what the finished order no longer needs is
	// unfrozen.
	Released bool
	// Sequence is the engine sequence of the last event applied.
	Sequence  int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Account returns the account the order trades from.
func (o Order) Account() Account {
	a := Account{UserID: o.UserID, Type: o.AccountType}
	if o.AccountType == AccountMarginIsolated {
		a.Scope = o.Symbol
	}
	return a
}

// Unused is what a finished order still has frozen: what it froze minus
// what its fills consumed (§11.1 step 8). A sell consumes the base it
// sold, a market buy the quote it spent, and a limit buy its limit price
// per unit filled (settlement releases the difference to the trade price
// at once).
func (o Order) Unused() decimal.Decimal {
	consumed := o.FilledQuantity
	switch {
	case o.Side == SideSell:
	case o.Type == TypeMarket:
		consumed = o.FilledQuote
	default:
		consumed = o.Price.Mul(o.FilledQuantity)
	}
	return decimal.Max(o.FrozenAmount.Sub(consumed), decimal.Zero)
}

// Terminal reports whether the order is finished.
func (s Status) Terminal() bool { return s.Valid() && !s.Active() }

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

// Apply records an engine update; updates older than the last one
// applied, or that the state machine forbids, change nothing.
func (o *Order) Apply(u Update, now time.Time) bool {
	if u.Seq <= o.Sequence || (o.Status != u.Status && !CanTransition(o.Status, u.Status)) {
		return false
	}
	o.Status, o.Sequence, o.UpdatedAt = u.Status, u.Seq, now
	if u.Status != StatusRejected {
		o.FilledQuantity, o.FilledQuote = u.Filled, u.FilledQuote
	}
	switch u.Status {
	case StatusCanceled:
		o.CancelReason = u.Reason
	case StatusRejected:
		o.RejectReason = u.Reason
	}
	return true
}

// Fill is one side of a trade, as the order's owner sees it.
type Fill struct {
	TradeID    string
	OrderID    string
	UserID     string
	Symbol     string
	Side       Side
	Maker      bool
	Price      decimal.Decimal
	Quantity   decimal.Decimal
	Quote      decimal.Decimal
	FeeAsset   string
	Fee        decimal.Decimal
	Seq        int64
	ExecutedAt time.Time
}

// SameAs reports whether a request repeating the order's client_order_id
// asks for the same order.
func (o Order) SameAs(r Request) bool {
	r = r.Defaults()
	return o.Symbol == r.Symbol && o.Side == r.Side && o.Type == r.Type && o.TimeInForce == r.TimeInForce &&
		o.STP == r.STP && o.Price.Equal(r.Price) && o.Quantity.Equal(r.Quantity) && o.QuoteAmount.Equal(r.QuoteAmount) &&
		o.AccountType == r.AccountType && o.SideEffect == r.SideEffect
}

// Errors of the order checks (appendix C).
var (
	ErrNotTrading     = apperr.New(apperr.KindUnprocessable, "INSTRUMENT_NOT_TRADING", "the pair does not accept new orders")
	ErrMinNotional    = apperr.New(apperr.KindUnprocessable, "ORDER_MIN_NOTIONAL", "the order is below the minimum notional")
	ErrOutOfBand      = apperr.New(apperr.KindUnprocessable, "ORDER_PRICE_OUT_OF_BAND", "the price is too far from the last price")
	ErrTooManyOpen    = apperr.New(apperr.KindUnprocessable, "ORDER_TOO_MANY_OPEN", "too many active orders")
	ErrAlreadyFilled  = apperr.New(apperr.KindConflict, "ORDER_ALREADY_FILLED", "the order is already filled")
	ErrOrderFinished  = apperr.New(apperr.KindConflict, apperr.CodeConflict, "the order is already finished")
	ErrOrderNotFound  = apperr.NotFound("no such order")
	ErrClientIDReused = apperr.New(apperr.KindConflict, apperr.CodeIdempotencyConflict,
		"client_order_id already names another order")
	// ErrMarginDisabled refuses an order on a margin account while
	// margin.enabled is off for the user, or one with AUTO_BORROW while
	// margin.auto_borrow is off (details flag); margin-service answers the
	// same code.
	ErrMarginDisabled = apperr.New(apperr.KindForbidden, "MARGIN_DISABLED", "margin trading is not available")
)

// Limits on active orders (§11.2).
const (
	MaxActivePerSymbol = 200
	MaxActive          = 1000
)

func precision(msg string) error {
	return apperr.New(apperr.KindInvalid, "INSTRUMENT_PRECISION", msg)
}

func quantityRange(msg string) error {
	return apperr.New(apperr.KindInvalid, "ORDER_QUANTITY_OUT_OF_RANGE", msg)
}
