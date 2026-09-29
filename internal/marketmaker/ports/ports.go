// Package ports declares what the market maker needs. It trades like any
// user (§11.10): through the order API and the ledger, as its own account.
// Spot pairs and perpetual contracts have an Orders, References and Pairs
// each: a contract's reference is its mark price, its "pair" the contract.
package ports

import (
	"context"
	"errors"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketmaker/domain"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

// Order is one of the market maker's active orders.
type Order struct {
	ID              string
	Side            string
	Price           decimal.Decimal
	CancelRequested bool
}

// Key identifies the order by side and price, as domain.Quote.Key.
func (o Order) Key() string { return o.Side + "@" + o.Price.String() }

// ErrPaused is returned by Place when the market takes no opening orders
// for now (a contract under reduce-only): every quote comes off until it
// does.
var ErrPaused = errors.New("the market takes no opening orders")

// Orders is the order API, acting as the market maker's account.
type Orders interface {
	Open(ctx context.Context, symbol string) ([]Order, error)
	Place(ctx context.Context, symbol string, q domain.Quote) error
	Cancel(ctx context.Context, orderID string) error
	CancelAll(ctx context.Context, symbol string) error
}

// Balance is an asset of the market maker's spot account.
type Balance struct {
	Available decimal.Decimal
	Frozen    decimal.Decimal
}

// Balances reads the market maker's spot account.
type Balances interface {
	Spot(ctx context.Context) (map[string]Balance, error)
}

// Positions reads the market maker's contract positions.
type Positions interface {
	// Net returns its position on a contract: long positive, short
	// negative, both sides together in hedge mode.
	Net(ctx context.Context, symbol string) (decimal.Decimal, error)
}

// References gives the reference price and whether it is fresh.
type References interface {
	Price(ctx context.Context, symbol string) (decimal.Decimal, bool, error)
}

// PairInfo is a trading pair as quoting needs it.
type PairInfo struct {
	Status   string
	Base     string
	Quote    string
	TickSize decimal.Decimal
	LotSize  decimal.Decimal
}

// Pairs reads trading pairs.
type Pairs interface {
	Pair(ctx context.Context, symbol string) (PairInfo, error)
}

// Flags answers feature-flag checks.
type Flags interface {
	Enabled(key string, s flags.Subject) bool
}
