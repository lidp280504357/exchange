// Package flags evaluates feature flags (ADR-0005, requirements §5.14):
// every high-risk capability is off unless its flag is enabled and allows
// the request's region, account status, asset, symbol and user. Flags live
// in the config schema; services keep a copy refreshed every 5 seconds,
// and a missing flag is off.
package flags

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/skill/exchange/internal/platform/pg"
)

// Known flag keys; exchangectl only sets known keys.
const (
	KeyRegistrationSMS  = "auth.sms"                 // SMS as a registration and login channel
	KeyTransfer         = "account.transfer"         // spot <-> futures transfers
	KeyManualAdjustment = "ledger.manual_adjustment" // operator credits of simulated funds
	KeyWelcomeCredit    = "ledger.welcome_credit"    // demo funds for new users, test only
	KeyWithdraw         = "wallet.withdraw"          // phase 2
	KeyDerivatives      = "derivatives.trading"      // phase 3
	KeyReferenceKline   = "market.reference_kline"   // show reference candles for new pairs
	KeyRiskEnforce      = "risk.enforce"             // carry out the actions of risk rules
	KeyReferenceFeed    = "market.reference_feed"    // external reference prices (Binance public data, test only)
	KeyMarketMaker      = "market.maker"             // retired (ADR-0015): the quoting market maker
	KeyReferenceTicker  = "market.reference_ticker"  // show the reference market's tickers, per symbol (ADR-0010)
	KeyHaltOnFeedLoss   = "market.halt_on_feed_loss" // halt followed pairs after 5 minutes without reference data
	KeyAdminNoTOTP      = "admin.login_without_totp" // admin console sign-in without the authenticator code
	KeyReferenceDepth   = "market.reference_depth"   // show the reference market's book and trades, per symbol (ADR-0010)
	KeyHouseLiquidity   = "market.house_liquidity"   // HOUSE's virtual liquidity from the reference book, per symbol (ADR-0015)
	KeyInternalMatching = "market.internal_matching" // users' orders trade with each other on pairs with HOUSE liquidity (ADR-0015)
)

// KeyTwoPerson makes the admin console's fund operations take a second
// administrator; off, one carries them out within limits (design
// 2026-10-02 §2).
const KeyTwoPerson = "admin.two_person_approval"

// KeyFlatMinutes has market-data-service store a flat one-minute candle
// for each minute without a trade of a symbol no reference market
// follows (the platform coin's), per symbol (coordinator 2026-10-04).
const KeyFlatMinutes = "market.flat_minutes"

// KeyTestAssets opens the hidden test assets' deposits and withdrawals
// (ADR-0017) to the users its rules allow: on the test server the
// end-to-end tests' accounts, region AQ.
const KeyTestAssets = "wallet.test_assets"

// The simulated market of the platform coin ASTRA (ASTRA design §5.2):
// the bots trade at all, the operators' price events run, the bots trade
// the perpetual too.
const (
	KeySimEnabled = "sim.enabled"
	KeySimEvents  = "sim.events"
	KeySimPerp    = "sim.perp"
	// KeySimHaltOnLoss halts a pair whose simulated market went silent
	// (market-sim's heartbeat lost for a minute) and resumes it when the
	// heartbeat is back (ASTRA design §9).
	KeySimHaltOnLoss = "sim.halt_on_loss"
)

// Margin trading (design 2026-10-06 §6): users borrow from HOUSE into
// margin accounts at all, the system liquidates accounts at their
// liquidation level, orders borrow what they lack (side_effect
// AUTO_BORROW). All off by default; margin-service reads them from batch E1.
const (
	KeyMarginEnabled     = "margin.enabled"
	KeyMarginLiquidation = "margin.liquidation"
	KeyMarginAutoBorrow  = "margin.auto_borrow"
)

