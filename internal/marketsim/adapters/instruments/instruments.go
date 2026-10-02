// Package instruments changes a pair's status through instrument-service's
// gRPC API, for the operators' halts (ASTRA design §6.2).
package instruments

import (
	"context"
	"fmt"

	instrumentv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/instrument/v1"
)

// Client implements ports.Pairs.
type Client struct {
	API instrumentv1.InstrumentServiceClient
}

// SetPairStatus moves symbol to the status to.
func (c Client) SetPairStatus(ctx context.Context, symbol, to, actor, reason string) error {
	if _, err := c.API.SetPairStatus(ctx, &instrumentv1.SetPairStatusRequest{Symbol: symbol, ToStatus: to, Actor: actor, Reason: reason}); err != nil {
		return fmt.Errorf("pair %s to %s: %w", symbol, to, err)
	}
	return nil
}
