// Package ports declares what derivatives-service's application layer
// needs.
package ports

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/lidp280504357/exchange/internal/derivatives/domain"
)

// Store is the unit of work over the derivatives schema.
type Store interface {
	// Tx runs fn in one transaction with the events it emits.
	Tx(ctx context.Context, fn func(Repos) error) error
	// Read returns repositories outside any transaction.
	Read() Repos
}

// Repos groups the repositories of one transaction.
type Repos interface {
	// LockUser serializes the transactions that change a user's orders,
	// positions and margin (a transaction-scoped advisory lock).
	LockUser(ctx context.Context, userID string) error
	Settings() SettingsRepo
	Orders() OrderRepo
	Positions() PositionRepo
	Fills() FillRepo
	Pending() PendingRepo
	Contracts() ContractStateRepo
	Runs() RunRepo
	// Emit queues an event (or an engine command) on topic, keyed by
	// aggregateID.
	Emit(ctx context.Context, topic string, msg proto.Message, aggregateType, aggregateID string) error
}

// SettingsRepo stores users' settings per contract.
type SettingsRepo interface {
	// Get returns the stored settings, or nil.
	Get(ctx context.Context, userID, symbol string) (*domain.Settings, error)
	Save(ctx context.Context, s domain.Settings) error
}

// OrderRepo stores orders.
type OrderRepo interface {
	Insert(ctx context.Context, o domain.Order) error
	// Get returns the order or domain.ErrOrderNotFound.
	Get(ctx context.Context, id string) (domain.Order, error)
	// GetForUpdate is Get with a row lock.
	GetForUpdate(ctx context.Context, id string) (domain.Order, error)
	// ByClientID returns the user's order with that client_order_id, or
	// domain.ErrOrderNotFound.
	ByClientID(ctx context.Context, userID, clientOrderID string) (domain.Order, error)
	Update(ctx context.Context, o domain.Order) error
	// Active returns the user's active orders, of one symbol when symbol is
	// set.
	Active(ctx context.Context, userID, symbol string) ([]domain.Order, error)
	// CountActive counts the user's active orders on symbol and overall.
	CountActive(ctx context.Context, userID, symbol string) (onSymbol, total int, err error)
	// Unreleased returns the user's orders whose reservation is not all
	// consumed or released.
	Unreleased(ctx context.Context, userID string) ([]domain.Order, error)
	// List returns a page of the user's orders, newest first.
	List(ctx context.Context, userID string, f ListFilter) ([]domain.Order, error)
	// PendingFreeze returns orders created before cutoff whose freeze was
	// not recorded.
	PendingFreeze(ctx context.Context, cutoff time.Time, limit int) ([]domain.Order, error)
	// ToRelease returns funded orders that finished before cutoff and are
	// not released yet.
	ToRelease(ctx context.Context, cutoff time.Time, limit int) ([]domain.Order, error)
}

// ListFilter selects a page of orders.
type ListFilter struct {
	Symbol   string
	Statuses []domain.Status
	// Before is the cursor: the ID of the last order of the previous page.
	Before string
	Limit  int
}

// PositionRepo stores positions, one row per user, contract and side.
type PositionRepo interface {
	// OfUser returns the user's positions, of one symbol when symbol is
	// set, flat ones included.
	OfUser(ctx context.Context, userID, symbol string) ([]domain.Position, error)
	// Save inserts or updates a position (by user, symbol and side) and
	// returns it with its ID and version.
	Save(ctx context.Context, p domain.Position) (domain.Position, error)
	// Open returns every open position of symbol ("" for all).
	Open(ctx context.Context, symbol string) ([]domain.Position, error)
	// Totals returns, per contract, the long quantity less the short
	// quantity and the long entry cost less the short entry cost.
	Totals(ctx context.Context) (map[string]Totals, error)
}

// Totals sum a contract's positions.
type Totals struct {
	NetQty  decimal.Decimal
	NetCost decimal.Decimal
}

// RunRepo records the reconciliation runs.
type RunRepo interface {
	Record(ctx context.Context, started time.Time, check string, mismatches int, details []byte) error
}

