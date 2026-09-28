// Package postgres stores market data in the market schema.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/lidp280504357/exchange/internal/marketdata/domain"
	"github.com/lidp280504357/exchange/internal/marketdata/ports"
	"github.com/lidp280504357/exchange/internal/platform/pg"
)

// Store implements ports.Store.
type Store struct{ db *pg.DB }

// NewStore returns a store on db.
func NewStore(db *pg.DB) *Store { return &Store{db: db} }

// Tx runs fn in a transaction.
func (s *Store) Tx(ctx context.Context, fn func(ports.Repos) error) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error { return fn(repos{q: tx}) })
}

// Read returns repositories on the pool.
func (s *Store) Read() ports.Repos { return repos{q: s.db} }

type repos struct{ q pg.Querier }

func (r repos) Symbols() ports.SymbolRepo { return symbols(r) }
func (r repos) Candles() ports.CandleRepo { return candles(r) }
func (r repos) Trades() ports.TradeRepo   { return trades(r) }

type symbols repos

func (r symbols) All(ctx context.Context) ([]ports.SymbolState, error) {
	rows, err := r.q.Query(ctx, `SELECT symbol, last_sequence, last_price, last_trade_at FROM symbols ORDER BY symbol`)
	if err != nil {
		return nil, fmt.Errorf("load symbols: %w", err)
	}
	defer rows.Close()
	var out []ports.SymbolState
	for rows.Next() {
		var s ports.SymbolState
		if err := rows.Scan(&s.Symbol, &s.Sequence, &s.LastPrice, &s.LastAt); err != nil {
			return nil, fmt.Errorf("load symbols: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r symbols) Save(ctx context.Context, s ports.SymbolState) error {
	_, err := r.q.Exec(ctx, `INSERT INTO symbols (symbol, last_sequence, last_price, last_trade_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (symbol) DO UPDATE SET last_sequence = $2, last_price = $3, last_trade_at = $4, updated_at = now()`,
		s.Symbol, s.Sequence, s.LastPrice, s.LastAt)
	if err != nil {
		return fmt.Errorf("save symbol: %w", err)
	}
	return nil
}

type candles repos

const candleColumns = `symbol, interval, open_time, open, high, low, close, volume, quote_volume, trade_count`

func scanCandle(row pgx.Row) (domain.Candle, error) {
	var c domain.Candle
	var interval string
	err := row.Scan(&c.Symbol, &interval, &c.OpenTime, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume, &c.QuoteVolume, &c.Trades)
	c.Interval, c.OpenTime = domain.Interval(interval), c.OpenTime.UTC()
	return c, err
}

func (r candles) Upsert(ctx context.Context, list []domain.Candle) error {
	if len(list) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, c := range list {
		batch.Queue(`INSERT INTO candles (`+candleColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (symbol, interval, open_time) DO UPDATE SET open = $4, high = $5, low = $6, close = $7, volume = $8,
				quote_volume = $9, trade_count = $10, updated_at = now()`,
			c.Symbol, string(c.Interval), c.OpenTime, c.Open, c.High, c.Low, c.Close, c.Volume, c.QuoteVolume, c.Trades)
	}
	if err := r.q.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("upsert candles: %w", err)
	}
	return nil
}

func (r candles) Latest(ctx context.Context, symbol string) ([]domain.Candle, error) {
	return r.query(ctx, `SELECT DISTINCT ON (interval) `+candleColumns+` FROM candles WHERE symbol = $1
		ORDER BY interval, open_time DESC`, symbol)
}

func (r candles) Range(ctx context.Context, symbol string, i domain.Interval, from, to time.Time) ([]domain.Candle, error) {
	return r.query(ctx, `SELECT `+candleColumns+` FROM candles WHERE symbol = $1 AND interval = $2 AND open_time >= $3 AND open_time < $4
		ORDER BY open_time`, symbol, string(i), from, to)
}

func (r candles) Before(ctx context.Context, symbol string, i domain.Interval, t time.Time) (*domain.Candle, error) {
	c, err := scanCandle(r.q.QueryRow(ctx, `SELECT `+candleColumns+` FROM candles WHERE symbol = $1 AND interval = $2 AND open_time < $3
		ORDER BY open_time DESC LIMIT 1`, symbol, string(i), t))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("candle before: %w", err)
	}
	return &c, nil
}

func (r candles) query(ctx context.Context, sql string, args ...any) ([]domain.Candle, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("load candles: %w", err)
	}
	defer rows.Close()
	var out []domain.Candle
	for rows.Next() {
		c, err := scanCandle(rows)
		if err != nil {
			return nil, fmt.Errorf("load candles: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type trades repos

func (r trades) Insert(ctx context.Context, list []domain.Trade) error {
	if len(list) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, t := range list {
		batch.Queue(`INSERT INTO trades (symbol, sequence, trade_id, trade_number, price, quantity, quote_quantity, taker_side, executed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) ON CONFLICT (symbol, sequence) DO NOTHING`,
			t.Symbol, t.Sequence, t.ID, t.Number, t.Price, t.Quantity, t.Quote, t.TakerSide, t.At)
	}
	if err := r.q.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("insert trades: %w", err)
	}
	return nil
}

func (r trades) Recent(ctx context.Context, symbol string, limit int) ([]domain.Trade, error) {
	rows, err := r.q.Query(ctx, `SELECT symbol, sequence, trade_id, trade_number, price, quantity, quote_quantity, taker_side, executed_at
		FROM trades WHERE symbol = $1 ORDER BY sequence DESC LIMIT $2`, symbol, limit)
	if err != nil {
		return nil, fmt.Errorf("recent trades: %w", err)
	}
	defer rows.Close()
	var out []domain.Trade
	for rows.Next() {
		var t domain.Trade
		var id uuid.UUID
		if err := rows.Scan(&t.Symbol, &t.Sequence, &id, &t.Number, &t.Price, &t.Quantity, &t.Quote, &t.TakerSide, &t.At); err != nil {
			return nil, fmt.Errorf("recent trades: %w", err)
		}
		t.ID, t.At = id.String(), t.At.UTC()
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r trades) Purge(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.q.Exec(ctx, `DELETE FROM trades WHERE executed_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("purge trades: %w", err)
	}
	return tag.RowsAffected(), nil
}
