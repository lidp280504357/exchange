package prices

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/trading/domain"
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

func TestTheAnchorPrefersTheReferenceThenTheLastTrade(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	trade := func(price string, age time.Duration) LastTradeFunc {
		return func(context.Context, string) (decimal.Decimal, time.Time, error) {
			if price == "" {
				return decimal.Zero, time.Time{}, nil
			}
			return decimal.RequireFromString(price), now.Add(-age), nil
		}
	}
	ref := func(price string) ReferenceFunc {
		return func(context.Context, string) (decimal.Decimal, error) { return decimal.RequireFromString(price), nil }
	}
	none := ref("0")
	down := func(context.Context, string) (decimal.Decimal, error) { return decimal.Zero, errors.New("unreachable") }
	for _, c := range []struct {
		name      string
		last      LastTradeFunc
		reference ReferenceFunc
		want      string
	}{
		{"a fresh reference over a recent trade", trade("84000", time.Minute), ref("83900"), "83900"},
		{"a price event's reference, 16% over the last trade", trade("84000", time.Minute), ref("97440"), "97440"},
		{"no trade: the reference", trade("", 0), ref("83900"), "83900"},
		{"no fresh reference: the last trade, however old", trade("70500", time.Hour), none, "70500"},
		{"an unreachable reference: the last trade", trade("70500", time.Hour), down, "70500"},
		{"no reference market at all (ASTRA-USDT)", trade("0.31", time.Minute), nil, "0.31"},
		{"nothing at all", trade("", 0), down, "0"},
	} {
		l := NewLastTrade(c.last, c.reference, time.Second)
		l.now = func() time.Time { return now }
		if got, err := l.Anchor(context.Background(), "BTC-USDT"); err != nil || got.String() != c.want {
			t.Errorf("%s: anchor %s, %v; want %s", c.name, got, err, c.want)
		}
	}
}

// TestAPriceEventMovesTheBand: a price event pushes BTC-USDT's reference
// 16% over the platform's last trade (review B144). Anchored on the
// reference, a limit buy 15% over the last trade is within the 10% band
// and a market buy may pay up to the band over the new price, where
// HOUSE quotes; anchored on the last trade both were stuck.
func TestAPriceEventMovesTheBand(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := NewLastTrade(func(context.Context, string) (decimal.Decimal, time.Time, error) {
		return decimal.RequireFromString("84000"), now.Add(-time.Minute), nil
	}, func(context.Context, string) (decimal.Decimal, error) {
		return decimal.RequireFromString("97440"), nil
	}, time.Second)
	l.now = func() time.Time { return now }
	anchor, err := l.Anchor(context.Background(), "BTC-USDT")
	if err != nil {
		t.Fatal(err)
	}
	d := decimal.RequireFromString
	pair := domain.Pair{
		Symbol: "BTC-USDT", Base: "BTC", Quote: "USDT",
		TickSize: d("0.01"), LotSize: d("0.00001"), MinQuantity: d("0.00001"), MaxQuantity: d("100"),
		MinNotional: d("5"), PriceBand: d("0.1"), MakerFeeRate: d("0.001"), TakerFeeRate: d("0.001"),
		Status: domain.PairTrading, BaseDecimals: 8, QuoteDecimals: 6, Tradable: true,
	}
	limit := domain.Request{UserID: "u", Symbol: "BTC-USDT", Side: domain.SideBuy, Type: domain.TypeLimit, Price: d("96600"), Quantity: d("0.001")}
	if _, err := domain.NewOrder("01a0e7de-9e72-74e7-87e8-61cdf564e6a8", limit, pair, anchor, now); err != nil {
		t.Fatalf("a limit buy at the event's price: %v", err)
	}
	market := domain.Request{UserID: "u", Symbol: "BTC-USDT", Side: domain.SideBuy, Type: domain.TypeMarket, QuoteAmount: d("100")}
	o, err := domain.NewOrder("01a0e7de-9e72-74e7-87e8-61cdf564e6a9", market, pair, anchor, now)
	if err != nil || !o.ProtectionPrice.Equal(d("107184")) {
		t.Fatalf("a market buy's protection price: %s (%v), want 107184", o.ProtectionPrice, err)
	}
}
