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

// ErrOutOfBand is a limit price the platform refused as too far from the
// band's anchor.
var ErrOutOfBand = errors.New("price out of band")

// Trading is the platform's spot trading as a bot uses it: as the bot's
// user, through the same paths as anyone.
type Trading interface {
	// Pair reads the pair's rules and whether it trades.
	Pair(ctx context.Context, symbol string) (domain.Pair, error)
	// Open lists the bot's active orders on symbol.
	Open(ctx context.Context, user, symbol string) ([]domain.Order, error)
	// Limit places a GTC limit order and returns its ID; a refusal for
	// funds is ErrFunds, one for the price band ErrOutOfBand.
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
	// ErrFunds, one for the price band ErrOutOfBand.
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

// Prices reads the reference market's prices and the platform's market
// data (market-data-service).
type Prices interface {
	// Reference returns symbol's reference price and whether it is fresh;
	// for a pair no reference market follows, its own market's price (the
	// middle of its book) or the simulated price Report left.
	Reference(ctx context.Context, symbol string) (decimal.Decimal, bool, error)
	// LastTrade returns symbol's last trade on the platform: its price and
	// time (0 and zero: none).
	LastTrade(ctx context.Context, symbol string) (decimal.Decimal, time.Time, error)
	// Report gives the simulated market's target of symbol, which stands
	// in for its reference while its book has no middle.
	Report(ctx context.Context, symbol string, price decimal.Decimal) error
	// Mark returns a contract's mark and index prices (0: none yet).
	Mark(ctx context.Context, symbol string) (mark, index decimal.Decimal, err error)
}

// Bot is one of the bot accounts.
type Bot struct {
	UserID  string
	Role    domain.Role
	Label   string
	Enabled bool
}

// Sample is the target and the last price (0: none yet) at a time, for the
// operators' chart.
type Sample struct {
	At     time.Time
	Target float64
	Last   decimal.Decimal
}

// ParamChange is a change of the settings as the guards count it (ASTRA
// design §6.2): when, by whom, approved by whom, and how far it moves the
// price (a share) and the day's turnover (a logarithm).
type ParamChange struct {
	At                time.Time
	Actor, ApprovedBy string
	Move, Volume      float64
}

// Audit is an operator's action, for the audit trail (audit.events).
type Audit struct {
	Action, Target, Actor, Reason string
	// Details is a JSON object.
	Details string
}

// Pairs changes a pair's or a contract's status (instrument-service).
type Pairs interface {
	SetPairStatus(ctx context.Context, symbol, to, actor, reason string) error
	SetContractStatus(ctx context.Context, symbol, to, actor, reason string) error
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
	// SaveSettings stores new settings, the change as the guards count it
	// and the audit record when there is one, and returns their version.
	SaveSettings(ctx context.Context, p domain.Params, change ParamChange, audit *Audit) (int64, error)
	// ParamChanges returns the changes of the settings made from from to
	// to.
	ParamChanges(ctx context.Context, from, to time.Time) ([]ParamChange, error)
	// Events returns the scheduled and running events (open), or the
	// latest limit events of any status.
	Events(ctx context.Context, open bool, limit int) ([]domain.Event, error)
	// EventsStarting returns the events not canceled that start (or
	// started) from from to to.
	EventsStarting(ctx context.Context, from, to time.Time) ([]domain.Event, error)
	// SaveEvent stores an event (new or changed), with the audit record
	// when there is one.
	SaveEvent(ctx context.Context, e domain.Event, audit *Audit) error
	// SaveSample keeps a sample of the target and the last price;
	// Samples returns the ones since t, oldest first; PruneSamples drops
	// the ones before t.
	SaveSample(ctx context.Context, s Sample) error
	Samples(ctx context.Context, t time.Time) ([]Sample, error)
	PruneSamples(ctx context.Context, t time.Time) error
	// State returns the model's saved state; false when there is none.
	State(ctx context.Context) (domain.State, bool, error)
	// SaveState stores the model's state.
	SaveState(ctx context.Context, st domain.State) error
}

// Flags answers feature-flag checks.
type Flags interface {
	Enabled(key string, s flags.Subject) bool
}
