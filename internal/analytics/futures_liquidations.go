package analytics

import (
	"context"
	"fmt"

	marketv1 "github.com/skill/exchange/api/gen/go/exchange/market/v1"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/kafka"
)

// The reference market's liquidation orders on the platform's contracts
// (design 2026-10-06 §3.3): market.liquidations' LiquidationOccurred into
// futures_liquidations (migrations/clickhouse/00011_coin_margined.sql),
// kept 7 days. Derived market data with a consumer group of its own
// (LiquidationsGroup): not in the events table, nothing to backfill.

// LiquidationsGroup consumes LiquidationTopics.
const LiquidationsGroup = "analytics-liquidations"

// LiquidationTopics feed futures_liquidations.
var LiquidationTopics = []string{event.TopicMarketLiquidations}

// insertFuturesLiquidations writes the table drafted with a liquidation
// order's fields: side is the side of the position closed (LONG, SHORT),
// quantity and filled_quantity are both what filled, status is FILLED
// (the event carries what filled alone).
const insertFuturesLiquidations = `INSERT INTO futures_liquidations (symbol, side, price, average_price, quantity, filled_quantity,
	value_usd, status, traded_at)`

// StoreLiquidations writes a batch of LiquidationOccurred; malformed ones
// are counted and skipped. The table is a ReplacingMergeTree keyed by the
// order's fields, so a batch stored again collapses.
func (in *Ingestor) StoreLiquidations(ctx context.Context, batch []kafka.Delivery) error {
	var rows [][]any
	for _, d := range batch {
		row, err := liquidationRow(d)
		if err != nil {
			in.rejected.Inc()
			in.log.WarnContext(ctx, "liquidation dropped", "topic", d.Topic, "event_id", d.Envelope.GetEventId(), "error", err)
			continue
		}
		if row != nil {
			rows = append(rows, row)
		}
	}
	return in.insert(ctx, insertFuturesLiquidations, rows)
}

// liquidationRow is a delivery's futures_liquidations row; nil for another
// event.
func liquidationRow(d kafka.Delivery) ([]any, error) {
	var l marketv1.LiquidationOccurred
	if d.Topic != event.TopicMarketLiquidations || !d.Envelope.GetPayload().MessageIs(&l) {
		return nil, nil
	}
	if err := d.Envelope.GetPayload().UnmarshalTo(&l); err != nil {
		return nil, fmt.Errorf("%w: payload: %w", errMalformed, err)
	}
	side := l.GetPositionSide()
	if l.GetSymbol() == "" || (side != "LONG" && side != "SHORT") || l.GetTradedAt() == nil {
		return nil, fmt.Errorf("%w: liquidation %q %q", errMalformed, l.GetSymbol(), side)
	}
	var errs []error
	parse := func(s string) any {
		v, err := amount(s)
		errs = append(errs, err)
		return v
	}
	price, avg, qty, value := parse(l.GetPrice()), parse(l.GetAveragePrice()), parse(l.GetQuantity()), parse(l.GetValueUsd())
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return []any{l.GetSymbol(), side, price, avg, qty, qty, value, "FILLED", l.GetTradedAt().AsTime().UTC()}, nil
}
