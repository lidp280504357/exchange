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

	"github.com/skill/exchange/internal/marketsim/domain"
	"github.com/skill/exchange/internal/platform/flags"
)

// ErrFunds is an order the bot's balance cannot fund: left for later.
var ErrFunds = errors.New("not enough funds")

// ErrOutOfBand is a limit price the platform refused as too far from the
// band's anchor.
var ErrOutOfBand = errors.New("price out of band")

// Refused reports whether err is the platform refusing a request outright
// (the funds, the price band, another answer of 4xx), which did nothing:
// its rate token goes back. A request that failed on the way or on the
// server may have done its work, and keeps its token.
func Refused(err error) bool {
	if errors.Is(err, ErrFunds) || errors.Is(err, ErrOutOfBand) {
		return true
	}
	var r interface{ Refused() bool }
	return errors.As(err, &r) && r.Refused()
}

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
// positions, the contract's default settings), linear contracts settled in
// USDT and coin-margined ones settled in their coin (quantities in whole
// contracts).
type Derivatives interface {
	// Contract reads the contract's rules, its face value and settlement
	// asset, and whether it trades.
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
	// Futures returns the bot's available FUTURES balance in asset (a
	// contract's settlement asset).
	Futures(ctx context.Context, user, asset string) (decimal.Decimal, error)
	// ToFutures moves amount of asset from the bot's SPOT account to its
	// FUTURES account; key makes a retry safe.
	ToFutures(ctx context.Context, user, asset string, amount decimal.Decimal, key string) error
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
	// State, when set, is the model's state saved with the settings in
	// one transaction: a re-anchoring's new P0 goes with the state it
	// was rebased to (a restart between the two would move the price).
	State *domain.State
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
	// when there is one; SaveEvents several at once (a target and its
	// spikes), all or none.
	SaveEvent(ctx context.Context, e domain.Event, audit *Audit) error
	SaveEvents(ctx context.Context, es []domain.Event, audit *Audit) error
	// SaveEventsEach stores events in one transaction with an audit
	// record each (the overlays one request makes). A second open
	// overlay on a pair is ErrOverlayOpen.
	SaveEventsEach(ctx context.Context, es []domain.Event, audits []Audit) error
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

// ErrOverlayOpen is a new overlay on a pair that has one scheduled or
// running.
var ErrOverlayOpen = errors.New("an overlay event is open on the pair")

// Followed is a pair's reference price as market-data has it (design
// 2026-10-07, general price control): whether a reference market follows
// the pair (an overlay can move it), the reference market's own price,
// the price shown with the overlay's factor on it, the factor, and
// whether the price is fresh.
type Followed struct {
	Followed bool
	Source   decimal.Decimal
	Shown    decimal.Decimal
	Factor   decimal.Decimal
	Fresh    bool
}

// OverlayPush is one push of an event's factor on a pair (J0 contract
// §2.1): it holds until Until; Seq orders an event's pushes; EndsAt is
// when the event is to be back at 1 (MarketOverlayStuck, review GD).
type OverlayPush struct {
	Factor  decimal.Decimal
	Until   time.Time
	Risk    bool
	EventID string
	Seq     int64
	EndsAt  time.Time
}

// Overlays is market-data's side of the overlays.
type Overlays interface {
	// Followed reads a pair's reference price.
	Followed(ctx context.Context, symbol string) (Followed, error)
	// Push sets a pair's factor; Clear takes it back to 1 at once.
	Push(ctx context.Context, symbol string, p OverlayPush) error
	Clear(ctx context.Context, symbol string) error
}

// Rooms is how much HOUSE may still buy and sell of a pair or a contract
// (its base asset, or contracts), what one unit is worth in USDT, and
// whether the unit is an inverse contract's (a fixed face in its coin).
type Rooms struct {
	Buy, Sell decimal.Decimal
	UnitValue decimal.Decimal
	Inverse   bool
}

// House reads HOUSE's rooms (market-maker): false while HOUSE does not
// quote the symbol.
type House interface {
	Rooms(ctx context.Context, symbol string) (Rooms, bool, error)
}

// Flags answers feature-flag checks.
type Flags interface {
	Enabled(key string, s flags.Subject) bool
}
