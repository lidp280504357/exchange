// Package prices provides the anchor price of the price band (§11.2).
package prices

import (
	"context"

	"github.com/shopspring/decimal"
)

// None has no anchor yet: until trades (task 3) and reference prices
// (task 7 of plan §6.3) exist, limit prices are not banded and market
// orders carry no protection price.
type None struct{}

// Anchor returns zero.
func (None) Anchor(context.Context, string) (decimal.Decimal, error) { return decimal.Zero, nil }
