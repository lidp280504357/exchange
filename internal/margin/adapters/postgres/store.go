// Package postgres stores margin-service's state in the margin schema.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/outbox"
	"github.com/skill/exchange/internal/platform/pg"
)

// Store implements ports.Store.
type Store struct {
	db     *pg.DB
	events *event.Factory
}

// NewStore returns a store whose events are built by events.
func NewStore(db *pg.DB, events *event.Factory) *Store { return &Store{db: db, events: events} }

// Tx runs fn in a transaction.
func (s *Store) Tx(ctx context.Context, fn func(ports.Repos) error) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error { return fn(repos{q: tx, events: s.events}) })
}

// Read returns repositories on the pool.
func (s *Store) Read() ports.Repos { return repos{q: s.db, events: s.events} }

// Snapshot runs fn in a read-only repeatable-read transaction.
func (s *Store) Snapshot(ctx context.Context, fn func(ports.Repos) error) error {
	opts := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	return s.db.InTxOpts(ctx, opts, func(tx pgx.Tx) error { return fn(repos{q: tx, events: s.events}) })
}

type repos struct {
	q      pg.Querier
	events *event.Factory
}

func (r repos) Terms() ports.TermsRepo        { return terms(r) }
func (r repos) Pools() ports.PoolRepo         { return pools(r) }
func (r repos) Rates() ports.RateRepo         { return rates(r) }
func (r repos) Accounts() ports.AccountRepo   { return accounts(r) }
func (r repos) Loans() ports.LoanRepo         { return loans(r) }
func (r repos) Borrows() ports.BorrowRepo     { return borrows(r) }
func (r repos) Repays() ports.RepayRepo       { return repays(r) }
func (r repos) Interest() ports.InterestRepo  { return interest(r) }
func (r repos) Transfers() ports.TransferRepo { return transfers(r) }

func (r repos) Reservations() ports.ReservationRepo { return reservations(r) }
func (r repos) Liquidations() ports.LiquidationRepo { return liquidations(r) }
func (r repos) Runs() ports.RunRepo                 { return runs(r) }

