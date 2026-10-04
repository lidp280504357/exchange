// Package postgres stores instrument-service's data in the instrument schema.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/skill/exchange/internal/instrument/domain"
	"github.com/skill/exchange/internal/instrument/ports"
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

type repos struct {
	q      pg.Querier
	events *event.Factory
}

func (r repos) FeeSchedules() ports.FeeRepo { return fees(r) }
func (r repos) Assets() ports.AssetRepo     { return assets(r) }
func (r repos) Networks() ports.NetworkRepo { return networks(r) }
func (r repos) Pairs() ports.PairRepo       { return pairs(r) }

func (r repos) Contracts() ports.ContractRepo { return contracts(r) }

func (r repos) Profiles() ports.ProfileRepo { return profiles(r) }

func (r repos) Record(ctx context.Context, entity, key string, version int64, value any, actor, reason, source string) error {
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("history value: %w", err)
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO config_history (entity, key, version, value, actor, reason, source)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, entity, key, version, b, actor, reason, source); err != nil {
		return fmt.Errorf("record history: %w", err)
	}
	return nil
}

func (r repos) LastEdit(ctx context.Context, entity, key string) (*ports.Edit, error) {
	var e ports.Edit
	err := r.q.QueryRow(ctx, `SELECT source, actor, created_at FROM config_history
		WHERE entity = $1 AND key = $2 AND source IN ('', 'FILE', 'CONSOLE') ORDER BY version DESC LIMIT 1`, entity, key).
		Scan(&e.Source, &e.Actor, &e.At)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("last edit: %w", err)
	}
	if e.Source == "" {
		e.Source = "FILE"
	}
	return &e, nil
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

const assetColumns = `asset_code, name, decimals, deposit_enabled, withdraw_enabled, trading_enabled, risk_restricted, rank,
	categories, hidden, version`

func scanAsset(row pgx.Row) (domain.Asset, error) {
	var a domain.Asset
	err := row.Scan(&a.Code, &a.Name, &a.Decimals, &a.DepositEnabled, &a.WithdrawEnabled, &a.TradingEnabled, &a.RiskRestricted,
		&a.Rank, &a.Categories, &a.Hidden, &a.Version)
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
	categories := a.Categories
	if categories == nil {
		categories = []string{}
	}
	return scanAsset(r.q.QueryRow(ctx, `INSERT INTO assets (asset_code, name, decimals, deposit_enabled, withdraw_enabled,
		trading_enabled, risk_restricted, rank, categories, hidden) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (asset_code) DO UPDATE SET name = EXCLUDED.name, decimals = EXCLUDED.decimals,
		deposit_enabled = EXCLUDED.deposit_enabled, withdraw_enabled = EXCLUDED.withdraw_enabled,
		trading_enabled = EXCLUDED.trading_enabled, risk_restricted = EXCLUDED.risk_restricted,
		rank = EXCLUDED.rank, categories = EXCLUDED.categories, hidden = EXCLUDED.hidden,
		version = assets.version + 1, updated_at = now()
		RETURNING `+assetColumns, a.Code, a.Name, a.Decimals, a.DepositEnabled, a.WithdrawEnabled, a.TradingEnabled, a.RiskRestricted,
		a.Rank, categories, a.Hidden))
}

type profiles repos

const profileColumns = `asset_code, display_name, description, links, logo_mime, coalesce(octet_length(logo), 0), profile_version`

func scanProfile(row pgx.Row) (domain.AssetProfile, error) {
	var p domain.AssetProfile
	var description, links []byte
	if err := row.Scan(&p.Code, &p.DisplayName, &description, &links, &p.LogoMIME, &p.LogoSize, &p.Version); err != nil {
		return p, err
	}
	if err := json.Unmarshal(description, &p.Description); err != nil {
		return p, fmt.Errorf("asset %s description: %w", p.Code, err)
	}
	if err := json.Unmarshal(links, &p.Links); err != nil {
		return p, fmt.Errorf("asset %s links: %w", p.Code, err)
	}
	return p, nil
}

func (r profiles) GetForUpdate(ctx context.Context, code string) (*domain.AssetProfile, error) {
	return one(r.q.QueryRow(ctx, `SELECT `+profileColumns+` FROM assets WHERE asset_code = $1 FOR UPDATE`, code), scanProfile)
}

func (r profiles) List(ctx context.Context) ([]domain.AssetProfile, error) {
	rows, err := r.q.Query(ctx, `SELECT `+profileColumns+` FROM assets ORDER BY asset_code`)
	return all(rows, err, scanProfile)
}

