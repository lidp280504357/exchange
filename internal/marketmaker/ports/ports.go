// Package ports declares what HOUSE's liquidity publisher needs (ADR-0015):
// the pairs and contracts it may offer liquidity on, and what HOUSE holds.
package ports

import (
	"context"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketmaker/domain"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

// Specs lists the pairs and contracts that follow a reference market and
// are TRADING.
type Specs interface {
	Specs(ctx context.Context) ([]domain.Spec, error)
}

// House reads what HOUSE holds.
type House interface {
	// Holdings returns its spot inventory: the available balance of each
	// asset's MARKET_MAKER system account.
	Holdings(ctx context.Context) (domain.Holdings, error)
	// Positions returns its net position on each contract, long positive.
	Positions(ctx context.Context) (map[string]decimal.Decimal, error)
}

// Flags answers feature-flag checks.
type Flags interface {
	Enabled(key string, s flags.Subject) bool
}
