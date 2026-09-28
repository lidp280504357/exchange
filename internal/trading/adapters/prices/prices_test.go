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
	now := time.Unix(1_000_000, 0)
	l := NewLastTrade(func(context.Context, string) (decimal.Decimal, time.Time, error) {
		calls++
		return price, now, nil
	}, nil, time.Second)
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

func TestTheAnchorPrefersARecentTradeThenTheReference(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	trade := func(price string, age time.Duration) LastTradeFunc {
		return func(context.Context, string) (decimal.Decimal, time.Time, error) {
			if price == "" {
				return decimal.Zero, time.Time{}, nil
			}
			return decimal.RequireFromString(price), now.Add(-age), nil
		}
	}
	ref := func(context.Context, string) (decimal.Decimal, error) { return decimal.RequireFromString("83900"), nil }
	none := func(context.Context, string) (decimal.Decimal, error) { return decimal.Zero, nil }
	down := func(context.Context, string) (decimal.Decimal, error) { return decimal.Zero, errors.New("unreachable") }
	for _, c := range []struct {
		name      string
		last      LastTradeFunc
		reference ReferenceFunc
		want      string
	}{
		{"a recent trade", trade("84000", time.Minute), ref, "84000"},
		{"an old trade gives way to the reference", trade("70500", time.Hour), ref, "83900"},
		{"no trade: the reference", trade("", 0), ref, "83900"},
		{"an old trade without a reference", trade("70500", time.Hour), none, "70500"},
		{"an unreachable reference", trade("70500", time.Hour), down, "70500"},
		{"nothing at all", trade("", 0), down, "0"},
	} {
		l := NewLastTrade(c.last, c.reference, time.Second)
		l.now = func() time.Time { return now }
		if got, err := l.Anchor(context.Background(), "BTC-USDT"); err != nil || got.String() != c.want {
			t.Errorf("%s: anchor %s, %v; want %s", c.name, got, err, c.want)
		}
	}
}