func (r profiles) Logo(ctx context.Context, code string) (*domain.Logo, int64, error) {
	var data []byte
	var mime string
	var version int64
	err := r.q.QueryRow(ctx, `SELECT logo, logo_mime, profile_version FROM assets WHERE asset_code = $1`, code).Scan(&data, &mime, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil || data == nil {
		return nil, version, err
	}
	return &domain.Logo{Data: data, MIME: mime}, version, nil
}

func (r profiles) Save(ctx context.Context, p domain.AssetProfile, logo *domain.Logo) (domain.AssetProfile, error) {
	description, err := json.Marshal(orEmpty(p.Description))
	if err != nil {
		return domain.AssetProfile{}, err
	}
	links, err := json.Marshal(orEmpty(p.Links))
	if err != nil {
		return domain.AssetProfile{}, err
	}
	if logo == nil {
		return scanProfile(r.q.QueryRow(ctx, `UPDATE assets SET display_name = $2, description = $3, links = $4,
			profile_version = profile_version + 1, updated_at = now() WHERE asset_code = $1 RETURNING `+profileColumns,
			p.Code, p.DisplayName, description, links))
	}
	var data []byte
	mime := ""
	if len(logo.Data) > 0 {
		data, mime = logo.Data, logo.MIME
	}
	return scanProfile(r.q.QueryRow(ctx, `UPDATE assets SET display_name = $2, description = $3, links = $4, logo = $5,
		logo_mime = $6, profile_version = profile_version + 1, updated_at = now() WHERE asset_code = $1 RETURNING `+profileColumns,
		p.Code, p.DisplayName, description, links, data, mime))
}

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

type networks repos

const networkColumns = `asset_code, network, chain, contract_address, confirmations, min_deposit, min_withdraw, withdraw_fee,
	memo_required, deposit_enabled, withdraw_enabled, display_name, address_format, eta_minutes, explorer_tx_url,
	explorer_address_url, provider, provider_coin, version`

func scanNetwork(row pgx.Row) (domain.Network, error) {
	var n domain.Network
	err := row.Scan(&n.AssetCode, &n.Network, &n.Chain, &n.ContractAddress, &n.Confirmations, &n.MinDeposit, &n.MinWithdraw,
		&n.WithdrawFee, &n.MemoRequired, &n.DepositEnabled, &n.WithdrawEnabled, &n.DisplayName, &n.AddressFormat, &n.ETAMinutes,
		&n.ExplorerTxURL, &n.ExplorerAddressURL, &n.Provider, &n.ProviderCoin, &n.Version)
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
	if n.AddressFormat == "" {
		n.AddressFormat = domain.FormatEVM
	}
	return scanNetwork(r.q.QueryRow(ctx, `INSERT INTO networks (asset_code, network, chain, contract_address, confirmations,
		min_deposit, min_withdraw, withdraw_fee, memo_required, deposit_enabled, withdraw_enabled, display_name, address_format,
		eta_minutes, explorer_tx_url, explorer_address_url, provider, provider_coin)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		ON CONFLICT (asset_code, network) DO UPDATE SET chain = EXCLUDED.chain, contract_address = EXCLUDED.contract_address,
		confirmations = EXCLUDED.confirmations, min_deposit = EXCLUDED.min_deposit, min_withdraw = EXCLUDED.min_withdraw,
		withdraw_fee = EXCLUDED.withdraw_fee, memo_required = EXCLUDED.memo_required,
		deposit_enabled = EXCLUDED.deposit_enabled, withdraw_enabled = EXCLUDED.withdraw_enabled,
		display_name = EXCLUDED.display_name, address_format = EXCLUDED.address_format, eta_minutes = EXCLUDED.eta_minutes,
		explorer_tx_url = EXCLUDED.explorer_tx_url, explorer_address_url = EXCLUDED.explorer_address_url,
		provider = EXCLUDED.provider, provider_coin = EXCLUDED.provider_coin,
		version = networks.version + 1, updated_at = now()
		RETURNING `+networkColumns, n.AssetCode, n.Network, n.Chain, n.ContractAddress, n.Confirmations, n.MinDeposit,
		n.MinWithdraw, n.WithdrawFee, n.MemoRequired, n.DepositEnabled, n.WithdrawEnabled, n.DisplayName, n.AddressFormat,
		n.ETAMinutes, n.ExplorerTxURL, n.ExplorerAddressURL, n.Provider, n.ProviderCoin))
}

type pairs repos

const pairColumns = `symbol, base_asset, quote_asset, tick_size, lot_size, min_quantity, max_quantity, min_notional,
	price_band, fee_tier, status, reference_symbol, reference_multiplier, listed_at, version`

func scanPair(row pgx.Row) (domain.TradingPair, error) {
	var p domain.TradingPair
	err := row.Scan(&p.Symbol, &p.BaseAsset, &p.QuoteAsset, &p.TickSize, &p.LotSize, &p.MinQuantity, &p.MaxQuantity,
		&p.MinNotional, &p.PriceBand, &p.FeeTier, &p.Status, &p.ReferenceSymbol, &p.ReferenceMultiplier, &p.ListedAt, &p.Version)
	p.ListedAt = p.ListedAt.UTC()
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
	var listed *time.Time // now() when not given
	if !p.ListedAt.IsZero() {
		listed = &p.ListedAt
	}
	multiplier := p.ReferenceMultiplier
	if multiplier.IsZero() {
		multiplier = decimal.NewFromInt(1)
	}
	return scanPair(r.q.QueryRow(ctx, `INSERT INTO trading_pairs (symbol, base_asset, quote_asset, tick_size, lot_size,
		min_quantity, max_quantity, min_notional, price_band, fee_tier, status, reference_symbol, reference_multiplier, listed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, COALESCE($14, now()))
		ON CONFLICT (symbol) DO UPDATE SET tick_size = EXCLUDED.tick_size, lot_size = EXCLUDED.lot_size,
		min_quantity = EXCLUDED.min_quantity, max_quantity = EXCLUDED.max_quantity, min_notional = EXCLUDED.min_notional,
		price_band = EXCLUDED.price_band, fee_tier = EXCLUDED.fee_tier, status = EXCLUDED.status,
		reference_symbol = EXCLUDED.reference_symbol, reference_multiplier = EXCLUDED.reference_multiplier,
		listed_at = COALESCE($14, trading_pairs.listed_at),
		version = trading_pairs.version + 1, updated_at = now()
		RETURNING `+pairColumns, p.Symbol, p.BaseAsset, p.QuoteAsset, p.TickSize, p.LotSize, p.MinQuantity, p.MaxQuantity,
		p.MinNotional, p.PriceBand, p.FeeTier, p.Status, p.ReferenceSymbol, multiplier, listed))
}

type contracts repos

const contractColumns = `symbol, type, base_asset, quote_asset, index_symbol, tick_size, lot_size, min_quantity, max_quantity,
	min_notional, price_band, risk_tiers, funding_interval_hours, interest_rate, funding_cap, impact_notional, fee_tier, status, version`

func scanContract(row pgx.Row) (domain.Contract, error) {
	var c domain.Contract
	var tiers []byte
	err := row.Scan(&c.Symbol, &c.Type, &c.BaseAsset, &c.QuoteAsset, &c.IndexSymbol, &c.TickSize, &c.LotSize, &c.MinQuantity,
		&c.MaxQuantity, &c.MinNotional, &c.PriceBand, &tiers, &c.FundingIntervalHours, &c.InterestRate, &c.FundingCap,
		&c.ImpactNotional, &c.FeeTier, &c.Status, &c.Version)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(tiers, &c.RiskTiers); err != nil {
		return c, fmt.Errorf("contract %s: risk tiers: %w", c.Symbol, err)
	}
	return c, nil
}

func (r contracts) Get(ctx context.Context, symbol string) (*domain.Contract, error) {
	return one(r.q.QueryRow(ctx, `SELECT `+contractColumns+` FROM contracts WHERE symbol = $1`, symbol), scanContract)
}

func (r contracts) GetForUpdate(ctx context.Context, symbol string) (*domain.Contract, error) {
	return one(r.q.QueryRow(ctx, `SELECT `+contractColumns+` FROM contracts WHERE symbol = $1 FOR UPDATE`, symbol), scanContract)
}

func (r contracts) List(ctx context.Context) ([]domain.Contract, error) {
	rows, err := r.q.Query(ctx, `SELECT `+contractColumns+` FROM contracts ORDER BY symbol`)
	return all(rows, err, scanContract)
}

func (r contracts) Save(ctx context.Context, c domain.Contract) (domain.Contract, error) {
	tiers, err := json.Marshal(c.RiskTiers)
	if err != nil {
		return domain.Contract{}, err
	}
	return scanContract(r.q.QueryRow(ctx, `INSERT INTO contracts (symbol, type, base_asset, quote_asset, index_symbol, tick_size,
		lot_size, min_quantity, max_quantity, min_notional, price_band, risk_tiers, funding_interval_hours, interest_rate,
		funding_cap, impact_notional, fee_tier, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		ON CONFLICT (symbol) DO UPDATE SET index_symbol = EXCLUDED.index_symbol, tick_size = EXCLUDED.tick_size,
		lot_size = EXCLUDED.lot_size, min_quantity = EXCLUDED.min_quantity, max_quantity = EXCLUDED.max_quantity,
		min_notional = EXCLUDED.min_notional, price_band = EXCLUDED.price_band, risk_tiers = EXCLUDED.risk_tiers,
		funding_interval_hours = EXCLUDED.funding_interval_hours, interest_rate = EXCLUDED.interest_rate,
		funding_cap = EXCLUDED.funding_cap, impact_notional = EXCLUDED.impact_notional, fee_tier = EXCLUDED.fee_tier,
		status = EXCLUDED.status, version = contracts.version + 1, updated_at = now()
		RETURNING `+contractColumns, c.Symbol, c.Type, c.BaseAsset, c.QuoteAsset, c.IndexSymbol, c.TickSize, c.LotSize,
		c.MinQuantity, c.MaxQuantity, c.MinNotional, c.PriceBand, tiers, c.FundingIntervalHours, c.InterestRate, c.FundingCap,
		c.ImpactNotional, c.FeeTier, c.Status))
}