// FillRepo stores each side of the trades.
type FillRepo interface {
	// Has reports whether the side of the trade is recorded.
	Has(ctx context.Context, tradeID string, side domain.Side) (bool, error)
	Insert(ctx context.Context, f domain.Fill) error
	// SetSettled marks a parked fill settled.
	SetSettled(ctx context.Context, tradeID string, side domain.Side) error
	// OfUser returns a page of the user's fills, newest first; before is
	// "<trade id>:<side>" of the previous page's last fill.
	OfUser(ctx context.Context, userID, symbol, before string, limit int) ([]domain.Fill, error)
}

// PendingSettlement is a ledger settlement the ledger refused.
type PendingSettlement struct {
	IdemKey    string
	UserID     string
	Request    SettleRequest
	PositionID string
	// FreezeMove is the index of a partial FREEZE whose frozen amount
	// becomes the position's margin, -1 when none.
	FreezeMove int
	// TradeID and Side name the fill it settles, if any.
	TradeID   string
	Side      domain.Side
	Attempts  int
	LastError string
	CreatedAt time.Time
}

// PendingRepo stores refused settlements until they are booked.
type PendingRepo interface {
	Insert(ctx context.Context, p PendingSettlement) error
	// Due returns up to limit pending settlements, oldest first.
	Due(ctx context.Context, limit int) ([]PendingSettlement, error)
	Failed(ctx context.Context, key, reason string) error
	Delete(ctx context.Context, key string) error
	Count(ctx context.Context) (int, error)
}

// ContractState is a contract under reduce-only.
type ContractState struct {
	Symbol     string
	ReduceOnly bool
	Reason     string
	Since      time.Time
	LiftedBy   string
}

// ContractStateRepo stores the contracts' reduce-only state.
type ContractStateRepo interface {
	Get(ctx context.Context, symbol string) (*ContractState, error)
	// Degrade sets reduce-only unless it is on already; true when it
	// changed.
	Degrade(ctx context.Context, symbol, reason string, at time.Time) (bool, error)
	// Lift ends reduce-only; true when it was on.
	Lift(ctx context.Context, symbol, by string, at time.Time) (bool, error)
	All(ctx context.Context) ([]ContractState, error)
}

// SettleRequest is a settlement step for the ledger (SettleFutures).
type SettleRequest struct {
	IdemKey   string        `json:"idem_key"`
	UserID    string        `json:"user_id"`
	Asset     string        `json:"asset"`
	Reference string        `json:"reference"`
	Moves     []domain.Move `json:"moves"`
}

// Balance is a user's FUTURES balance.
type Balance struct {
	Available decimal.Decimal
	Frozen    decimal.Decimal
}

// Ledger is ledger-service (gRPC).
type Ledger interface {
	// Freeze locks amount in the user's FUTURES account (ORDER_FREEZE); a
	// repeated key returns the first result.
	Freeze(ctx context.Context, key, userID, asset string, amount decimal.Decimal, reference string) error
	// Unfreeze releases it (ORDER_UNFREEZE).
	Unfreeze(ctx context.Context, key, userID, asset string, amount decimal.Decimal, reference string) error
	// Settle books a settlement step and returns an outcome per move.
	Settle(ctx context.Context, r SettleRequest) ([]domain.Outcome, error)
	// Balance returns the user's FUTURES balance in asset.
	Balance(ctx context.Context, userID, asset string) (Balance, error)
	// PnLClearing returns the PNL_CLEARING balance in asset.
	PnLClearing(ctx context.Context, asset string) (decimal.Decimal, error)
}

// Instruments reads contracts (instrument-service gRPC).
type Instruments interface {
	Contract(ctx context.Context, symbol string) (domain.Contract, error)
	Contracts(ctx context.Context) ([]domain.Contract, error)
}

// Eligibility asks user-service whether a user may use a feature.
type Eligibility interface {
	Check(ctx context.Context, userID, feature, symbol string) (allowed bool, reason string, err error)
}

// Mark is a contract's mark price.
type Mark struct {
	Price decimal.Decimal
	At    time.Time
}

// Marks gives the latest mark prices (market.candle.events).
type Marks interface {
	// Mark returns the contract's mark price and whether it is fresh.
	Mark(symbol string) (Mark, bool)
}
