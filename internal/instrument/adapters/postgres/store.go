// Package postgres stores instrument-service's data in the instrument schema.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	"github.com/lidp280504357/exchange/internal/instrument/domain"
	"github.com/lidp280504357/exchange/internal/instrument/ports"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/outbox"
	"github.com/lidp280504357/exchange/internal/platform/pg"
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

type repos struct {
	q      pg.Querier
	events *event.Factory
}

func (r repos) FeeSchedules() ports.FeeRepo { return fees(r) }
func (r repos) Assets() ports.AssetRepo     { return assets(r) }
func (r repos) Networks() ports.NetworkRepo { return networks(r) }
func (r repos) Pairs() ports.PairRepo       { return pairs(r) }

func (r repos) Record(ctx context.Context, entity, key string, version int64, value any, actor, reason string) error {
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("history value: %w", err)
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO config_history (entity, key, version, value, actor, reason) VALUES ($1, $2, $3, $4, $5, $6)`,
		entity, key, version, b, actor, reason); err != nil {
		return fmt.Errorf("record history: %w", err)
	}
	return nil
}

func (r repos) Emit(ctx context.Context, msg proto.Message, aggregateType, aggregateID string) error {
	env, err := r.events.New(ctx, msg, aggregateType, aggregateID)
	if err != nil {
		return err
	}
	return outbox.Add(ctx, r.q, event.TopicInstrument, env)
}

// one runs a single-row query; no row gives (nil, nil).
func one[T any](row pgx.Row, scan func(pgx.Row) (T, error)) (*T, error) {
	v, err := scan(row)
	if pg.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func all[T any](rows pgx.Rows, err error, scan func(pgx.Row) (T, error)) ([]T, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type fees repos

const feeColumns = `tier, maker_fee_rate, taker_fee_rate, version`

func scanFee(row pgx.Row) (domain.FeeSchedule, error) {
	var f domain.FeeSchedule
	err := row.Scan(&f.Tier, &f.MakerFeeRate, &f.TakerFeeRate, &f.Version)
	return f, err
}

func (r fees) Get(ctx context.Context, tier string) (*domain.FeeSchedule, error) {
	return one(r.q.QueryRow(ctx, `SELECT `+feeColumns+` FROM fee_schedules WHERE tier = $1`, tier), scanFee)
}

func (r fees) List(ctx context.Context) ([]domain.FeeSchedule, error) {
	rows, err := r.q.Query(ctx, `SELECT `+feeColumns+` FROM fee_schedules ORDER BY tier`)
	return all(rows, err, scanFee)
}

func (r fees) Save(ctx context.Context, f domain.FeeSchedule) (domain.FeeSchedule, error) {
	return scanFee(r.q.QueryRow(ctx, `INSERT INTO fee_schedules (tier, maker_fee_rate, taker_fee_rate) VALUES ($1, $2, $3)
		ON CONFLICT (tier) DO UPDATE SET maker_fee_rate = EXCLUDED.maker_fee_rate, taker_fee_rate = EXCLUDED.taker_fee_rate,
		version = fee_schedules.version + 1, updated_at = now()
		RETURNING `+feeColumns, f.Tier, f.MakerFeeRate, f.TakerFeeRate))
}

type assets repos

const assetColumns = `asset_code, name, decimals, deposit_enabled, withdraw_enabled, trading_enabled, risk_restricted, version`

func scanAsset(row pgx.Row) (domain.Asset, error) {
	var a domain.Asset
	err := row.Scan(&a.Code, &a.Name, &a.Decimals, &a.DepositEnabled, &a.WithdrawEnabled, &a.TradingEnabled, &a.RiskRestricted, &a.Version)
	return a, err
}

func (r assets) Get(ctx context.Context, code string) (*domain.Asset, error) {
	return one(r.q.QueryRow(ctx, `SELECT `+assetColumns+` FROM assets WHERE asset_code = $1`, code), scanAsset)
}

func (r assets) List(ctx context.Context) ([]domain.Asset, error) {
	rows, err := r.q.Query(ctx, `SELECT `+assetColumns+` FROM assets ORDER BY asset_code`)
	return all(rows, err, scanAsset)
}

func (r assets) Save(ctx context.Context, a domain.Asset) (domain.Asset, error) {
	return scanAsset(r.q.QueryRow(ctx, `INSERT INTO assets (asset_code, name, decimals, deposit_enabled, withdraw_enabled,
		trading_enabled, risk_restricted) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (asset_code) DO UPDATE SET name = EXCLUDED.name, decimals = EXCLUDED.decimals,
		deposit_enabled = EXCLUDED.deposit_enabled, withdraw_enabled = EXCLUDED.withdraw_enabled,
		trading_enabled = EXCLUDED.trading_enabled, risk_restricted = EXCLUDED.risk_restricted,
		version = assets.version + 1, updated_at = now()
		RETURNING `+assetColumns, a.Code, a.Name, a.Decimals, a.DepositEnabled, a.WithdrawEnabled, a.TradingEnabled, a.RiskRestricted))
}

type networks repos

const networkColumns = `asset_code, network, chain, contract_address, confirmations, min_deposit, min_withdraw, withdraw_fee,
	memo_required, deposit_enabled, withdraw_enabled, version`

func scanNetwork(row pgx.Row) (domain.Network, error) {
	var n domain.Network
	err := row.Scan(&n.AssetCode, &n.Network, &n.Chain, &n.ContractAddress, &n.Confirmations, &n.MinDeposit, &n.MinWithdraw,
		&n.WithdrawFee, &n.MemoRequired, &n.DepositEnabled, &n.WithdrawEnabled, &n.Version)
	return n, err
}

func (r networks) Get(ctx context.Context, asset, network string) (*domain.Network, error) {
	return one(r.q.QueryRow(ctx, `SELECT `+networkColumns+` FROM networks WHERE asset_code = $1 AND network = $2`, asset, network), scanNetwork)
}

func (r networks) List(ctx context.Context) ([]domain.Network, error) {
	rows, err := r.q.Query(ctx, `SELECT `+networkColumns+` FROM networks ORDER BY asset_code, network`)
	return all(rows, err, scanNetwork)
}

func (r networks) Save(ctx context.Context, n domain.Network) (domain.Network, error) {
	return scanNetwork(r.q.QueryRow(ctx, `INSERT INTO networks (asset_code, network, chain, contract_address, confirmations,
		min_deposit, min_withdraw, withdraw_fee, memo_required, deposit_enabled, withdraw_enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (asset_code, network) DO UPDATE SET chain = EXCLUDED.chain, contract_address = EXCLUDED.contract_address,
		confirmations = EXCLUDED.confirmations, min_deposit = EXCLUDED.min_deposit, min_withdraw = EXCLUDED.min_withdraw,
		withdraw_fee = EXCLUDED.withdraw_fee, memo_required = EXCLUDED.memo_required,
		deposit_enabled = EXCLUDED.deposit_enabled, withdraw_enabled = EXCLUDED.withdraw_enabled,
		version = networks.version + 1, updated_at = now()
		RETURNING `+networkColumns, n.AssetCode, n.Network, n.Chain, n.ContractAddress, n.Confirmations, n.MinDeposit,
		n.MinWithdraw, n.WithdrawFee, n.MemoRequired, n.DepositEnabled, n.WithdrawEnabled))
}

type pairs repos

const pairColumns = `symbol, base_asset, quote_asset, tick_size, lot_size, min_quantity, max_quantity, min_notional,
	price_band, fee_tier, status, version`

func scanPair(row pgx.Row) (domain.TradingPair, error) {
	var p domain.TradingPair
	err := row.Scan(&p.Symbol, &p.BaseAsset, &p.QuoteAsset, &p.TickSize, &p.LotSize, &p.MinQuantity, &p.MaxQuantity,
		&p.MinNotional, &p.PriceBand, &p.FeeTier, &p.Status, &p.Version)
	return p, err
}

func (r pairs) Get(ctx context.Context, symbol string) (*domain.TradingPair, error) {
	return one(r.q.QueryRow(ctx, `SELECT `+pairColumns+` FROM trading_pairs WHERE symbol = $1`, symbol), scanPair)
}

func (r pairs) GetForUpdate(ctx context.Context, symbol string) (*domain.TradingPair, error) {
	return one(r.q.QueryRow(ctx, `SELECT `+pairColumns+` FROM trading_pairs WHERE symbol = $1 FOR UPDATE`, symbol), scanPair)
}

func (r pairs) List(ctx context.Context) ([]domain.TradingPair, error) {
	rows, err := r.q.Query(ctx, `SELECT `+pairColumns+` FROM trading_pairs ORDER BY symbol`)
	return all(rows, err, scanPair)
}

func (r pairs) Save(ctx context.Context, p domain.TradingPair) (domain.TradingPair, error) {
	return scanPair(r.q.QueryRow(ctx, `INSERT INTO trading_pairs (symbol, base_asset, quote_asset, tick_size, lot_size,
		min_quantity, max_quantity, min_notional, price_band, fee_tier, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (symbol) DO UPDATE SET tick_size = EXCLUDED.tick_size, lot_size = EXCLUDED.lot_size,
		min_quantity = EXCLUDED.min_quantity, max_quantity = EXCLUDED.max_quantity, min_notional = EXCLUDED.min_notional,
		price_band = EXCLUDED.price_band, fee_tier = EXCLUDED.fee_tier, status = EXCLUDED.status,
		version = trading_pairs.version + 1, updated_at = now()
		RETURNING `+pairColumns, p.Symbol, p.BaseAsset, p.QuoteAsset, p.TickSize, p.LotSize, p.MinQuantity, p.MaxQuantity,
		p.MinNotional, p.PriceBand, p.FeeTier, p.Status))
}
