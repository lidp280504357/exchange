package analytics

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"

	marketv1 "github.com/skill/exchange/api/gen/go/exchange/market/v1"
	"github.com/skill/exchange/internal/platform/event"
)

func TestLiquidationRows(t *testing.T) {
	at := time.Date(2026, 10, 6, 12, 30, 1, 893_000_000, time.UTC)
	ok := &marketv1.LiquidationOccurred{
		Symbol: "BTC-USD-PERP", PositionSide: "SHORT", Price: "9425.5", AveragePrice: "9496.5", Quantity: "3", ValueUsd: "300",
		TradedAt: timestamppb.New(at),
	}
	row, err := liquidationRow(delivery(t, event.TopicMarketLiquidations, ok, at))
	if err != nil {
		t.Fatal(err)
	}
	want := []any{
		"BTC-USD-PERP", "SHORT", decimal.RequireFromString("9425.5"), decimal.RequireFromString("9496.5"),
		decimal.NewFromInt(3), decimal.NewFromInt(300), at,
	}
	if len(row) != len(want) {
		t.Fatalf("row %v", row)
	}
	for i := range want {
		switch w := want[i].(type) {
		case decimal.Decimal:
			if v, isDec := row[i].(decimal.Decimal); !isDec || !v.Equal(w) {
				t.Fatalf("column %d: %v, want %v", i, row[i], w)
			}
		case time.Time:
			if v, isTime := row[i].(time.Time); !isTime || !v.Equal(w) {
				t.Fatalf("column %d: %v, want %v", i, row[i], w)
			}
		default:
			if row[i] != w {
				t.Fatalf("column %d: %v, want %v", i, row[i], w)
			}
		}
	}

	// Another topic or event: no row.
	if row, err := liquidationRow(delivery(t, event.TopicMarketTrades, ok, at)); row != nil || err != nil {
		t.Fatalf("another topic: %v %v", row, err)
	}
	if row, err := liquidationRow(delivery(t, event.TopicMarketLiquidations, &marketv1.TradesPrinted{}, at)); row != nil || err != nil {
		t.Fatalf("another event: %v %v", row, err)
	}
	// Malformed: no position side, a bad amount, no time.
	for _, bad := range []*marketv1.LiquidationOccurred{
		{Symbol: "BTC-USDT-PERP", PositionSide: "BOTH", Price: "1", AveragePrice: "1", Quantity: "1", ValueUsd: "1", TradedAt: timestamppb.New(at)},
		{Symbol: "BTC-USDT-PERP", PositionSide: "LONG", Price: "x", AveragePrice: "1", Quantity: "1", ValueUsd: "1", TradedAt: timestamppb.New(at)},
		{Symbol: "BTC-USDT-PERP", PositionSide: "LONG", Price: "1", AveragePrice: "1", Quantity: "1", ValueUsd: "1"},
	} {
		if _, err := liquidationRow(delivery(t, event.TopicMarketLiquidations, bad, at)); !errors.Is(err, errMalformed) {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
}