// Known describes the known flags.
var Known = map[string]string{
	KeyRegistrationSMS:   "SMS as a registration and login channel (high-risk regions stay email-only)",
	KeyTransfer:          "Transfers between spot and futures accounts",
	KeyManualAdjustment:  "Operator credits of simulated funds (MANUAL_ADJUSTMENT)",
	KeyWelcomeCredit:     "Simulated demo funds for newly registered users (test environments only)",
	KeyWithdraw:          "Withdrawals (phase 2)",
	KeyDerivatives:       "Perpetual futures trading (phase 3)",
	KeyReferenceKline:    "Candles from the reference market instead of the platform's, per symbol (ADR-0010)",
	KeyRiskEnforce:       "Carry out risk rule actions (accounts scored for review move to RISK_REVIEW); off only records the scores",
	KeyReferenceFeed:     "External reference prices from Binance public data; test environments only until a data license exists (§11.9)",
	KeyMarketMaker:       "Retired with ADR-0015: the quoting market maker of §11.10, replaced by HOUSE's virtual liquidity (market.house_liquidity)",
	KeyReferenceTicker:   "Tickers (last price, 24-hour statistics, best bid and ask) from the reference market instead of the platform's (ADR-0010)",
	KeyHaltOnFeedLoss:    "Halt the pairs that follow a reference market after 5 minutes without reference data; resume them when it is back (ADR-0010)",
	KeyAdminNoTOTP:       "Admin console sign-in with the password alone: the authenticator code is not asked for or checked (test environments only)",
	KeyReferenceDepth:    "Order book and public trades from the reference market instead of the platform's, per symbol (ADR-0010)",
	KeyHouseLiquidity:    "HOUSE trades against orders at the reference market's book (virtual liquidity), per symbol; off leaves the platform's own book (ADR-0015)",
	KeyInternalMatching:  "Users' orders also trade with each other on pairs with HOUSE liquidity; off makes HOUSE the counterparty of every trade (ADR-0015)",
	KeyTwoPerson:         "Admin console: manual adjustments, insurance fund contributions and withdrawals needing two reviewers take a second administrator; off lets one administrator carry them out within the console's single-person limits",
	KeyFlatMinutes:       "Store a flat one-minute candle (the previous close, no volume) for each minute without a trade, per symbol no reference market follows (the platform coin and its perpetual), for the chart and ClickHouse",
	KeyTestAssets:        "Deposits and withdrawals of the hidden test assets (ADR-0017) for the users these rules allow: the end-to-end tests' accounts (region AQ on the test server); off, nobody's",
	KeySimEnabled:        "The simulated market of the platform coin (market-sim): its bots quote and trade ASTRA-USDT around the model's price; off cancels their orders",
	KeySimEvents:         "Operators' price events in the simulated market (jumps, targets, trends, pauses)",
	KeySimPerp:           "The simulated market's bots also make the market on the platform coin's perpetual",
	KeySimHaltOnLoss:     "Halt a pair (and its perpetual) a minute after its simulated market's heartbeat stopped; resume when it is back",
	KeyMarginEnabled:     "Margin trading: transfers to margin accounts, borrowing from HOUSE, repaying and orders on margin accounts; off answers MARGIN_DISABLED (design 2026-10-06)",
	KeyMarginLiquidation: "Margin liquidations: accounts at their liquidation level are frozen, closed against HOUSE and their debts repaid; off only warns (design 2026-10-06 §4.5)",
	KeyMarginAutoBorrow:  "Orders on margin accounts with side_effect AUTO_BORROW borrow what the free balance lacks (design 2026-10-06 §5.1)",
}

// List allows or denies values of one dimension. An empty Allow allows
// every value that Deny does not list.
type List struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

func (l *List) constrains() bool { return l != nil && (len(l.Allow) > 0 || len(l.Deny) > 0) }

// permits fails closed: a constrained dimension with an unknown value is
// not permitted.
func (l *List) permits(v string) bool {
	if !l.constrains() {
		return true
	}
	if v == "" || slices.Contains(l.Deny, v) {
		return false
	}
	return len(l.Allow) == 0 || slices.Contains(l.Allow, v)
}

