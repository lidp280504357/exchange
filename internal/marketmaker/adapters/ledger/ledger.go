// Package ledger reads HOUSE's spot inventory from ledger-service over
// gRPC: the MARKET_MAKER system accounts (ADR-0013).
package ledger

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	ledgerv1 "github.com/skill/exchange/api/gen/go/exchange/ledger/v1"
	"github.com/skill/exchange/internal/marketmaker/domain"
)

// accountMarketMaker is the system account type of HOUSE's spot inventory.
const accountMarketMaker = "MARKET_MAKER"

// Inventory implements the holdings half of ports.House.
type Inventory struct {
	Client ledgerv1.LedgerServiceClient
}

// Holdings returns the available balance of each asset's MARKET_MAKER
// account, less what HOUSE owes in trades the ledger parked as FAILED: it
// is not off the balance yet, and quoting on it would sell it twice.
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
		if p := b.GetParked(); p != "" {
			owed, err := decimal.NewFromString(p)
			if err != nil {
				return nil, fmt.Errorf("MARKET_MAKER %s: bad parked %q", b.GetAsset(), p)
			}
			v = v.Sub(owed)
		}
		out[b.GetAsset()] = v
	}
	return out, nil
}
