package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketdata/ports"
	"github.com/skill/exchange/internal/platform/pg"
)

// FuturesStats implements ports.FuturesStatsRepo on futures_stats.
type FuturesStats struct{ q pg.Querier }

// NewFuturesStats returns the futures statistics on db.
func NewFuturesStats(db *pg.DB) *FuturesStats { return &FuturesStats{q: db} }

// Upsert writes points, replacing stored ones.
func (r *FuturesStats) Upsert(ctx context.Context, stats []ports.FuturesStat) error {
	if len(stats) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, s := range stats {
		data, err := json.Marshal(s.Values)
		if err != nil {
			return fmt.Errorf("upsert futures stats: %w", err)
		}
		batch.Queue(`INSERT INTO futures_stats (symbol, metric, period, ts, data) VALUES ($1, $2, $3, $4, $5::jsonb)
			ON CONFLICT (symbol, metric, period, ts) DO UPDATE SET data = EXCLUDED.data
			WHERE futures_stats.data IS DISTINCT FROM EXCLUDED.data`,
			s.Symbol, s.Metric, s.Period, s.At, string(data))
	}
	if err := r.q.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("upsert futures stats: %w", err)
	}
	return nil
}

// Last returns the time of a series' latest point, zero without one.
func (r *FuturesStats) Last(ctx context.Context, symbol, metric, period string) (time.Time, error) {
	var at *time.Time
	if err := r.q.QueryRow(ctx, `SELECT max(ts) FROM futures_stats WHERE symbol = $1 AND metric = $2 AND period = $3`,
		symbol, metric, period).Scan(&at); err != nil {
		return time.Time{}, fmt.Errorf("last futures stat: %w", err)
	}
	if at == nil {
		return time.Time{}, nil
	}
	return at.UTC(), nil
}

// Recent returns a series' latest limit points, oldest first.
func (r *FuturesStats) Recent(ctx context.Context, symbol, metric, period string, limit int) ([]ports.FuturesStat, error) {
	rows, err := r.q.Query(ctx, `SELECT ts, data FROM futures_stats WHERE symbol = $1 AND metric = $2 AND period = $3
		ORDER BY ts DESC LIMIT $4`, symbol, metric, period, limit)
	if err != nil {
		return nil, fmt.Errorf("recent futures stats: %w", err)
	}
	defer rows.Close()
	out := []ports.FuturesStat{}
	for rows.Next() {
		s := ports.FuturesStat{Symbol: symbol, Metric: metric, Period: period}
		var data map[string]decimal.Decimal
		if err := rows.Scan(&s.At, &data); err != nil {
			return nil, fmt.Errorf("recent futures stats: %w", err)
		}
		s.At, s.Values = s.At.UTC(), data
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recent futures stats: %w", err)
	}
	slices.Reverse(out)
	return out, nil
}

// Purge deletes the points of a period older than before.
func (r *FuturesStats) Purge(ctx context.Context, period string, before time.Time) (int64, error) {
	tag, err := r.q.Exec(ctx, `DELETE FROM futures_stats WHERE period = $1 AND ts < $2`, period, before)
	if err != nil {
		return 0, fmt.Errorf("purge futures stats: %w", err)
	}
	return tag.RowsAffected(), nil
}
