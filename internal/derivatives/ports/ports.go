// Package ports declares what derivatives-service's application layer
// needs.
package ports

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/skill/exchange/internal/derivatives/domain"
	"github.com/skill/exchange/internal/platform/flags"
)

// Store is the unit of work over the derivatives schema.
type Store interface {
	// Tx runs fn in one transaction with the events it emits.
	Tx(ctx context.Context, fn func(Repos) error) error
	// Read returns repositories outside any transaction.
	Read() Repos
}

// Repos groups the repositories of one transaction.
type Repos interface {
	// LockUser serializes the transactions that change a user's orders,
	// positions and margin (a transaction-scoped advisory lock).
	LockUser(ctx context.Context, userID string) error
	Settings() SettingsRepo
	Orders() OrderRepo
	Positions() PositionRepo
	Fills() FillRepo
	Pending() PendingRepo
	Contracts() ContractStateRepo
	Runs() RunRepo
	Funding() FundingRepo
	Cross() CrossRepo
	Conditionals() ConditionalRepo
	CrossLiquidations() CrossLiquidationRepo
	// Emit queues an event (or an engine command) on topic, keyed by
	// aggregateID.
	Emit(ctx context.Context, topic string, msg proto.Message, aggregateType, aggregateID string) error
}

// SettingsRepo stores users' settings per contract.
type SettingsRepo interface {
	// Get returns the stored settings, or nil.
	Get(ctx context.Context, userID, symbol string) (*domain.Settings, error)
	Save(ctx context.Context, s domain.Settings) error
}

// OrderRepo stores orders.
type OrderRepo interface {
	Insert(ctx context.Context, o domain.Order) error
	// Get returns the order or domain.ErrOrderNotFound.
	Get(ctx context.Context, id string) (domain.Order, error)
	// GetForUpdate is Get with a row lock.
	GetForUpdate(ctx context.Context, id string) (domain.Order, error)
	// ByClientID returns the user's order with that client_order_id, or
	// domain.ErrOrderNotFound.
	ByClientID(ctx context.Context, userID, clientOrderID string) (domain.Order, error)
	Update(ctx context.Context, o domain.Order) error
	// Active returns the user's active orders, of one symbol when symbol is
	// set.
	Active(ctx context.Context, userID, symbol string) ([]domain.Order, error)
	// CountActive counts the user's active orders on symbol and overall.
	CountActive(ctx context.Context, userID, symbol string) (onSymbol, total int, err error)
	// ActiveOn returns every user's active orders on the symbols (a
	// product line's, design 2026-10-07 product switches).
	ActiveOn(ctx context.Context, symbols []string) ([]domain.Order, error)
	// Unreleased returns the user's orders whose reservation is not all
	// consumed or released.
	Unreleased(ctx context.Context, userID string) ([]domain.Order, error)
	// List returns a page of the user's orders, newest first.
	List(ctx context.Context, userID string, f ListFilter) ([]domain.Order, error)
	// PendingFreeze returns orders created before cutoff whose freeze was
	// not recorded.
	PendingFreeze(ctx context.Context, cutoff time.Time, limit int) ([]domain.Order, error)
	// ToRelease returns funded orders that finished before cutoff and are
	// not released yet.
	ToRelease(ctx context.Context, cutoff time.Time, limit int) ([]domain.Order, error)
}

// ListFilter selects a page of orders.
type ListFilter struct {
	Symbol   string
	Statuses []domain.Status
	// Before is the cursor: the ID of the last order of the previous page.
	Before string
	Limit  int
}

// PositionRepo stores positions, one row per user, contract and side.
type PositionRepo interface {
	// OfUser returns the user's positions, of one symbol when symbol is
	// set, flat ones included.
	OfUser(ctx context.Context, userID, symbol string) ([]domain.Position, error)
	// Save inserts or updates a position (by user, symbol and side) and
	// returns it with its ID and version.
	Save(ctx context.Context, p domain.Position) (domain.Position, error)
	// Open returns every open position of symbol ("" for all).
	Open(ctx context.Context, symbol string) ([]domain.Position, error)
	// Listed returns the open positions of symbol ("" for all) of the
	// users users selects (the admin console's lists, review L3).
	Listed(ctx context.Context, symbol string, users UserFilter) ([]domain.Position, error)
	// Totals returns, per contract, the long quantity less the short
	// quantity and the long entry cost less the short entry cost.
	Totals(ctx context.Context) (map[string]Totals, error)
}

// UserFilter selects users for the admin console's lists (review L3: the
// real users' rows, or all but the bots' and test accounts'): only those
// of Only when it is not nil (an empty Only selects nobody), none of
// Except. At most httpx.MaxFilterUserIDs each from a GET's query string,
// httpx.MaxFilterUserIDsBody from a POST .../list's body (review C76).
type UserFilter struct {
	Only, Except []string
}

