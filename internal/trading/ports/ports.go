// Package ports declares what the trading application needs.
package ports

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/trading/domain"
)

// Store is the unit of work over the trading schema.
type Store interface {
	// Tx runs fn in one transaction with the events it emits.
	Tx(ctx context.Context, fn func(Repos) error) error
	// Read returns repositories outside any transaction.
	Read() Repos
}

// Repos groups the repositories of one transaction.
type Repos interface {
	Orders() OrderRepo
	Fills() FillRepo
	// Emit queues an event (or a command) on topic, keyed by aggregateID.
	Emit(ctx context.Context, topic string, msg proto.Message, aggregateType, aggregateID string) error
}

// OrderRepo stores orders.
type OrderRepo interface {
	// LockUser serializes the transactions that place orders for a user,
	// so that the active order limits hold.
	LockUser(ctx context.Context, userID string) error
	Insert(ctx context.Context, o domain.Order) error
	// Get returns the order or domain.ErrOrderNotFound.
	Get(ctx context.Context, id string) (domain.Order, error)
	// GetForUpdate is Get with a row lock.
	GetForUpdate(ctx context.Context, id string) (domain.Order, error)
	// ByClientID returns the user's order with that client_order_id, or
	// domain.ErrOrderNotFound.
	ByClientID(ctx context.Context, userID, clientOrderID string) (domain.Order, error)
	Update(ctx context.Context, o domain.Order) error
	// CountActive counts the user's active orders on symbol and overall.
	CountActive(ctx context.Context, userID, symbol string) (onSymbol, total int, err error)
	// Active locks and returns the user's active orders, of one symbol when
	// symbol is set.
	Active(ctx context.Context, userID, symbol string) ([]domain.Order, error)
	// List returns a page of the user's orders, newest first.
	List(ctx context.Context, userID string, f ListFilter) ([]domain.Order, error)
	// PendingFreeze returns orders created before cutoff whose freeze was
	// not recorded.
	PendingFreeze(ctx context.Context, cutoff time.Time, limit int) ([]domain.Order, error)
	// PendingStats counts the orders whose freeze was not recorded and
	// returns when the oldest was created.
	PendingStats(ctx context.Context) (int, time.Time, error)
	// Unreleased returns funded orders that finished before cutoff and
	// whose unused funds are not released yet.
	Unreleased(ctx context.Context, cutoff time.Time, limit int) ([]domain.Order, error)
}

// FillRepo stores each side of the trades.
type FillRepo interface {
	// Insert stores a fill once; a repeated one is ignored.
	Insert(ctx context.Context, f domain.Fill) error
	OfOrder(ctx context.Context, orderID string) ([]domain.Fill, error)
	// OfUser returns a page of the user's fills, newest first; before is
	// the trade ID of the previous page's last fill.
	OfUser(ctx context.Context, userID, symbol, before string, limit int) ([]domain.Fill, error)
	// LastTrade returns the price and time of the symbol's latest trade,
	// zero when it has none.
	LastTrade(ctx context.Context, symbol string) (decimal.Decimal, time.Time, error)
}

// ListFilter selects a page of orders.
type ListFilter struct {
	Symbol   string
	Statuses []domain.Status
	// Before is the cursor: the ID of the last order of the previous page.
	Before string
	Limit  int
}

// Ledger freezes funds (ledger-service gRPC).
type Ledger interface {
	// Freeze locks amount of asset in the order's account (SPOT, or a
	// margin account); a repeated key returns the first result.
	Freeze(ctx context.Context, key string, account domain.Account, asset string, amount decimal.Decimal, orderID string) error
	// Unfreeze releases what a finished order no longer needs; a repeated
	// key returns the first result.
	Unfreeze(ctx context.Context, key string, account domain.Account, asset string, amount decimal.Decimal, orderID string) error
}

// Margin checks an order on a margin account before its freeze
// (margin-service gRPC, margin design 2026-10-06 §5.1): the account's
// state, its assets and margin level, and, with AUTO_BORROW, borrows what
// the free balance lacks. Idempotent by the order's ID: a repeat returns
// the first outcome.
type Margin interface {
	ReserveOrder(ctx context.Context, o domain.Order) (Reservation, error)
}

// Reservation is margin-service's outcome for an order: what it borrowed
// of the order's frozen asset (zero when the free balance covered it) and
// the borrow's ID.
type Reservation struct {
	Borrowed decimal.Decimal
	BorrowID string
}

// Instruments reads trading pairs (instrument-service gRPC).
type Instruments interface {
	Pair(ctx context.Context, symbol string) (domain.Pair, error)
}

// Features answers feature-flag checks.
type Features interface {
	Enabled(key string, s flags.Subject) bool
}

// Eligibility asks user-service whether a user may use a feature.
type Eligibility interface {
	Check(ctx context.Context, userID, feature, symbol string) (allowed bool, reason string, err error)
}

// Prices gives the price that bands and market protection are measured
// from: the reference price of a pair that follows a reference market
// (followed), else the last trade; zero when there is none.
type Prices interface {
	Anchor(ctx context.Context, symbol string, followed bool) (decimal.Decimal, error)
}
