// Package derivatives asks derivatives-service about users' cross
// positions.
package derivatives

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	derivativesv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/derivatives/v1"
)

// Client implements ports.Futures.
type Client struct {
	c derivativesv1.DerivativesServiceClient
}

// New wraps a DerivativesService client.
func New(c derivativesv1.DerivativesServiceClient) *Client { return &Client{c: c} }

// CrossUnrealizedPnL returns the unrealized result of the user's cross
// positions.
func (c *Client) CrossUnrealizedPnL(ctx context.Context, userID, asset string) (decimal.Decimal, error) {
	resp, err := c.c.GetUnrealizedPnL(ctx, &derivativesv1.GetUnrealizedPnLRequest{UserId: userID, Asset: asset})
	if err != nil {
		return decimal.Zero, err
	}
	d, err := decimal.NewFromString(resp.GetCrossUnrealizedPnl())
	if err != nil {
		return decimal.Zero, fmt.Errorf("bad unrealized result %q", resp.GetCrossUnrealizedPnl())
	}
	return d, nil
}
