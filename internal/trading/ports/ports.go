// Package ports declares what the trading application needs.
package ports

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/lidp280504357/exchange/internal/trading/domain"
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
	// Freeze locks amount of asset in the user's SPOT account for an order;
	// a repeated key returns the first result.
	Freeze(ctx context.Context, key, userID, asset string, amount decimal.Decimal, orderID string) error
}

// Instruments reads trading pairs (instrument-service gRPC).
type Instruments interface {
	Pair(ctx context.Context, symbol string) (domain.Pair, error)
}

// Eligibility asks user-service whether a user may use a feature.
type Eligibility interface {
	Check(ctx context.Context, userID, feature, symbol string) (allowed bool, reason string, err error)
}

// Prices gives the price that bands and market protection are measured
// from: the last trade, or a reference price; zero when there is none.
type Prices interface {
	Anchor(ctx context.Context, symbol string) (decimal.Decimal, error)
}
