// Package ledger reads HOUSE's spot inventory from ledger-service over
// gRPC: the MARKET_MAKER system accounts (ADR-0013).
package ledger

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	ledgerv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/ledger/v1"
	"github.com/lidp280504357/exchange/internal/marketmaker/domain"
)

// accountMarketMaker is the system account type of HOUSE's spot inventory.
const accountMarketMaker = "MARKET_MAKER"

// Inventory implements the holdings half of ports.House.
type Inventory struct {
	Client ledgerv1.LedgerServiceClient
}

// Holdings returns the available balance of each asset's MARKET_MAKER
// account.
func (i Inventory) Holdings(ctx context.Context) (domain.Holdings, error) {
	resp, err := i.Client.GetSystemBalances(ctx, &ledgerv1.GetSystemBalancesRequest{})
	if err != nil {
		return nil, fmt.Errorf("system balances: %w", err)
	}
	out := domain.Holdings{}
	for _, b := range resp.GetBalances() {
		if b.GetAccountType() != accountMarketMaker {
			continue
		}
		v, err := decimal.NewFromString(b.GetAvailable())
		if err != nil {
			return nil, fmt.Errorf("MARKET_MAKER %s: bad balance %q", b.GetAsset(), b.GetAvailable())
		}
		out[b.GetAsset()] = v
	}
	return out, nil
}
