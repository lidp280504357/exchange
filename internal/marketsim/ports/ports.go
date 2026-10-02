// Package ports declares what the simulated market needs (ASTRA design
// §5.1): the platform's order and balance paths for its bots, the
// reference prices of BTC and ETH, and a store for its bots, settings and
// state.
package ports

import (
	"context"
	"errors"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketsim/domain"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

// ErrFunds is an order the bot's balance cannot fund: left for later.
var ErrFunds = errors.New("not enough funds")

// Trading is the platform's spot trading as a bot uses it: as the bot's
// user, through the same paths as anyone.
type Trading interface {
	// Pair reads the pair's rules and whether it trades.
	Pair(ctx context.Context, symbol string) (domain.Pair, error)
	// Open lists the bot's active orders on symbol.
	Open(ctx context.Context, user, symbol string) ([]domain.Order, error)
	// Limit places a GTC limit order and returns its ID; a refusal for
	// funds is ErrFunds.
	Limit(ctx context.Context, user, symbol string, side domain.Side, price, qty decimal.Decimal) (string, error)
	// Market places a market order: a buy spends quote, a sell sells qty.
	Market(ctx context.Context, user, symbol string, side domain.Side, quote, qty decimal.Decimal) error
	// Cancel asks to cancel an order; one that just finished is fine.
	Cancel(ctx context.Context, user, orderID string) error
	// CancelAll asks to cancel every active order of the bot on symbol.
	CancelAll(ctx context.Context, user, symbol string) error
	// Balances returns the bot's available SPOT balances by asset.
	Balances(ctx context.Context, user string) (map[string]decimal.Decimal, error)
}

// Prices reads the reference market's prices.
type Prices interface {
	// Reference returns symbol's reference price and whether it is fresh.
	Reference(ctx context.Context, symbol string) (decimal.Decimal, bool, error)
	// Last returns symbol's last traded price on the platform (0: none).
	Last(ctx context.Context, symbol string) (decimal.Decimal, error)
}

// Bot is one of the bot accounts.
type Bot struct {
	UserID  string
	Role    domain.Role
	Label   string
	Enabled bool
}

// Store keeps the bots, the settings and the model's state.
type Store interface {
	// Bots lists the bot accounts by label.
	Bots(ctx context.Context) ([]Bot, error)
	// AddBot registers an account as a bot of role (its label unique).
	AddBot(ctx context.Context, b Bot) error
	// Settings returns the settings and their version; false when none
	// were saved yet.
	Settings(ctx context.Context) (domain.Params, int64, bool, error)
	// SaveSettings stores new settings for actor and returns their version.
	SaveSettings(ctx context.Context, p domain.Params, actor string) (int64, error)
	// State returns the model's saved state; false when there is none.
	State(ctx context.Context) (domain.State, bool, error)
	// SaveState stores the model's state.
	SaveState(ctx context.Context, st domain.State) error
}

// Flags answers feature-flag checks.
type Flags interface {
	Enabled(key string, s flags.Subject) bool
}
