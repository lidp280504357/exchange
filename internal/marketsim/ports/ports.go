// Package ports declares what the simulated market needs (ASTRA design
// §5.1): the platform's order and balance paths for its bots, the
// reference prices of BTC and ETH, and a store for its bots, settings and
// state.
package ports

import (
	"context"
	"errors"
	"time"

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

// Derivatives is the platform's contract trading as a bot uses it (one-way
// positions, the contract's default settings).
type Derivatives interface {
	// Contract reads the contract's rules and whether it trades.
	Contract(ctx context.Context, symbol string) (domain.Pair, error)
	// OpenContract lists the bot's active orders on the contract.
	OpenContract(ctx context.Context, user, symbol string) ([]domain.Order, error)
	// LimitContract places a GTC limit order; a refusal for margin is
	// ErrFunds.
	LimitContract(ctx context.Context, user, symbol string, side domain.Side, price, qty decimal.Decimal) (string, error)
	// MarketContract places a market order of qty, reducing the position
	// only when reduceOnly.
	MarketContract(ctx context.Context, user, symbol string, side domain.Side, qty decimal.Decimal, reduceOnly bool) error
	// CancelContract asks to cancel an order; one that just finished is fine.
	CancelContract(ctx context.Context, user, orderID string) error
	// CancelAllContract asks to cancel every active order of the bot on the
	// contract.
	CancelAllContract(ctx context.Context, user, symbol string) error
	// Position returns the bot's signed position on the contract (long
	// positive).
	Position(ctx context.Context, user, symbol string) (decimal.Decimal, error)
	// Futures returns the bot's available FUTURES balance.
	Futures(ctx context.Context, user string) (decimal.Decimal, error)
	// ToFutures moves amount of USDT from the bot's SPOT account to its
	// FUTURES account; key makes a retry safe.
	ToFutures(ctx context.Context, user string, amount decimal.Decimal, key string) error
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

// Audit is an operator's action, for the audit trail (audit.events).
type Audit struct {
	Action, Target, Actor, Reason string
	// Details is a JSON object.
	Details string
}

// Pairs changes a pair's status (instrument-service).
type Pairs interface {
	SetPairStatus(ctx context.Context, symbol, to, actor, reason string) error
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
	// SaveSettings stores new settings for actor and returns their
	// version, with the audit record when there is one.
	SaveSettings(ctx context.Context, p domain.Params, actor string, audit *Audit) (int64, error)
	// Events returns the scheduled and running events (open), or the
	// latest limit events of any status.
	Events(ctx context.Context, open bool, limit int) ([]domain.Event, error)
	// EventsSince returns the events created since t.
	EventsSince(ctx context.Context, t time.Time) ([]domain.Event, error)
	// SaveEvent stores an event (new or changed), with the audit record
	// when there is one.
	SaveEvent(ctx context.Context, e domain.Event, audit *Audit) error
	// State returns the model's saved state; false when there is none.
	State(ctx context.Context) (domain.State, bool, error)
	// SaveState stores the model's state.
	SaveState(ctx context.Context, st domain.State) error
}

// Flags answers feature-flag checks.
type Flags interface {
	Enabled(key string, s flags.Subject) bool
}
