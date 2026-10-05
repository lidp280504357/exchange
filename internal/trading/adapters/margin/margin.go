// Package margin asks margin-service about orders on margin accounts.
package margin

import (
	"context"

	"github.com/shopspring/decimal"

	marginv1 "github.com/skill/exchange/api/gen/go/exchange/margin/v1"
	orderv1 "github.com/skill/exchange/api/gen/go/exchange/order/v1"
	"github.com/skill/exchange/internal/trading/domain"
)

// Client implements ports.Margin.
type Client struct{ c marginv1.MarginServiceClient }

// New wraps a MarginService client.
func New(c marginv1.MarginServiceClient) *Client { return &Client{c: c} }

// ReserveOrder checks the order against its margin account and, with
// AUTO_BORROW, borrows what the free balance lacks (idempotent by the
// order's ID). margin-service's refusals come back as their codes, with
// their details.
func (c *Client) ReserveOrder(ctx context.Context, o domain.Order) error {
	_, err := c.c.ReserveOrder(ctx, &marginv1.ReserveOrderRequest{Order: Check(o)})
	return err
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