// Allows reports whether the filter selects the user (an ID's spellings
// differ only in case once parsed: compared regardless of it).
func (f UserFilter) Allows(userID string) bool {
	in := func(ids []string) bool {
		return slices.ContainsFunc(ids, func(id string) bool { return strings.EqualFold(id, userID) })
	}
	if f.Only != nil && !in(f.Only) {
		return false
	}
	return !in(f.Except)
}

// Totals sum a contract's positions.
type Totals struct {
	NetQty  decimal.Decimal
	NetCost decimal.Decimal
	// LongQty is the open interest; Positions counts the open positions.
	LongQty   decimal.Decimal
	Positions int
}

// FundingRepo stores the funding rounds and payments.
type FundingRepo interface {
	// Round returns the round, or nil.
	Round(ctx context.Context, symbol string, at time.Time) (*domain.FundingRound, error)
	// Snapshot stores a round with the positions held at its time.
	Snapshot(ctx context.Context, r domain.FundingRound, payments []domain.FundingPayment) error
	// Waiting returns the rounds waiting for their rate, oldest first.
	Waiting(ctx context.Context) ([]domain.FundingRound, error)
	// SetRate records a round's rate and mark price.
	SetRate(ctx context.Context, symbol string, at time.Time, rate, mark decimal.Decimal) error
	// Unsettled returns a round's payments not settled yet.
	Unsettled(ctx context.Context, symbol string, at time.Time) ([]domain.FundingPayment, error)
	// Settle records a settled payment.
	Settle(ctx context.Context, p domain.FundingPayment) error
	// Finish sets a round SETTLED or SKIPPED.
	Finish(ctx context.Context, symbol string, at time.Time, status string) error
	// OfUser returns a page of the user's settled payments, newest first;
	// before is "<unix time>:<position id>" of the previous page's last.
	OfUser(ctx context.Context, userID, symbol, before string, limit int) ([]domain.FundingPayment, error)
}

// FundingRates reads the settled funding rates (market-data-service).
type FundingRates interface {
	// Rate returns the rate and mark price a contract's period ending at
	// settled at; found is false while it is not settled.
	Rate(ctx context.Context, symbol string, at time.Time) (rate, mark decimal.Decimal, found bool, err error)
}

// ConditionalRepo stores take-profit and stop-loss orders.
type ConditionalRepo interface {
	Insert(ctx context.Context, c domain.Conditional) error
	// Get returns the conditional order, or domain.ErrOrderNotFound.
	Get(ctx context.Context, id string) (domain.Conditional, error)
	// Update ends an active conditional order: its new status, reason
	// and order. domain.ErrConditionalEnded when it is no longer active,
	// so that an end never overwrites another (review C69).
	Update(ctx context.Context, c domain.Conditional) error
	// Active returns the active conditional orders of symbol ("" for all).
	Active(ctx context.Context, symbol string) ([]domain.Conditional, error)
	// ActiveOn returns the active conditional orders on the symbols (a
	// product line's).
	ActiveOn(ctx context.Context, symbols []string) ([]domain.Conditional, error)
	// OfUser returns a page of the user's conditional orders, newest first,
	// of one status when status is set.
	OfUser(ctx context.Context, userID, symbol, status, before string, limit int) ([]domain.Conditional, error)
}

// CrossRepo stores the warnings of cross accounts.
type CrossRepo interface {
	// WarnedAt returns when the user's cross account in asset was warned,
	// zero when it is not (a user has one cross account per settlement
	// asset, coin-M design §2.3).
	WarnedAt(ctx context.Context, userID, asset string) (time.Time, error)
	SetWarnedAt(ctx context.Context, userID, asset string, at time.Time) error
	// Warned returns the cross accounts warned now, with when.
	Warned(ctx context.Context) (map[CrossAccount]time.Time, error)
}

// CrossAccount is a user's cross account in one settlement asset.
type CrossAccount struct {
	UserID string
	Asset  string
}

// RunRepo records the reconciliation runs.
type RunRepo interface {
	Record(ctx context.Context, started time.Time, check string, mismatches int, details []byte) error
}

// FillRepo stores each side of the trades.
type FillRepo interface {
	// Has reports whether the side of the trade is recorded, or was: the
	// retention keeps the keys of the fills it deleted (M1).
	Has(ctx context.Context, tradeID string, side domain.Side) (bool, error)
	Insert(ctx context.Context, f domain.Fill) error
	// SetSettled marks a parked fill settled.
	SetSettled(ctx context.Context, tradeID string, side domain.Side) error
	// Get returns the side of the trade, or domain.ErrOrderNotFound.
	Get(ctx context.Context, tradeID string, side domain.Side) (domain.Fill, error)
	// OfUser returns a page of the user's fills, newest first; before is
	// "<trade id>:<side>" of the previous page's last fill.
	OfUser(ctx context.Context, userID, symbol, before string, limit int) ([]domain.Fill, error)
	// LastPrice returns the price of the contract's latest fill, zero when
	// there is none.
	LastPrice(ctx context.Context, symbol string) (decimal.Decimal, error)
}

