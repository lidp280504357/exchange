package prices

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestLastTradeIsCachedBriefly(t *testing.T) {
	calls := 0
	price := decimal.RequireFromString("70000")
	l := NewLastTrade(func(context.Context, string) (decimal.Decimal, error) {
		calls++
		return price, nil
	}, nil, time.Second)
	now := time.Unix(1_000_000, 0)
	l.now = func() time.Time { return now }
	for range 3 {
		if got, err := l.Anchor(context.Background(), "BTC-USDT"); err != nil || !got.Equal(price) {
			t.Fatalf("anchor %s, %v", got, err)
		}
	}
	if calls != 1 {
		t.Fatalf("%d lookups within the TTL, want 1", calls)
	}
	price = decimal.RequireFromString("70100")
	now = now.Add(time.Second)
	if got, _ := l.Anchor(context.Background(), "BTC-USDT"); !got.Equal(price) || calls != 2 {
		t.Fatalf("after the TTL: %s, %d lookups", got, calls)
	}
	if got, _ := (None{}).Anchor(context.Background(), "BTC-USDT"); !got.IsZero() {
		t.Fatal("None has no anchor")
	}
}

func TestAPairWithoutTradesAnchorsOnTheReference(t *testing.T) {
	none := func(context.Context, string) (decimal.Decimal, error) { return decimal.Zero, nil }
	ref := func(context.Context, string) (decimal.Decimal, error) { return decimal.RequireFromString("83900"), nil }
	if got, err := NewLastTrade(none, ref, time.Second).Anchor(context.Background(), "BTC-USDT"); err != nil || got.String() != "83900" {
		t.Fatalf("anchor %s, %v", got, err)
	}
	down := func(context.Context, string) (decimal.Decimal, error) { return decimal.Zero, errors.New("unreachable") }
	if got, err := NewLastTrade(none, down, time.Second).Anchor(context.Background(), "BTC-USDT"); err != nil || !got.IsZero() {
		t.Fatalf("an unreachable reference leaves no anchor: %s, %v", got, err)
	}
	trade := func(context.Context, string) (decimal.Decimal, error) { return decimal.RequireFromString("84000"), nil }
	if got, _ := NewLastTrade(trade, ref, time.Second).Anchor(context.Background(), "BTC-USDT"); got.String() != "84000" {
		t.Fatalf("the last trade wins: %s", got)
	}
}