// Rules restrict where an enabled flag applies.
type Rules struct {
	Regions  *List `json:"regions,omitempty"`
	Statuses *List `json:"statuses,omitempty"`
	Assets   *List `json:"assets,omitempty"`
	Symbols  *List `json:"symbols,omitempty"`
	Users    *List `json:"users,omitempty"`
}

// Flag is one stored flag.
type Flag struct {
	Key         string    `json:"key"`
	Enabled     bool      `json:"enabled"`
	Rules       Rules     `json:"rules"`
	Description string    `json:"description,omitempty"`
	Version     int64     `json:"version"`
	UpdatedBy   string    `json:"updated_by"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Subject is what a check is about; leave unknown dimensions empty.
type Subject struct {
	UserID string
	Region string // ISO 3166-1 alpha-2
	Status string // ACTIVE, RISK_REVIEW, FROZEN, CLOSED
	Asset  string
	Symbol string
}

// Allows reports whether f is on for s.
func (f Flag) Allows(s Subject) bool { return f.Denial(s) == "" }

// Denial names what rejects s: "disabled", or the dimension whose rule
// fails ("region", "status", "asset", "symbol", "user"); "" when f allows
// s. Eligibility uses it to tell a region block from a switched-off feature.
func (f Flag) Denial(s Subject) string {
	r := f.Rules
	switch {
	case !f.Enabled:
		return "disabled"
	case !r.Regions.permits(s.Region):
		return "region"
	case !r.Statuses.permits(s.Status):
		return "status"
	case !r.Assets.permits(s.Asset):
		return "asset"
	case !r.Symbols.permits(s.Symbol):
		return "symbol"
	case !r.Users.permits(s.UserID):
		return "user"
	}
	return ""
}

// Load reads every flag.
func Load(ctx context.Context, q pg.Querier) (map[string]Flag, error) {
	rows, err := q.Query(ctx, `SELECT key, enabled, rules, description, version, updated_by, updated_at FROM flags`)
	if err != nil {
		return nil, fmt.Errorf("flags: load: %w", err)
	}
	defer rows.Close()
	out := map[string]Flag{}
	for rows.Next() {
		var f Flag
		var rules []byte
		if err := rows.Scan(&f.Key, &f.Enabled, &rules, &f.Description, &f.Version, &f.UpdatedBy, &f.UpdatedAt); err != nil {
			return nil, fmt.Errorf("flags: load: %w", err)
		}
		if err := json.Unmarshal(rules, &f.Rules); err != nil {
			return nil, fmt.Errorf("flags: rules of %s: %w", f.Key, err)
		}
		out[f.Key] = f
	}
	return out, rows.Err()
}

// Change is one entry of a flag's history.
type Change struct {
	Key       string
	Old, New  json.RawMessage
	ChangedBy string
	Reason    string
	ChangedAt time.Time
}

// Set creates or replaces flag f inside tx, bumping its version and
// recording the change. It returns the previous flag (nil when new) and
// the stored one.
func Set(ctx context.Context, tx pgx.Tx, f Flag, actor, reason string) (*Flag, Flag, error) {
	if actor == "" || reason == "" {
		return nil, Flag{}, fmt.Errorf("flags: actor and reason are required")
	}
	var old *Flag
	var prev Flag
	var rules []byte
	err := tx.QueryRow(ctx, `SELECT key, enabled, rules, description, version, updated_by, updated_at
		FROM flags WHERE key = $1 FOR UPDATE`, f.Key).
		Scan(&prev.Key, &prev.Enabled, &rules, &prev.Description, &prev.Version, &prev.UpdatedBy, &prev.UpdatedAt)
	switch {
	case err == nil:
		if err := json.Unmarshal(rules, &prev.Rules); err != nil {
			return nil, Flag{}, fmt.Errorf("flags: rules of %s: %w", f.Key, err)
		}
		old = &prev
	case !pg.IsNoRows(err):
		return nil, Flag{}, fmt.Errorf("flags: read %s: %w", f.Key, err)
	}

	newRules, err := json.Marshal(f.Rules)
	if err != nil {
		return nil, Flag{}, fmt.Errorf("flags: %w", err)
	}
	var stored Flag
	err = tx.QueryRow(ctx, `INSERT INTO flags (key, enabled, rules, description, updated_by)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (key) DO UPDATE SET enabled = EXCLUDED.enabled, rules = EXCLUDED.rules,
			description = EXCLUDED.description, updated_by = EXCLUDED.updated_by,
			version = flags.version + 1, updated_at = now()
		RETURNING key, enabled, description, version, updated_by, updated_at`,
		f.Key, f.Enabled, newRules, f.Description, actor).
		Scan(&stored.Key, &stored.Enabled, &stored.Description, &stored.Version, &stored.UpdatedBy, &stored.UpdatedAt)
	if err != nil {
		return nil, Flag{}, fmt.Errorf("flags: write %s: %w", f.Key, err)
	}
	stored.Rules = f.Rules

	var oldJSON []byte
	if old != nil {
		oldJSON, _ = json.Marshal(old)
	}
	newJSON, _ := json.Marshal(stored)
	if _, err := tx.Exec(ctx, `INSERT INTO flag_changes (key, old_value, new_value, changed_by, reason)
		VALUES ($1, $2, $3, $4, $5)`, f.Key, oldJSON, newJSON, actor, reason); err != nil {
		return nil, Flag{}, fmt.Errorf("flags: record change of %s: %w", f.Key, err)
	}
	return old, stored, nil
}

// History returns the latest changes of key, newest first.
func History(ctx context.Context, q pg.Querier, key string, limit int) ([]Change, error) {
	rows, err := q.Query(ctx, `SELECT key, old_value, new_value, changed_by, reason, changed_at
		FROM flag_changes WHERE key = $1 ORDER BY id DESC LIMIT $2`, key, limit)
	if err != nil {
		return nil, fmt.Errorf("flags: history: %w", err)
	}
	defer rows.Close()
	var out []Change
	for rows.Next() {
		var c Change
		if err := rows.Scan(&c.Key, &c.Old, &c.New, &c.ChangedBy, &c.Reason, &c.ChangedAt); err != nil {
			return nil, fmt.Errorf("flags: history: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RefreshInterval is how stale a service's copy may be.
const RefreshInterval = 5 * time.Second

// Client answers flag checks from a local copy that Run refreshes.
type Client struct {
	db  *pg.DB
	log *slog.Logger

	mu    sync.RWMutex
	flags map[string]Flag

	lastRefresh prometheus.Gauge
}

// NewClient returns a client reading from db, which is bound to the config
// schema, and registers its metrics.
func NewClient(db *pg.DB, log *slog.Logger, reg prometheus.Registerer) *Client {
	c := &Client{
		db:  db,
		log: log,
		lastRefresh: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "flags_last_refresh_timestamp_seconds",
			Help: "When the local copy of the feature flags was last refreshed.",
		}),
	}
	reg.MustRegister(c.lastRefresh)
	return c
}

// Refresh reloads every flag.
func (c *Client) Refresh(ctx context.Context) error {
	all, err := Load(ctx, c.db)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.flags = all
	c.mu.Unlock()
	c.lastRefresh.SetToCurrentTime()
	return nil
}

// Run refreshes every RefreshInterval until ctx ends; failures keep the
// last copy.
func (c *Client) Run(ctx context.Context) error {
	ticker := time.NewTicker(RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		if err := c.Refresh(ctx); err != nil && ctx.Err() == nil {
			c.log.WarnContext(ctx, "feature flag refresh failed; keeping the last copy", "error", err)
		}
	}
}

// Enabled reports whether key is on for s; unknown or missing flags are off.
func (c *Client) Enabled(key string, s Subject) bool {
	c.mu.RLock()
	f, ok := c.flags[key]
	c.mu.RUnlock()
	return ok && f.Allows(s)
}

// Get returns the local copy of key.
func (c *Client) Get(key string) (Flag, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	f, ok := c.flags[key]
	return f, ok
}
