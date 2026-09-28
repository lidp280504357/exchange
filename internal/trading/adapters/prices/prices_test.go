package prices

import (
	"context"
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
	}, time.Second)
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