func (r repos) LockUser(ctx context.Context, userID string) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('margin:' || $1, 0))`, userID); err != nil {
		return fmt.Errorf("lock user: %w", err)
	}
	return nil
}

func (r repos) Emit(ctx context.Context, topic string, msg proto.Message, aggregateType, aggregateID string) error {
	env, err := r.events.New(ctx, msg, aggregateType, aggregateID)
	if err != nil {
		return err
	}
	return outbox.Add(ctx, r.q, topic, env)
}

// account rebuilds an account from its columns.
func account(accountType, symbol string) domain.Account {
	return domain.Account{Type: domain.AccountType(accountType), Symbol: symbol}
}

// nullTime stores a zero time as NULL.
func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func timeOf(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// nullUUID stores an empty ID as NULL.
func nullUUID(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}

func stringOf(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

type terms repos

const assetColumns = `asset, borrowable, collateral, haircut, pool_cap, user_cap, interest_model, fixed_rate, float_base,
	float_kink, float_kink_rate, float_max_rate`

func scanAsset(row pgx.Row) (domain.AssetTerms, error) {
	var t domain.AssetTerms
	err := row.Scan(&t.Asset, &t.Borrowable, &t.Collateral, &t.Haircut, &t.PoolCap, &t.UserCap, &t.Model, &t.FixedRate,
		&t.Floating.Base, &t.Floating.Kink, &t.Floating.KinkRate, &t.Floating.MaxRate)
	return t, err
}

func (r terms) Assets(ctx context.Context) ([]domain.AssetTerms, error) {
	rows, err := r.q.Query(ctx, `SELECT `+assetColumns+` FROM asset_terms ORDER BY asset`)
	if err != nil {
		return nil, fmt.Errorf("query asset terms: %w", err)
	}
	defer rows.Close()
	var out []domain.AssetTerms
	for rows.Next() {
		t, err := scanAsset(rows)
		if err != nil {
			return nil, fmt.Errorf("scan asset terms: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r terms) Asset(ctx context.Context, asset string) (domain.AssetTerms, bool, error) {
	t, err := scanAsset(r.q.QueryRow(ctx, `SELECT `+assetColumns+` FROM asset_terms WHERE asset = $1`, asset))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AssetTerms{}, false, nil
	}
	if err != nil {
		return domain.AssetTerms{}, false, fmt.Errorf("load asset terms: %w", err)
	}
	return t, true, nil
}

func (r terms) SaveAsset(ctx context.Context, t domain.AssetTerms, by string) error {
	_, err := r.q.Exec(ctx, `INSERT INTO asset_terms (`+assetColumns+`, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (asset) DO UPDATE SET borrowable = $2, collateral = $3, haircut = $4, pool_cap = $5, user_cap = $6,
			interest_model = $7, fixed_rate = $8, float_base = $9, float_kink = $10, float_kink_rate = $11, float_max_rate = $12,
			version = asset_terms.version + 1, updated_at = now(), updated_by = $13`,
		t.Asset, t.Borrowable, t.Collateral, t.Haircut, t.PoolCap, t.UserCap, t.Model, t.FixedRate, t.Floating.Base,
		t.Floating.Kink, t.Floating.KinkRate, t.Floating.MaxRate, by)
	if err != nil {
		return fmt.Errorf("save asset terms: %w", err)
	}
	return nil
}

const pairColumns = `symbol, base_asset, quote_asset, isolated, leverage, warn_level, liquidation_level, liquidation_fee`

func scanPair(row pgx.Row) (domain.Pair, error) {
	var p domain.Pair
	err := row.Scan(&p.Symbol, &p.Base, &p.Quote, &p.Isolated, &p.Terms.Leverage, &p.Terms.WarnLevel, &p.Terms.LiquidationLevel,
		&p.Terms.LiquidationFee)
	return p, err
}

func (r terms) Pairs(ctx context.Context) ([]domain.Pair, error) {
	rows, err := r.q.Query(ctx, `SELECT `+pairColumns+` FROM pair_terms ORDER BY symbol`)
	if err != nil {
		return nil, fmt.Errorf("query pair terms: %w", err)
	}
	defer rows.Close()
	var out []domain.Pair
	for rows.Next() {
		p, err := scanPair(rows)
		if err != nil {
			return nil, fmt.Errorf("scan pair terms: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r terms) Pair(ctx context.Context, symbol string) (domain.Pair, bool, error) {
	p, err := scanPair(r.q.QueryRow(ctx, `SELECT `+pairColumns+` FROM pair_terms WHERE symbol = $1`, symbol))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Pair{}, false, nil
	}
	if err != nil {
		return domain.Pair{}, false, fmt.Errorf("load pair terms: %w", err)
	}
	return p, true, nil
}

func (r terms) SavePair(ctx context.Context, p domain.Pair, by string) error {
	_, err := r.q.Exec(ctx, `INSERT INTO pair_terms (`+pairColumns+`, updated_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (symbol) DO UPDATE SET base_asset = $2, quote_asset = $3, isolated = $4, leverage = $5, warn_level = $6,
			liquidation_level = $7, liquidation_fee = $8, version = pair_terms.version + 1, updated_at = now(), updated_by = $9`,
		p.Symbol, p.Base, p.Quote, p.Isolated, p.Terms.Leverage, p.Terms.WarnLevel, p.Terms.LiquidationLevel, p.Terms.LiquidationFee, by)
	if err != nil {
		return fmt.Errorf("save pair terms: %w", err)
	}
	return nil
}

func (r terms) Cross(ctx context.Context) (domain.Terms, bool, error) {
	var t domain.Terms
	err := r.q.QueryRow(ctx, `SELECT leverage, warn_level, liquidation_level, liquidation_fee FROM cross_terms`).
		Scan(&t.Leverage, &t.WarnLevel, &t.LiquidationLevel, &t.LiquidationFee)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Terms{}, false, nil
	}
	if err != nil {
		return domain.Terms{}, false, fmt.Errorf("load cross terms: %w", err)
	}
	return t, true, nil
}

func (r terms) SaveCross(ctx context.Context, t domain.Terms, by string) error {
	_, err := r.q.Exec(ctx, `INSERT INTO cross_terms (id, leverage, warn_level, liquidation_level, liquidation_fee, updated_by)
		VALUES (true, $1, $2, $3, $4, $5) ON CONFLICT (id) DO UPDATE SET leverage = $1, warn_level = $2, liquidation_level = $3,
		liquidation_fee = $4, version = cross_terms.version + 1, updated_at = now(), updated_by = $5`,
		t.Leverage, t.WarnLevel, t.LiquidationLevel, t.LiquidationFee, by)
	if err != nil {
		return fmt.Errorf("save cross terms: %w", err)
	}
	return nil
}

func (r terms) CrossWithSource(ctx context.Context) (domain.Terms, string, bool, error) {
	var t domain.Terms
	var by string
	err := r.q.QueryRow(ctx, `SELECT leverage, warn_level, liquidation_level, liquidation_fee, updated_by FROM cross_terms`).
		Scan(&t.Leverage, &t.WarnLevel, &t.LiquidationLevel, &t.LiquidationFee, &by)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Terms{}, "", false, nil
	}
	if err != nil {
		return domain.Terms{}, "", false, fmt.Errorf("load cross terms: %w", err)
	}
	return t, by, true, nil
}

func (r terms) AssetWithSource(ctx context.Context, asset string) (domain.AssetTerms, string, bool, error) {
	var by string
	row := r.q.QueryRow(ctx, `SELECT `+assetColumns+`, updated_by FROM asset_terms WHERE asset = $1`, asset)
	var t domain.AssetTerms
	err := row.Scan(&t.Asset, &t.Borrowable, &t.Collateral, &t.Haircut, &t.PoolCap, &t.UserCap, &t.Model, &t.FixedRate,
		&t.Floating.Base, &t.Floating.Kink, &t.Floating.KinkRate, &t.Floating.MaxRate, &by)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AssetTerms{}, "", false, nil
	}
	if err != nil {
		return domain.AssetTerms{}, "", false, fmt.Errorf("load asset terms: %w", err)
	}
	return t, by, true, nil
}

func (r terms) metas(ctx context.Context, sql string) (map[string]ports.Meta, error) {
	rows, err := r.q.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("query terms versions: %w", err)
	}
	defer rows.Close()
	out := map[string]ports.Meta{}
	for rows.Next() {
		var key string
		var m ports.Meta
		if err := rows.Scan(&key, &m.Version, &m.UpdatedBy, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan terms version: %w", err)
		}
		out[key] = m
	}
	return out, rows.Err()
}

func (r terms) AssetMetas(ctx context.Context) (map[string]ports.Meta, error) {
	return r.metas(ctx, `SELECT asset, version, updated_by, updated_at FROM asset_terms`)
}

func (r terms) PairMetas(ctx context.Context) (map[string]ports.Meta, error) {
	return r.metas(ctx, `SELECT symbol, version, updated_by, updated_at FROM pair_terms`)
}

func (r terms) CrossMeta(ctx context.Context) (ports.Meta, bool, error) {
	all, err := r.metas(ctx, `SELECT 'cross', version, updated_by, updated_at FROM cross_terms`)
	m, ok := all["cross"]
	return m, ok, err
}

// saved reports whether a conditional save of terms changed its row.
func saved(tag pgconn.CommandTag, err error) (bool, error) {
	if err != nil {
		return false, fmt.Errorf("save terms: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r terms) SaveAssetIf(ctx context.Context, t domain.AssetTerms, by string, version int64) (bool, error) {
	args := []any{
		t.Asset, t.Borrowable, t.Collateral, t.Haircut, t.PoolCap, t.UserCap, t.Model, t.FixedRate, t.Floating.Base,
		t.Floating.Kink, t.Floating.KinkRate, t.Floating.MaxRate, by,
	}
	if version == 0 {
		return saved(r.q.Exec(ctx, `INSERT INTO asset_terms (`+assetColumns+`, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) ON CONFLICT (asset) DO NOTHING`, args...))
	}
	return saved(r.q.Exec(ctx, `UPDATE asset_terms SET borrowable = $2, collateral = $3, haircut = $4, pool_cap = $5, user_cap = $6,
		interest_model = $7, fixed_rate = $8, float_base = $9, float_kink = $10, float_kink_rate = $11, float_max_rate = $12,
		version = version + 1, updated_at = now(), updated_by = $13 WHERE asset = $1 AND version = $14`, append(args, version)...))
}

func (r terms) SavePairIf(ctx context.Context, p domain.Pair, by string, version int64) (bool, error) {
	args := []any{
		p.Symbol, p.Base, p.Quote, p.Isolated, p.Terms.Leverage, p.Terms.WarnLevel, p.Terms.LiquidationLevel,
		p.Terms.LiquidationFee, by,
	}
	if version == 0 {
		return saved(r.q.Exec(ctx, `INSERT INTO pair_terms (`+pairColumns+`, updated_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (symbol) DO NOTHING`, args...))
	}
	return saved(r.q.Exec(ctx, `UPDATE pair_terms SET base_asset = $2, quote_asset = $3, isolated = $4, leverage = $5,
		warn_level = $6, liquidation_level = $7, liquidation_fee = $8, version = version + 1, updated_at = now(), updated_by = $9
		WHERE symbol = $1 AND version = $10`, append(args, version)...))
}

func (r terms) SaveCrossIf(ctx context.Context, t domain.Terms, by string, version int64) (bool, error) {
	args := []any{t.Leverage, t.WarnLevel, t.LiquidationLevel, t.LiquidationFee, by}
	if version == 0 {
		return saved(r.q.Exec(ctx, `INSERT INTO cross_terms (id, leverage, warn_level, liquidation_level, liquidation_fee, updated_by)
			VALUES (true, $1, $2, $3, $4, $5) ON CONFLICT (id) DO NOTHING`, args...))
	}
	return saved(r.q.Exec(ctx, `UPDATE cross_terms SET leverage = $1, warn_level = $2, liquidation_level = $3, liquidation_fee = $4,
		version = version + 1, updated_at = now(), updated_by = $5 WHERE version = $6`, append(args, version)...))
}

func (r terms) PairWithSource(ctx context.Context, symbol string) (domain.Pair, string, bool, error) {
	var p domain.Pair
	var by string
	err := r.q.QueryRow(ctx, `SELECT `+pairColumns+`, updated_by FROM pair_terms WHERE symbol = $1`, symbol).Scan(&p.Symbol, &p.Base,
		&p.Quote, &p.Isolated, &p.Terms.Leverage, &p.Terms.WarnLevel, &p.Terms.LiquidationLevel, &p.Terms.LiquidationFee, &by)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Pair{}, "", false, nil
	}
	if err != nil {
		return domain.Pair{}, "", false, fmt.Errorf("load pair terms: %w", err)
	}
	return p, by, true, nil
}

type pools repos

func (r pools) LockLent(ctx context.Context, asset string) (decimal.Decimal, error) {
	if _, err := r.q.Exec(ctx, `INSERT INTO pools (asset) VALUES ($1) ON CONFLICT DO NOTHING`, asset); err != nil {
		return decimal.Zero, fmt.Errorf("create pool: %w", err)
	}
	var lent decimal.Decimal
	if err := r.q.QueryRow(ctx, `SELECT lent FROM pools WHERE asset = $1 FOR UPDATE`, asset).Scan(&lent); err != nil {
		return decimal.Zero, fmt.Errorf("lock pool: %w", err)
	}
	return lent, nil
}

func (r pools) AddLent(ctx context.Context, asset string, delta decimal.Decimal) error {
	tag, err := r.q.Exec(ctx, `UPDATE pools SET lent = lent + $2 WHERE asset = $1`, asset, delta)
	if err != nil {
		return fmt.Errorf("update pool: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("update pool %s: no row", asset)
	}
	return nil
}

func (r pools) Lent(ctx context.Context) (map[string]decimal.Decimal, error) {
	rows, err := r.q.Query(ctx, `SELECT asset, lent FROM pools`)
	if err != nil {
		return nil, fmt.Errorf("query pools: %w", err)
	}
	defer rows.Close()
	out := map[string]decimal.Decimal{}
	for rows.Next() {
		var asset string
		var lent decimal.Decimal
		if err := rows.Scan(&asset, &lent); err != nil {
			return nil, fmt.Errorf("scan pool: %w", err)
		}
		out[asset] = lent
	}
	return out, rows.Err()
}

type rates repos

func (r rates) Get(ctx context.Context, asset string, hour time.Time) (ports.Rate, bool, error) {
	rate := ports.Rate{Asset: asset}
	err := r.q.QueryRow(ctx, `SELECT hour, interest_model, rate, lent, pool_cap FROM hourly_rates WHERE asset = $1 AND hour = $2`,
		asset, hour).Scan(&rate.Hour, &rate.Model, &rate.Rate, &rate.Lent, &rate.PoolCap)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.Rate{}, false, nil
	}
	if err != nil {
		return ports.Rate{}, false, fmt.Errorf("load hourly rate: %w", err)
	}
	rate.Hour = rate.Hour.UTC()
	return rate, true, nil
}

func (r rates) Set(ctx context.Context, rate ports.Rate) (ports.Rate, error) {
	if _, err := r.q.Exec(ctx, `INSERT INTO hourly_rates (asset, hour, interest_model, rate, lent, pool_cap)
		VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (asset, hour) DO NOTHING`,
		rate.Asset, rate.Hour, rate.Model, rate.Rate, rate.Lent, rate.PoolCap); err != nil {
		return ports.Rate{}, fmt.Errorf("store hourly rate: %w", err)
	}
	stored, ok, err := r.Get(ctx, rate.Asset, rate.Hour)
	if err != nil {
		return ports.Rate{}, err
	}
	if !ok {
		return ports.Rate{}, fmt.Errorf("hourly rate of %s at %s vanished", rate.Asset, rate.Hour)
	}
	return stored, nil
}

type accounts repos

const accountColumns = `user_id, account_type, symbol, status, warned_at, frozen_reason, frozen_by, frozen_at, created_at, updated_at`

func scanAccount(row pgx.Row) (ports.Account, error) {
	var a ports.Account
	var accountType, symbol string
	var warned, frozen *time.Time
	if err := row.Scan(&a.UserID, &accountType, &symbol, &a.Status, &warned, &a.FrozenReason, &a.FrozenBy, &frozen, &a.CreatedAt,
		&a.UpdatedAt); err != nil {
		return ports.Account{}, err
	}
	a.Account, a.WarnedAt, a.FrozenAt = account(accountType, symbol), timeOf(warned), timeOf(frozen)
	return a, nil
}

func (r accounts) Ensure(ctx context.Context, userID string, a domain.Account, at time.Time) (ports.Account, error) {
	if _, err := r.q.Exec(ctx, `INSERT INTO accounts (user_id, account_type, symbol, created_at, updated_at) VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT DO NOTHING`, userID, a.Type, a.Symbol, at); err != nil {
		return ports.Account{}, fmt.Errorf("create margin account: %w", err)
	}
	got, ok, err := r.Get(ctx, userID, a)
	if err != nil {
		return ports.Account{}, err
	}
	if !ok {
		return ports.Account{}, fmt.Errorf("margin account %s of %s vanished", a, userID)
	}
	return got, nil
}

func (r accounts) Get(ctx context.Context, userID string, a domain.Account) (ports.Account, bool, error) {
	got, err := scanAccount(r.q.QueryRow(ctx, `SELECT `+accountColumns+` FROM accounts
		WHERE user_id = $1 AND account_type = $2 AND symbol = $3`, userID, a.Type, a.Symbol))
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.Account{}, false, nil
	}
	if err != nil {
		return ports.Account{}, false, fmt.Errorf("load margin account: %w", err)
	}
	return got, true, nil
}

func (r accounts) query(ctx context.Context, sql string, args ...any) ([]ports.Account, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query margin accounts: %w", err)
	}
	defer rows.Close()
	var out []ports.Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("scan margin account: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r accounts) OfUser(ctx context.Context, userID string) ([]ports.Account, error) {
	return r.query(ctx, `SELECT `+accountColumns+` FROM accounts WHERE user_id = $1 ORDER BY account_type, symbol`, userID)
}

func (r accounts) WithDebt(ctx context.Context) ([]ports.Account, error) {
	return r.query(ctx, `SELECT `+accountColumns+` FROM accounts a WHERE status <> 'NORMAL' OR EXISTS (SELECT 1 FROM loans l
		WHERE l.user_id = a.user_id AND l.account_type = a.account_type AND l.symbol = a.symbol AND (l.principal > 0 OR l.interest > 0))
		ORDER BY user_id, account_type, symbol`)
}

func (r accounts) List(ctx context.Context, f ports.AccountFilter, limit int) ([]ports.Account, error) {
	var user any // NULL: every user
	if f.UserID != "" {
		user = f.UserID
	}
	return r.query(ctx, `SELECT `+accountColumns+` FROM accounts WHERE ($1::uuid IS NULL OR user_id = $1::uuid)
		AND ($2 = '' OR account_type = $2) AND ($3 = '' OR symbol = $3) AND ($4 = '' OR status = $4)
		ORDER BY created_at, user_id, account_type, symbol LIMIT $5`, user, string(f.Type), f.Symbol, string(f.Status), limit)
}

func (r accounts) IsolatedCounts(ctx context.Context) (map[string]int, error) {
	rows, err := r.q.Query(ctx, `SELECT symbol, count(*) FROM accounts WHERE account_type = 'MARGIN_ISOLATED' GROUP BY symbol`)
	if err != nil {
		return nil, fmt.Errorf("count isolated accounts: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var symbol string
		var n int
		if err := rows.Scan(&symbol, &n); err != nil {
			return nil, fmt.Errorf("scan isolated accounts: %w", err)
		}
		out[symbol] = n
	}
	return out, rows.Err()
}

func (r accounts) Update(ctx context.Context, a ports.Account) error {
	tag, err := r.q.Exec(ctx, `UPDATE accounts SET status = $4, warned_at = $5, frozen_reason = $6, frozen_by = $7, frozen_at = $8,
		updated_at = $9 WHERE user_id = $1 AND account_type = $2 AND symbol = $3`,
		a.UserID, a.Account.Type, a.Account.Symbol, a.Status, nullTime(a.WarnedAt), a.FrozenReason, a.FrozenBy, nullTime(a.FrozenAt),
		a.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update margin account: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("update margin account %s of %s: no row", a.Account, a.UserID)
	}
	return nil
}

type loans repos

const loanColumns = `user_id, account_type, symbol, asset, principal, interest, opened_at, updated_at`

func scanLoan(row pgx.Row) (ports.Loan, error) {
	var l ports.Loan
	var accountType, symbol string
	var opened *time.Time
	if err := row.Scan(&l.UserID, &accountType, &symbol, &l.Asset, &l.Principal, &l.Interest, &opened, &l.UpdatedAt); err != nil {
		return ports.Loan{}, err
	}
	l.Account, l.OpenedAt = account(accountType, symbol), timeOf(opened)
	return l, nil
}

func (r loans) Get(ctx context.Context, userID string, a domain.Account, asset string) (ports.Loan, error) {
	l, err := scanLoan(r.q.QueryRow(ctx, `SELECT `+loanColumns+` FROM loans
		WHERE user_id = $1 AND account_type = $2 AND symbol = $3 AND asset = $4`, userID, a.Type, a.Symbol, asset))
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.Loan{UserID: userID, Account: a, Asset: asset, Principal: decimal.Zero, Interest: decimal.Zero}, nil
	}
	if err != nil {
		return ports.Loan{}, fmt.Errorf("load loan: %w", err)
	}
	return l, nil
}

func (r loans) Add(ctx context.Context, userID string, a domain.Account, asset string, principal, interest decimal.Decimal, at time.Time) error {
	// The row first at zero, then the change: an INSERT ... ON CONFLICT
	// checks the proposed row's constraints before it finds the conflict,
	// so a repayment's negative deltas cannot go in its VALUES.
	if _, err := r.q.Exec(ctx, `INSERT INTO loans (user_id, account_type, symbol, asset, updated_at) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT DO NOTHING`, userID, a.Type, a.Symbol, asset, at); err != nil {
		return fmt.Errorf("create loan: %w", err)
	}
	_, err := r.q.Exec(ctx, `UPDATE loans SET principal = principal + $5, interest = interest + $6, updated_at = $7,
		opened_at = CASE WHEN principal = 0 AND interest = 0 AND principal + $5 + interest + $6 > 0 THEN $7 ELSE opened_at END
		WHERE user_id = $1 AND account_type = $2 AND symbol = $3 AND asset = $4`,
		userID, a.Type, a.Symbol, asset, principal, interest, at)
	if err != nil {
		return fmt.Errorf("update loan: %w", err)
	}
	return nil
}

func (r loans) query(ctx context.Context, sql string, args ...any) ([]ports.Loan, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query loans: %w", err)
	}
	defer rows.Close()
	var out []ports.Loan
	for rows.Next() {
		l, err := scanLoan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan loan: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r loans) OfUser(ctx context.Context, userID string) ([]ports.Loan, error) {
	return r.query(ctx, `SELECT `+loanColumns+` FROM loans WHERE user_id = $1 AND (principal > 0 OR interest > 0)
		ORDER BY account_type, symbol, asset`, userID)
}

func (r loans) UserOwed(ctx context.Context, userID, asset string) (decimal.Decimal, error) {
	var owed decimal.Decimal
	if err := r.q.QueryRow(ctx, `SELECT coalesce(sum(principal), 0) FROM loans WHERE user_id = $1 AND asset = $2`,
		userID, asset).Scan(&owed); err != nil {
		return decimal.Zero, fmt.Errorf("sum the user's loans: %w", err)
	}
	return owed, nil
}

func (r loans) Open(ctx context.Context, asset string) ([]ports.Loan, error) {
	return r.query(ctx, `SELECT `+loanColumns+` FROM loans WHERE (principal > 0 OR interest > 0) AND ($1 = '' OR asset = $1)
		ORDER BY user_id, account_type, symbol, asset`, asset)
}

func (r loans) Totals(ctx context.Context) (map[string]ports.LoanTotal, error) {
	rows, err := r.q.Query(ctx, `SELECT asset, sum(principal), sum(interest), count(*) FROM loans
		WHERE principal > 0 OR interest > 0 GROUP BY asset`)
	if err != nil {
		return nil, fmt.Errorf("sum loans: %w", err)
	}
	defer rows.Close()
	out := map[string]ports.LoanTotal{}
	for rows.Next() {
		var asset string
		var t ports.LoanTotal
		if err := rows.Scan(&asset, &t.Principal, &t.Interest, &t.Borrowers); err != nil {
			return nil, fmt.Errorf("scan loan totals: %w", err)
		}
		out[asset] = t
	}
	return out, rows.Err()
}

func scanChange(row pgx.Row) (ports.LoanChange, error) {
	var c ports.LoanChange
	var reason, order, liquidation *string
	if err := row.Scan(&c.ID, &c.Asset, &c.Kind, &c.Status, &c.Amount, &c.PrincipalPart, &c.InterestPart, &reason, &order,
		&liquidation, &c.JournalKey, &c.CreatedAt); err != nil {
		return ports.LoanChange{}, err
	}
	c.Reason, c.OrderID, c.LiquidationID = stringOf(reason), stringOf(order), stringOf(liquidation)
	return c, nil
}

func (r loans) Changes(ctx context.Context, userID string, a domain.Account, limit int) ([]ports.LoanChange, error) {
	rows, err := r.q.Query(ctx, `SELECT id, asset, kind, status, amount, principal_part, interest_part, reason, order_id::text,
		liquidation_id::text, journal_key, created_at FROM loan_changes WHERE user_id = $1 AND account_type = $2 AND symbol = $3
		ORDER BY created_at DESC, id DESC LIMIT $4`, userID, a.Type, a.Symbol, limit)
	return collect(rows, err, "loan changes", scanChange)
}

type runs repos

func (r runs) Record(ctx context.Context, started time.Time, check string, mismatches int, details []byte) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO reconciliation_runs (started_at, check_name, mismatches, details) VALUES ($1, $2, $3, $4)`,
		started, check, mismatches, details); err != nil {
		return fmt.Errorf("record reconciliation: %w", err)
	}
	return nil
}

type reservations repos

const reservationColumns = `order_id, user_id, account_type, symbol, side_effect, borrowed, borrow_id, margin_level, request_hash,
	created_at`

func (r reservations) Get(ctx context.Context, orderID string) (ports.Reservation, bool, error) {
	var res ports.Reservation
	var borrow *string
	err := r.q.QueryRow(ctx, `SELECT `+reservationColumns+` FROM order_reservations WHERE order_id = $1`, orderID).Scan(&res.OrderID,
		&res.UserID, &res.AccountType, &res.Symbol, &res.SideEffect, &res.Borrowed, &borrow, &res.MarginLevel, &res.RequestHash,
		&res.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.Reservation{}, false, nil
	}
	if err != nil {
		return ports.Reservation{}, false, fmt.Errorf("load order reservation: %w", err)
	}
	res.BorrowID = stringOf(borrow)
	return res, true, nil
}

func (r reservations) Insert(ctx context.Context, res ports.Reservation) (ports.Reservation, error) {
	if _, err := r.q.Exec(ctx, `INSERT INTO order_reservations (`+reservationColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (order_id) DO NOTHING`, res.OrderID, res.UserID, res.AccountType, res.Symbol, res.SideEffect, res.Borrowed,
		nullUUID(res.BorrowID), res.MarginLevel, res.RequestHash, res.CreatedAt); err != nil {
		return ports.Reservation{}, fmt.Errorf("insert order reservation: %w", err)
	}
	stored, ok, err := r.Get(ctx, res.OrderID)
	if err != nil {
		return ports.Reservation{}, err
	}
	if !ok {
		return ports.Reservation{}, fmt.Errorf("order reservation %s vanished", res.OrderID)
	}
	return stored, nil
}
