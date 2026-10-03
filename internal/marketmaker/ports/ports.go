// Package ports declares what HOUSE's liquidity publisher needs (ADR-0015):
// the pairs and contracts it may offer liquidity on, and what HOUSE holds.
package ports

import (
	"context"

	"github.com/lidp280504357/exchange/internal/marketmaker/domain"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

// Specs lists the pairs that follow a reference market (those not TRADING
// Halted) and the TRADING contracts that do.
type Specs interface {
	Specs(ctx context.Context) ([]domain.Spec, error)
	// Backed lists the assets HOUSE must hold to sell (ADR-0013): those
	// with a network to deposit or withdraw them on, the ledger's
	// definition too.
	Backed(ctx context.Context) ([]string, error)
}

// House reads what HOUSE holds.
type House interface {
	// Holdings returns its spot inventory: the available balance of each
	// asset's MARKET_MAKER system account.
	Holdings(ctx context.Context) (domain.Holdings, error)
	// Contracts returns its FUTURES account: its net position on each
	// contract, what its positions are worth at the mark prices and its
	// equity.
	Contracts(ctx context.Context) (domain.ContractAccount, error)
}

// Flags answers feature-flag checks.
type Flags interface {
	Enabled(key string, s flags.Subject) bool
}