// PendingSettlement is a ledger settlement the ledger refused.
type PendingSettlement struct {
	IdemKey    string
	UserID     string
	Request    SettleRequest
	PositionID string
	// FreezeMove is the index of a partial FREEZE whose frozen amount
	// becomes the position's margin, -1 when none.
	FreezeMove int
	// TradeID and Side name the fill it settles, if any.
	TradeID   string
	Side      domain.Side
	Attempts  int
	LastError string
	CreatedAt time.Time
}

// PendingRepo stores refused settlements until they are booked.
type PendingRepo interface {
	Insert(ctx context.Context, p PendingSettlement) error
	// Due returns up to limit pending settlements, oldest first.
	Due(ctx context.Context, limit int) ([]PendingSettlement, error)
	Failed(ctx context.Context, key, reason string) error
	Delete(ctx context.Context, key string) error
	Count(ctx context.Context) (int, error)
	// CountOf counts the user's.
	CountOf(ctx context.Context, userID string) (int, error)
}

// CrossLiquidationRepo stores the cross accounts' liquidations (C68).
type CrossLiquidationRepo interface {
	// Open returns the user's open liquidation of the cross account in
	// asset, or nil.
	Open(ctx context.Context, userID, asset string) (*domain.CrossLiquidation, error)
	Insert(ctx context.Context, l domain.CrossLiquidation) error
	// AddFlow adds a fill's flow to an open one.
	AddFlow(ctx context.Context, id string, flow decimal.Decimal) error
	// Update stores its fee, status and end.
	Update(ctx context.Context, l domain.CrossLiquidation) error
	// AllOpen returns the open ones, oldest first.
	AllOpen(ctx context.Context) ([]domain.CrossLiquidation, error)
}

// ContractState is a contract under reduce-only.
type ContractState struct {
	Symbol     string
	ReduceOnly bool
	Reason     string
	Since      time.Time
	LiftedBy   string
}

// ContractStateRepo stores the contracts' reduce-only state.
type ContractStateRepo interface {
	Get(ctx context.Context, symbol string) (*ContractState, error)
	// Degrade sets reduce-only unless it is on already; true when it
	// changed.
	Degrade(ctx context.Context, symbol, reason string, at time.Time) (bool, error)
	// Lift ends reduce-only; true when it was on.
	Lift(ctx context.Context, symbol, by string, at time.Time) (bool, error)
	All(ctx context.Context) ([]ContractState, error)
}

// SettleRequest is a settlement step for the ledger (SettleFutures).
type SettleRequest struct {
	IdemKey   string        `json:"idem_key"`
	UserID    string        `json:"user_id"`
	Asset     string        `json:"asset"`
	Reference string        `json:"reference"`
	Moves     []domain.Move `json:"moves"`
}

// Balance is a user's FUTURES balance.
type Balance struct {
	Available decimal.Decimal
	Frozen    decimal.Decimal
}

// Ledger is ledger-service (gRPC).
type Ledger interface {
	// Freeze locks amount in the user's FUTURES account (ORDER_FREEZE); a
	// repeated key returns the first result.
	Freeze(ctx context.Context, key, userID, asset string, amount decimal.Decimal, reference string) error
	// Unfreeze releases it (ORDER_UNFREEZE).
	Unfreeze(ctx context.Context, key, userID, asset string, amount decimal.Decimal, reference string) error
	// Settle books a settlement step and returns an outcome per move.
	Settle(ctx context.Context, r SettleRequest) ([]domain.Outcome, error)
	// Balance returns the user's FUTURES balance in asset.
	Balance(ctx context.Context, userID, asset string) (Balance, error)
	// PnLClearing returns the PNL_CLEARING balance in asset.
	PnLClearing(ctx context.Context, asset string) (decimal.Decimal, error)
}

// Instruments reads contracts (instrument-service gRPC).
type Instruments interface {
	Contract(ctx context.Context, symbol string) (domain.Contract, error)
	Contracts(ctx context.Context) ([]domain.Contract, error)
}

// Eligibility asks user-service whether a user may use a feature.
type Eligibility interface {
	Check(ctx context.Context, userID, feature, symbol string) (allowed bool, reason string, err error)
}

// Mark is a contract's mark price.
type Mark struct {
	Price decimal.Decimal
	At    time.Time
}

// Marks gives the latest mark prices (market.candle.events).
type Marks interface {
	// Mark returns the contract's mark price and whether it is fresh.
	Mark(symbol string) (Mark, bool)
}

// Features answers feature-flag checks.
type Features interface {
	Enabled(key string, s flags.Subject) bool
	// Closed reports whether an operator closed the product line of key
	// (flags.Client.Closed: its flag stored and off).
	Closed(key string) bool
	// Get returns the local copy of a flag: a closed product line's
	// UpdatedAt is when it closed.
	Get(key string) (flags.Flag, bool)
	// Refresh reloads the flags: closing a product line reads its flag
	// before canceling its orders.
	Refresh(ctx context.Context) error
}
