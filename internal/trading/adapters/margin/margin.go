// Package margin asks margin-service about orders on margin accounts.
package margin

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	marginv1 "github.com/skill/exchange/api/gen/go/exchange/margin/v1"
	orderv1 "github.com/skill/exchange/api/gen/go/exchange/order/v1"
	"github.com/skill/exchange/internal/trading/domain"
	"github.com/skill/exchange/internal/trading/ports"
)

// Client implements ports.Margin.
type Client struct{ c marginv1.MarginServiceClient }

// New wraps a MarginService client.
func New(c marginv1.MarginServiceClient) *Client { return &Client{c: c} }

// ReserveOrder checks the order against its margin account and, with
// AUTO_BORROW, borrows what the free balance lacks (idempotent by the
// order's ID), and returns what it borrowed. margin-service's refusals
// come back as their codes, with their details. A margin-service that does
// not serve the call yet refuses the order as MARGIN_DISABLED: left
// pending, it would go to the engine whenever the call arrived.
func (c *Client) ReserveOrder(ctx context.Context, o domain.Order) (ports.Reservation, error) {
	resp, err := c.c.ReserveOrder(ctx, &marginv1.ReserveOrderRequest{Order: Check(o)})
	if status.Code(err) == codes.Unimplemented {
		return ports.Reservation{}, domain.ErrMarginDisabled
	}
	if err != nil {
		return ports.Reservation{}, err
	}
	r := ports.Reservation{Borrowed: decimal.Zero, BorrowID: resp.GetBorrowId()}
	if b := resp.GetBorrowed(); b != "" {
		if r.Borrowed, err = decimal.NewFromString(b); err != nil {
			return ports.Reservation{}, fmt.Errorf("margin-service borrowed %q for order %s: %w", b, o.ID, err)
		}
	}
	return r, nil
}

// Check is the order as margin-service checks it: what it freezes, on
// which account, and its price and quantity for the margin level after it
// filled (empty for a market order's price and a market buy's quantity).
func Check(o domain.Order) *marginv1.OrderCheck {
	side := orderv1.Side_SIDE_BUY
	if o.Side == domain.SideSell {
		side = orderv1.Side_SIDE_SELL
	}
	return &marginv1.OrderCheck{
		UserId: o.UserID, OrderId: o.ID, AccountType: string(o.AccountType), Symbol: o.Symbol, OrderSide: side,
		FreezeAsset: o.FrozenAsset, FreezeAmount: o.FrozenAmount.String(), SideEffect: string(o.SideEffect),
		Price: amount(o.Price), Quantity: amount(o.Quantity),
	}
}

// amount writes a decimal, or "" for an absent (zero) one.
func amount(d decimal.Decimal) string {
	if d.IsZero() {
		return ""
	}
	return d.String()
}
