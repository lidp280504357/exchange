package analytics_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"

	marketv1 "github.com/skill/exchange/api/gen/go/exchange/market/v1"
	"github.com/skill/exchange/internal/analytics"
	"github.com/skill/exchange/internal/platform/chx"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/kafka"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/migrations"
)

func TestFuturesLiquidations(t *testing.T) {
	ctx := context.Background()
	chCfg := testenv.ClickHouse(t)
	conn, err := chx.Open(ctx, chCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sqlDB := chx.OpenDB(chCfg)
	defer sqlDB.Close()
	if err := migrate.UpClickHouse(ctx, sqlDB, migrations.ClickHouse(), discard); err != nil {
		t.Fatalf("clickhouse migrations: %v", err)
	}
	in := analytics.NewIngestor(conn, discard, prometheus.NewRegistry())

	f := event.NewFactory("test", "t")
	// A symbol of this run alone: the test database is shared.
	symbol := "T" + uuid.NewString()[:6] + "-USDT-PERP"
	at := time.Now().UTC().Truncate(time.Millisecond)
	var batch []kafka.Delivery
	for i, side := range []string{"LONG", "SHORT", "BOTH"} {
		env, err := f.New(ctx, &marketv1.LiquidationOccurred{
			Symbol: symbol, PositionSide: side, Price: "86000.1", AveragePrice: "86010", Quantity: "0.014", ValueUsd: "1204.14",
			TradedAt: timestamppb.New(at.Add(time.Duration(i) * time.Second)),
		}, "symbol", symbol)
		if err != nil {
			t.Fatal(err)
		}
		batch = append(batch, kafka.Delivery{Topic: event.TopicMarketLiquidations, Envelope: env})
	}
	// Twice: the copies collapse; the malformed one is skipped.
	if err := in.StoreLiquidations(ctx, append(batch, batch...)); err != nil {
		t.Fatal(err)
	}
	var rows uint64
	if err := conn.QueryRow(ctx, `SELECT count() FROM futures_liquidations FINAL WHERE symbol = ?`, symbol).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("%d rows", rows)
	}
	var side, status string
	var qty, filled, value decimal.Decimal
	var traded time.Time
	if err := conn.QueryRow(ctx, `SELECT side, quantity, filled_quantity, value_usd, status, traded_at FROM futures_liquidations FINAL
		WHERE symbol = ? ORDER BY traded_at LIMIT 1`, symbol).Scan(&side, &qty, &filled, &value, &status, &traded); err != nil {
		t.Fatal(err)
	}
	if side != "LONG" || qty.String() != "0.014" || !filled.Equal(qty) || value.String() != "1204.14" || status != "FILLED" || !traded.Equal(at) {
		t.Fatalf("row: %s %s %s %s %s %s", side, qty, filled, value, status, traded)
	}
}
