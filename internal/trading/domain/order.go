// Package domain holds spot orders (requirements §5.6, §11.2): their
// checks against the trading pair, the funds they freeze and their state
// machine (appendix B).
package domain

import (
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
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
	CancelRequested bool
	// Sequence is the engine sequence of the last event applied.
	Sequence  int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SameAs reports whether a request repeating the order's client_order_id
// asks for the same order.
func (o Order) SameAs(r Request) bool {
	r = r.Defaults()
	return o.Symbol == r.Symbol && o.Side == r.Side && o.Type == r.Type && o.TimeInForce == r.TimeInForce &&
		o.STP == r.STP && o.Price.Equal(r.Price) && o.Quantity.Equal(r.Quantity) && o.QuoteAmount.Equal(r.QuoteAmount)
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
