// Package ports declares what the admin console's application layer
// needs: its own storage and the services it acts on.
package ports

import (
	"context"
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

// Store is the unit of work over the admin schema.
type Store interface {
	Tx(ctx context.Context, fn func(Repos) error) error
	Read() Repos
}

// Repos groups the repositories of one transaction.
type Repos interface {
	Admins() AdminRepo
	Sessions() SessionRepo
	Approvals() ApprovalRepo
	Settings() SettingsRepo
	Notes() NoteRepo
	Tags() TagRepo
	// Audit queues an administrator's action on audit.events.
	Audit(ctx context.Context, msg proto.Message, actor string) error
}

// NoteRepo stores administrators' notes on accounts.
type NoteRepo interface {
	Insert(ctx context.Context, n domain.Note) error
	// List returns an account's notes, newest first, with their authors'
	// emails, after the one created at afterTime with ID afterID (zero for
	// the newest).
	List(ctx context.Context, userID string, afterTime time.Time, afterID string, limit int) ([]domain.Note, error)
}

// TagRepo stores accounts' tags.
type TagRepo interface {
	// Of returns the tags of each account (absent: none).
	Of(ctx context.Context, userIDs []string) (map[string][]string, error)
	// Set replaces an account's tags.
	Set(ctx context.Context, userID string, tags []string, adminID string, now time.Time) error
}

// SettingsRepo stores the console's settings (one row).
type SettingsRepo interface {
	// Get returns the settings, or nil while nobody changed them.
	Get(ctx context.Context) (*domain.Settings, error)
	Put(ctx context.Context, s domain.Settings) error
}

// AdminRepo stores administrators.
type AdminRepo interface {
	Insert(ctx context.Context, a domain.Admin) error
	Update(ctx context.Context, a domain.Admin) error
	// ByEmail returns the administrator with email, or nil.
	ByEmail(ctx context.Context, email string) (*domain.Admin, error)
	// ByEmailForUpdate is ByEmail with the row locked.
	ByEmailForUpdate(ctx context.Context, email string) (*domain.Admin, error)
	// Get returns an administrator, or nil.
	Get(ctx context.Context, id string) (*domain.Admin, error)
	// GetForUpdate is Get with the row locked.
	GetForUpdate(ctx context.Context, id string) (*domain.Admin, error)
	List(ctx context.Context) ([]domain.Admin, error)
}

// SessionRepo stores sessions by token hash.
type SessionRepo interface {
	Insert(ctx context.Context, s domain.Session) error
	// Get returns a session that is not revoked, or nil.
	Get(ctx context.Context, hash []byte) (*domain.Session, error)
	Touch(ctx context.Context, hash []byte, now time.Time) error
	Revoke(ctx context.Context, hash []byte, now time.Time) error
	// RevokeAll ends every session of an administrator.
	RevokeAll(ctx context.Context, adminID string, now time.Time) error
}

// ApprovalRepo stores fund operations: two-person requests and
// single-person operations.
type ApprovalRepo interface {
	Insert(ctx context.Context, a domain.Approval) error
	Update(ctx context.Context, a domain.Approval) error
	GetForUpdate(ctx context.Context, id string) (*domain.Approval, error)
	// Get returns one with the administrators' emails, or nil.
	Get(ctx context.Context, id string) (*domain.Approval, error)
	// List returns up to limit requests in a status ("": all), newest
	// first, after the one created at afterTime with ID afterID (zero for
	// the newest), with the administrators' emails.
	List(ctx context.Context, status string, afterTime time.Time, afterID string, limit int) ([]domain.Approval, error)
	// SingleUsage sums the worth of an administrator's single-person
	// operations requested since then that were not refused (pending ones
	// count: their outcome may be booked).
	SingleUsage(ctx context.Context, adminID string, since time.Time) (decimal.Decimal, error)
	// CountPending counts the requests waiting for a decision.
	CountPending(ctx context.Context) (int, error)
}

// User is an account as the console shows it.
type User struct {
	ID        string
	Status    string
	Region    string
	Language  string
	Timezone  string
	KYCLevel  int32
	CreatedAt time.Time
	// Tags are the console's tags on it (filled by the console).
	Tags []string
}

// Balance is one of a user's balances.
type Balance struct {
	AccountType string
	Asset       string
	Available   string
	Frozen      string
}

// UserQuery selects accounts; empty fields match everything.
type UserQuery struct {
	Status        string
	Region        string
	CreatedFrom   time.Time
	CreatedBefore time.Time
	Cursor        string
	Limit         int
}

// UserStats are the overview's account counts.
type UserStats struct {
	Total        int64
	CreatedSince int64
	// Days maps a UTC day (YYYY-MM-DD) to its new accounts.
	Days map[string]int64
}

// Users reads and changes accounts (auth-, user- and ledger-service).
type Users interface {
	// Find returns the user of an email address or phone number.
	Find(ctx context.Context, identifier string) (string, error)
	Get(ctx context.Context, userID string) (User, error)
	Balances(ctx context.Context, userID string) ([]Balance, error)
	// ChangeStatus returns the previous status.
	ChangeStatus(ctx context.Context, userID, to, reason, actor, note string) (string, error)
	// List returns a page of accounts, newest first, and the cursor of
	// the next ("" on the last).
	List(ctx context.Context, q UserQuery) ([]User, string, error)
	// Stats counts all accounts, those created since, and per day for the
	// last days.
	Stats(ctx context.Context, since time.Time, days int) (UserStats, error)
}

// Identity is one of an account's sign-in identities.
type Identity struct {
	// Kind is EMAIL or PHONE.
	Kind string
	// Value is the lower-case email or E.164 number (masked unless the
	// console reveals it).
	Value      string
	VerifiedAt time.Time
	CreatedAt  time.Time
}

// LiveSession is one of an account's sessions that has not ended; IP is
// masked.
type LiveSession struct {
	ID         string
	DeviceID   string
	ClientType string
	UserAgent  string
	IP         string
	CreatedAt  time.Time
	LastSeenAt time.Time
}

// Device is a device an account signed in from.
type Device struct {
	ID          string
	FirstSeenAt time.Time
	LastSeenAt  time.Time
}

// Security is an account's sign-in security.
type Security struct {
	Identities []Identity
	// TOTP is ACTIVE, PENDING (set up, not confirmed) or "" (none).
	TOTP              string
	TOTPActivatedAt   time.Time
	PasswordChangedAt time.Time
	LastLoginAt       time.Time
	// LockedSeconds is how long password sign-in stays locked, 0 when it
	// is not.
	LockedSeconds int
	Sessions      []LiveSession
	Devices       []Device
	// PendingIdentityRequests counts its rebind requests waiting.
	PendingIdentityRequests int
}

// LoginEntry is a sign-in attempt; IP is masked.
type LoginEntry struct {
	ID           int64
	Method       string
	Result       string
	IdentityMask string
	DeviceID     string
	UserAgent    string
	IP           string
	NewDevice    bool
	CreatedAt    time.Time
}

// IdentityRequest is a request to move an identity to a new value, which
// an administrator decides when the account has no other identity.
type IdentityRequest struct {
	ID     string
	UserID string
	Kind   string
	// NewValue and CurrentValue are masked unless revealed; CurrentValue
	// is "" when the identity is gone.
	NewValue     string
	CurrentValue string
	// Status is PENDING_REVIEW, APPROVED or REJECTED.
	Status    string
	CreatedAt time.Time
	DecidedAt time.Time
	DecidedBy string
	Reason    string
}

// IdentityRequestQuery selects rebind requests; empty fields match all.
type IdentityRequestQuery struct {
	Status string
	UserID string
	Cursor string
	Limit  int
}

// AccountSecurity reads and changes an account's sign-in security
// (auth-service); actor names the administrator, who has been checked.
type AccountSecurity interface {
	Get(ctx context.Context, userID string) (Security, error)
	// LoginHistory returns sign-ins newest first before the one with ID
	// beforeID (0: the newest) and the beforeID of the next page (0 on the
	// last).
	LoginHistory(ctx context.Context, userID string, beforeID int64, limit int) ([]LoginEntry, int64, error)
	// RevokeSessions ends one live session, or all when sessionID is "",
	// and returns how many ended.
	RevokeSessions(ctx context.Context, userID, sessionID, actor, reason string) (int, error)
	// ResetTOTP removes the authenticator app; false when there was none.
	ResetTOTP(ctx context.Context, userID, actor, reason string) (bool, error)
	// TemporaryPassword sets a random password, ends every session and
	// returns the password and how many sessions ended.
	TemporaryPassword(ctx context.Context, userID, actor, reason string) (string, int, error)
	IdentityRequests(ctx context.Context, q IdentityRequestQuery) ([]IdentityRequest, string, error)
	DecideIdentityRequest(ctx context.Context, id string, approve bool, actor, reason string) (IdentityRequest, error)
}

// StatusChange is a move of an account to another status.
type StatusChange struct {
	From   string
	To     string
	Reason string
	// Actor is an administrator's email, cli:<os user> or a service.
	Actor string
	At    time.Time
}

// Consent is a document version an account accepted.
type Consent struct {
	// Document is TERMS or RISK_DISCLOSURE.
	Document   string
	Version    string
	AcceptedAt time.Time
}

// AccountHistory reads an account's status changes, newest first, and its
// consents (user-service).
type AccountHistory interface {
	History(ctx context.Context, userID string) ([]StatusChange, []Consent, error)
}

// RuleHit is a risk rule that matched.
type RuleHit struct {
	Rule   string
	Score  int
	Detail string
}

// Assessment is an assessment of the risk rules with at least one hit.
type Assessment struct {
	ID              string
	SourceEventType string
	Score           int
	// Action is NONE, STEP_UP, REVIEW or REJECT.
	Action string
	Hits   []RuleHit
	// Enforced tells whether the action was carried out (risk.enforce).
	Enforced  bool
	CreatedAt time.Time
}

// Risk reads risk-service's assessments of an account, newest first.
type Risk interface {
	Assessments(ctx context.Context, userID string, limit int) ([]Assessment, error)
}

// Orders cancels a user's orders (spot-trading-service).
type Orders interface {
	CancelAll(ctx context.Context, userID string) error
}

// WithdrawalQuery selects withdrawals: a status (PENDING_REVIEW when
// empty, ALL for every status), a user and an asset, a page.
type WithdrawalQuery struct {
	Status  string
	UserID  string
	Asset   string
	Network string
	Cursor  string
	Limit   int
	// Order is asc (oldest first, the review queue's default) or desc.
	Order string
}

// CallbackQuery selects the custodian's callbacks: an outcome, a kind, a
// trade/withdrawal ID, transaction hash or address, a page.
type CallbackQuery struct {
	Result string
	Kind   string
	Query  string
	Cursor string
	Limit  int
}

// Withdrawals lists and reviews withdrawals and shows the custody wallet
// (wallet-service); answers pass through as the wallet renders them.
type Withdrawals interface {
	List(ctx context.Context, q WithdrawalQuery) (json.RawMessage, error)
	// Review approves or rejects a withdrawal in review; a positive
	// soleMax lets this approval complete one worth at most that much in
	// USDT however many reviewers it needs (single-person mode).
	Review(ctx context.Context, id string, approve bool, reviewer, reason string, soleMax decimal.Decimal) (json.RawMessage, error)
	// Custody describes the custodian: coins, checks, what is with it.
	Custody(ctx context.Context) (json.RawMessage, error)
	Callbacks(ctx context.Context, q CallbackQuery) (json.RawMessage, error)
	Callback(ctx context.Context, id string) (json.RawMessage, error)
	// Replay applies a stored callback again for actor.
	Replay(ctx context.Context, id, actor, reason string) (json.RawMessage, error)
}

// Instruments lists and changes reference data (instrument-service).
type Instruments interface {
	// List returns the assets, pairs and contracts as JSON.
	List(ctx context.Context) (json.RawMessage, error)
	// SetPairStatus returns the previous status.
	SetPairStatus(ctx context.Context, symbol, to, reason, actor string) (string, error)
	// SetContractStatus returns the previous status.
	SetContractStatus(ctx context.Context, symbol, to, reason, actor string) (string, error)
}

// Derivatives watches the perpetual contracts (derivatives-service's
// internal API); the items pass through as it renders them.
type Derivatives interface {
	// Contracts returns each contract's status, reduce-only state, mark
	// price and open interest.
	Contracts(ctx context.Context) (json.RawMessage, error)
	// LiftReduceOnly ends a contract's reduce-only; the answer says
	// whether it was on.
	LiftReduceOnly(ctx context.Context, symbol, actor string) (json.RawMessage, error)
	// Risk returns the positions warned, taken over or close to it.
	Risk(ctx context.Context) (json.RawMessage, error)
}

// Flag is a feature switch.
type Flag struct {
	Key         string          `json:"key"`
	Enabled     bool            `json:"enabled"`
	Description string          `json:"description"`
	Rules       json.RawMessage `json:"rules"`
	Version     int64           `json:"version"`
	UpdatedBy   string          `json:"updated_by"`
	// UpdatedAt is nil for a flag never set (off).
	UpdatedAt *time.Time `json:"updated_at"`
}

// Flags reads and switches feature flags (the shared config schema); a
// change is recorded with its audit event.
type Flags interface {
	List(ctx context.Context) ([]Flag, error)
	Switch(ctx context.Context, key string, enabled bool, actor, reason string) (Flag, error)
}

// Features evaluates the feature flags that change the console's own
// behavior (a *flags.Client).
type Features interface {
	Enabled(key string, s flags.Subject) bool
}

// Ledger books manual adjustments and insurance fund contributions and
// reads the platform's system accounts (ledger-service).
type Ledger interface {
	Adjust(ctx context.Context, key, userID, asset string, amount decimal.Decimal, actor, reason string) (journalID string, err error)
	FundInsurance(ctx context.Context, key, asset string, amount decimal.Decimal, actor, reason string) (journalID string, err error)
	// SystemBalances returns the system accounts in an asset.
	SystemBalances(ctx context.Context, asset string) ([]Balance, error)
}

// AuditEntry is a line of the audit trail.
type AuditEntry struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	Actor      string          `json:"actor"`
	Target     string          `json:"target"`
	OccurredAt time.Time       `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

// AuditQuery selects audit entries: an exact actor, target and event
// type, a time range, a page.
type AuditQuery struct {
	Actor     string
	Target    string
	EventType string
	From      time.Time
	To        time.Time
	Cursor    string
	Limit     int
}

// AuditLog reads the audit trail (ClickHouse audit_logs).
type AuditLog interface {
	// Search returns a page of entries, newest first, and the cursor of
	// the next ("" on the last).
	Search(ctx context.Context, q AuditQuery) ([]AuditEntry, string, error)
}

// TradingDay is one symbol's trading on one day (UTC); amounts are
// decimal strings.
type TradingDay struct {
	Day         string `json:"day"`
	Symbol      string `json:"symbol"`
	Trades      uint64 `json:"trades"`
	Volume      string `json:"volume"`
	QuoteVolume string `json:"quote_volume"`
	Orders      uint64 `json:"orders"`
	Rejected    uint64 `json:"rejected"`
}

// WalletDay is one asset's credited deposits and confirmed withdrawals on
// one day (UTC).
type WalletDay struct {
	Day              string `json:"day"`
	Asset            string `json:"asset"`
	Deposits         uint64 `json:"deposits"`
	DepositAmount    string `json:"deposit_amount"`
	Withdrawals      uint64 `json:"withdrawals"`
	WithdrawalAmount string `json:"withdrawal_amount"`
	WithdrawalFees   string `json:"withdrawal_fees"`
}

// Candle is a candle of the trades read model.
type Candle struct {
	OpenTime    time.Time `json:"open_time"`
	Open        string    `json:"open"`
	High        string    `json:"high"`
	Low         string    `json:"low"`
	Close       string    `json:"close"`
	Volume      string    `json:"volume"`
	QuoteVolume string    `json:"quote_volume"`
	Trades      uint64    `json:"trades"`
}

// DerivativesDay is one contract's trading, funding and liquidations on
// one day (UTC); amounts are decimal strings in the settlement asset.
type DerivativesDay struct {
	Day    string `json:"day"`
	Symbol string `json:"symbol"`
	// Fills counts the settled sides of trades (two per trade).
	Fills       uint64 `json:"fills"`
	Volume      string `json:"volume"`
	Notional    string `json:"notional"`
	Fees        string `json:"fees"`
	RealizedPnL string `json:"realized_pnl"`
	// FundingPaid and FundingReceived are the positions' payments at the
	// day's settlements.
	FundingPaid     string `json:"funding_paid"`
	FundingReceived string `json:"funding_received"`
	Liquidations    uint64 `json:"liquidations"`
	ADL             uint64 `json:"adl"`
	InsurancePaid   string `json:"insurance_paid"`
}

// OpenInterest is a contract's open positions in the read model.
type OpenInterest struct {
	Symbol    string `json:"symbol"`
	Long      string `json:"long"`
	Short     string `json:"short"`
	Positions uint64 `json:"positions"`
}

// LiquidationStep is a row of the liquidation read model.
type LiquidationStep struct {
	EventID           string    `json:"event_id"`
	Kind              string    `json:"kind"`
	UserID            string    `json:"user_id"`
	Symbol            string    `json:"symbol"`
	PositionSide      string    `json:"position_side"`
	Cross             bool      `json:"cross"`
	ADL               bool      `json:"adl"`
	TradeID           string    `json:"trade_id"`
	Price             string    `json:"price"`
	Quantity          string    `json:"quantity"`
	RealizedPnL       string    `json:"realized_pnl"`
	InsurancePaid     string    `json:"insurance_paid"`
	MarkPrice         string    `json:"mark_price"`
	BankruptcyPrice   string    `json:"bankruptcy_price"`
	MarginBalance     string    `json:"margin_balance"`
	MaintenanceMargin string    `json:"maintenance_margin"`
	OccurredAt        time.Time `json:"occurred_at"`
}

// Reports reads the ClickHouse read models (trades, orders, wallet,
// candles, contracts).
type Reports interface {
	Trading(ctx context.Context, days int) ([]TradingDay, error)
	Wallet(ctx context.Context, days int) ([]WalletDay, error)
	Candles(ctx context.Context, symbol string, seconds uint32, limit int) ([]Candle, error)
	Derivatives(ctx context.Context, days int) ([]DerivativesDay, error)
	OpenInterest(ctx context.Context) ([]OpenInterest, error)
	// Liquidations returns a page of the liquidation steps of the last
	// days, of one kind unless kind is empty, newest first, and the
	// cursor of the next ("" on the last).
	Liquidations(ctx context.Context, days int, kind, cursor string, limit int) ([]LiquidationStep, string, error)
}

// OrderQuery selects spot orders; empty fields match everything.
type OrderQuery struct {
	UserID  string
	OrderID string
	Symbol  string
	Status  string
	Side    string
	From    time.Time
	To      time.Time
	Cursor  string
	Limit   int
}

// Order is a spot order in its latest state (ClickHouse orders_current).
type Order struct {
	OrderID        string    `json:"order_id"`
	ClientOrderID  string    `json:"client_order_id"`
	UserID         string    `json:"user_id"`
	Symbol         string    `json:"symbol"`
	Side           string    `json:"side"`
	Type           string    `json:"type"`
	TimeInForce    string    `json:"time_in_force"`
	Price          *string   `json:"price"`
	Quantity       *string   `json:"quantity"`
	QuoteAmount    *string   `json:"quote_amount"`
	Status         string    `json:"status"`
	FilledQuantity string    `json:"filled_quantity"`
	FilledQuote    string    `json:"filled_quote"`
	Reason         string    `json:"reason"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// TradeQuery selects spot trades: a symbol, a user on either side, a time
// range, a page.
type TradeQuery struct {
	Symbol string
	UserID string
	From   time.Time
	To     time.Time
	Cursor string
	Limit  int
}

// Trade is a spot trade (ClickHouse trades).
type Trade struct {
	TradeID       string    `json:"trade_id"`
	Symbol        string    `json:"symbol"`
	TradeNumber   uint64    `json:"trade_number"`
	Price         string    `json:"price"`
	Quantity      string    `json:"quantity"`
	QuoteQuantity string    `json:"quote_quantity"`
	TakerSide     string    `json:"taker_side"`
	BuyerUserID   string    `json:"buyer_user_id"`
	BuyerOrderID  string    `json:"buyer_order_id"`
	SellerUserID  string    `json:"seller_user_id"`
	SellerOrderID string    `json:"seller_order_id"`
	BuyerIsMaker  bool      `json:"buyer_is_maker"`
	BuyerFee      string    `json:"buyer_fee"`
	SellerFee     string    `json:"seller_fee"`
	ExecutedAt    time.Time `json:"executed_at"`
	// HouseSide is the side HOUSE took (ADR-0015), "" between users.
	HouseSide string `json:"house_side"`
}

// DepositQuery selects deposits; empty fields match everything.
type DepositQuery struct {
	UserID  string
	Asset   string
	Network string
	Status  string
	TxHash  string
	Cursor  string
	Limit   int
}

// Deposit is a deposit in its latest state (ClickHouse wallet_deposits).
type Deposit struct {
	DepositID             string    `json:"deposit_id"`
	UserID                string    `json:"user_id"`
	Asset                 string    `json:"asset"`
	Network               string    `json:"network"`
	Kind                  string    `json:"kind"`
	Address               string    `json:"address"`
	TxHash                string    `json:"tx_hash"`
	Amount                string    `json:"amount"`
	Status                string    `json:"status"`
	Unclaimed             bool      `json:"unclaimed"`
	Reason                string    `json:"reason"`
	Confirmations         uint32    `json:"confirmations"`
	RequiredConfirmations uint32    `json:"required_confirmations"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// Turnover is a day's (or a period's) traded value in one quote asset.
type Turnover struct {
	QuoteAsset string `json:"quote_asset"`
	Amount     string `json:"amount"`
}

// Activity is the overview's trading and wallet figures from the read
// models: the last 24 hours, and per UTC day for the last days.
type Activity struct {
	Trades24h         uint64
	ActiveTraders24h  uint64
	Turnover24h       []Turnover
	PendingDeposits   uint64
	PendingWithdraws  uint64
	RiskEvents24h     uint64
	TradesByDay       map[string]uint64
	TurnoverUSDTByDay map[string]string
}

// Records pages through the read models' orders, trades and deposits and
// sums the overview's activity (ClickHouse).
type Records interface {
	// Each returns a page, newest first, and the cursor of the next (""
	// on the last).
	Orders(ctx context.Context, q OrderQuery) ([]Order, string, error)
	Trades(ctx context.Context, q TradeQuery) ([]Trade, string, error)
	Deposits(ctx context.Context, q DepositQuery) ([]Deposit, string, error)
	Activity(ctx context.Context, days int) (Activity, error)
}

// FeedStatus is market-data-service's reference feed state.
type FeedStatus struct {
	State      string          `json:"state"`
	ReceivedAt *time.Time      `json:"received_at"`
	Followed   []string        `json:"followed"`
	Halted     json.RawMessage `json:"halted"`
}

// Market reads market-data-service's internal state.
type Market interface {
	Feed(ctx context.Context) (FeedStatus, error)
}

// Prices are the last prices of the listed symbols (market-data-service's
// tickers), by symbol.
type Prices map[string]decimal.Decimal

// MarketPrices reads the last prices of every listed symbol.
type MarketPrices interface {
	Prices(ctx context.Context) (Prices, error)
}

// HousePair is HOUSE's spot trading on one pair in the trades read model
// (ADR-0015): the base it bought and sold, the quote it paid and got.
type HousePair struct {
	Symbol     string    `json:"symbol"`
	Trades     uint64    `json:"trades"`
	BoughtBase string    `json:"bought_base"`
	SoldBase   string    `json:"sold_base"`
	PaidQuote  string    `json:"paid_quote"`
	GotQuote   string    `json:"got_quote"`
	LastAt     time.Time `json:"last_at"`
}

// HouseTrades sums HOUSE's spot trades per pair (ClickHouse).
type HouseTrades interface {
	HousePairs(ctx context.Context) ([]HousePair, error)
}

// HousePositions reads HOUSE's perpetual contract positions as
// derivatives-service renders a user's positions.
type HousePositions interface {
	Positions(ctx context.Context, userID string) (json.RawMessage, error)
}

// ReconciliationRun is one invariant check of one ledger reconciliation.
type ReconciliationRun struct {
	Check      string          `json:"check"`
	StartedAt  time.Time       `json:"started_at"`
	Mismatches int             `json:"mismatches"`
	Details    json.RawMessage `json:"details"`
}

// Reconciliation is the latest run of every check and the recent runs
// that found mismatches.
type Reconciliation struct {
	Latest   []ReconciliationRun `json:"latest"`
	Failures []ReconciliationRun `json:"failures"`
}

// Reconciler reads the ledger's reconciliation runs (ledger-service).
type Reconciler interface {
	Reconciliation(ctx context.Context, failures int) (Reconciliation, error)
}

// ServiceHealth is a service's readiness as its ops endpoint answers.
type ServiceHealth struct {
	Service   string `json:"service"`
	Ready     bool   `json:"ready"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

// Health probes every service's readiness.
type Health interface {
	Check(ctx context.Context) []ServiceHealth
}
