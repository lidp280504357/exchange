package instruments

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	instrumentv1 "github.com/skill/exchange/api/gen/go/exchange/instrument/v1"
	"github.com/skill/exchange/internal/marketdata/ports"
)

// FuturesContracts lists the contracts of both margin types for the
// futures statistics (design 2026-10-06 §3.3): instrument-service lists
// the linear ones alone unless asked for ALL (G0 contract §1), and the
// rest of market-data-service keeps to those until it handles inverse
// contracts (batch G2). It implements ports.FuturesContracts, cached for
// ttl.
type FuturesContracts struct {
	c   instrumentv1.InstrumentServiceClient
	ttl time.Duration

	mu   sync.Mutex
	list []ports.FuturesContract
	at   time.Time
}

// NewFuturesContracts wraps an InstrumentService client.
func NewFuturesContracts(c instrumentv1.InstrumentServiceClient, ttl time.Duration) *FuturesContracts {
	return &FuturesContracts{c: c, ttl: ttl}
}

// FuturesContracts lists the contracts that are not delisted.
func (f *FuturesContracts) FuturesContracts(ctx context.Context) ([]ports.FuturesContract, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.list != nil && time.Since(f.at) < f.ttl {
		return f.list, nil
	}
	resp, err := f.c.ListContracts(ctx, &instrumentv1.ListContractsRequest{MarginType: "ALL"})
	if err != nil {
		return nil, err
	}
	list := []ports.FuturesContract{}
	for _, k := range resp.GetContracts() {
		if k.GetStatus() == "DELISTED" {
			continue
		}
		size := decimal.Zero
		if s := k.GetContractSize(); s != "" {
			if size, err = decimal.NewFromString(s); err != nil {
				return nil, fmt.Errorf("contract %s: bad contract size %q", k.GetSymbol(), s)
			}
		}
		list = append(list, ports.FuturesContract{
			Symbol: k.GetSymbol(), CoinMargined: k.GetMarginType() == "COIN", ReferenceSymbol: k.GetReferenceSymbol(), ContractSize: size,
		})
	}
	f.list, f.at = list, time.Now()
	return list, nil
}
