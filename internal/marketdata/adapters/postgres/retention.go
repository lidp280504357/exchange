package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
)

// Retention is market-data-service's part of the history policy (M1, the
// user's decision of 2026-10-10: Postgres keeps the last 15 days), run by
// exchangectl retention. The service purges its fast tables every hour
// with shorter windows (trades and the reference market's candles 7 days,
// the futures statistics at most 15 days, their liquidations a day); the
// rules here bound them at the window should that stop, and take what it
// does not: the platform's candles of less than a day once they closed
// before the cutoff (by their close: the current candle of a longer
// interval opened long ago), the settled funding periods, the reference
// candles an operator's price event changed. The platform's candles of a
// day, a week and a month stay (the coordinator's decision of 19:0x: the
// long charts of the pairs only the platform trades, a row a day), and so
// do the symbols' last sequences (the trades' duplicate check) and the
// halts.
type Retention struct{}

// Schema is market-data-service's schema.
func (Retention) Schema() string { return "market" }

// intraday are the platform's candles the window bounds.
var intraday = []domain.Interval{
	domain.Minute1, domain.Minute3, domain.Minute5, domain.Minute15, domain.Minute30, domain.Hour1, domain.Hour2, domain.Hour4,
	domain.Hour6, domain.Hour12,
}

// closedCandles selects the intraday candles whose close (open time and
// length, fixed for each) is at or before $1.
var closedCandles = func() string {
	ref := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	names, lengths := make([]string, 0, len(intraday)), ""
	for _, i := range intraday {
		names = append(names, "'"+string(i)+"'")
		lengths += fmt.Sprintf(" WHEN '%s' THEN interval '%d seconds'", i, int64(i.Next(ref).Sub(ref)/time.Second))
	}
	return "interval IN (" + strings.Join(names, ", ") + ") AND open_time + CASE interval" + lengths + " END <= $1"
}()

// kept are the tables of current state, reported with their size.
var kept = []string{"symbols", "feed_halts", "sim_halts", "sim_heartbeats"}

// Run deletes, a batch at a time, or with DryRun counts, what the window
// keeps no longer, then reports the tables it keeps whole.
func (Retention) Run(ctx context.Context, db *pg.DB, w retention.Window) ([]retention.Result, error) {
	cut := w.History()
	out, err := retention.ApplyAll(ctx, db, w, []retention.Rule{
		{Table: "candles", Name: "less than a day, closed before the cutoff (a day, a week, a month kept)", Cutoff: cut, Where: closedCandles},
		{Table: "funding_periods", Name: "settled, funding time before the cutoff", Cutoff: cut, Where: "settled_at IS NOT NULL AND funding_time < $1"},
		{
			Table: "reference_candles", Name: "opened before the cutoff, a price event's too (the service keeps the others 7 days)", Cutoff: cut,
			Where: "open_time < $1",
		},
		{Table: "trades", Name: "executed before the cutoff (the service keeps 7 days)", Cutoff: cut, Where: "executed_at < $1"},
		{Table: "futures_stats", Name: "points before the cutoff (the service keeps at most 15 days)", Cutoff: cut, Where: "ts < $1"},
		{Table: "futures_liquidations", Name: "traded before the cutoff (the service keeps a day)", Cutoff: cut, Where: "traded_at < $1"},
	})
	if err != nil {
		return out, err
	}
	for _, t := range kept {
		rows, bytes, err := retention.Stats(ctx, db, t)
		if err != nil {
			return out, err
		}
		out = append(out, retention.Result{Table: t, Rule: "kept: current state", Total: rows, Bytes: bytes})
	}
	return out, nil
}
