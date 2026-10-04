package application

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketdata/domain"
)

func TestSparklinesAreKeptAndCut(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	s := &Sparklines{
		Now: func() time.Time { return now },
		TTL: 5 * time.Minute,
		Closes: func(_ context.Context, symbol string, hours int) ([]domain.Candle, error) {
			calls.Add(1)
			if symbol == "NOPE-USDT" {
				return nil, errors.New("not listed")
			}
			out := make([]domain.Candle, hours)
			for i := range out {
				out[i].Close = decimal.NewFromInt(int64(i))
			}
			return out, nil
		},
	}
	ctx := context.Background()
	week := s.Get(ctx, []string{"BTC-USDT", "ETH-USDT", "NOPE-USDT"}, SparkWeek)
	if len(week) != 2 || len(week["BTC-USDT"]) != SparkWeekPoints || week["BTC-USDT"][0] != "0" || week["BTC-USDT"][SparkWeekPoints-1] != "167" {
		t.Fatalf("week: %v", week)
	}
	if _, ok := week["NOPE-USDT"]; ok {
		t.Fatal("a symbol without candles has a line")
	}
	day := s.Get(ctx, []string{"BTC-USDT"}, SparkDay)
	if got := day["BTC-USDT"]; len(got) != 24 || got[0] != strconv.Itoa(168-24) || got[23] != "167" {
		t.Fatalf("day: %v", got)
	}
	if calls.Load() != 3 {
		t.Fatalf("%d source calls, want 3 (the day comes from the kept week)", calls.Load())
	}
	now = now.Add(6 * time.Minute)
	s.Get(ctx, []string{"BTC-USDT"}, SparkWeek)
	if calls.Load() != 4 {
		t.Fatalf("%d source calls after the TTL, want 4", calls.Load())
	}
}
