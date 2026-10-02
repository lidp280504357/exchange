package application

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	"github.com/lidp280504357/exchange/internal/marketdata/ports"
)

// A pair no reference market follows has its own market as its reference:
// the middle of the engine's book, else a recent trade, else the price the
// simulated market reported in the last 30 seconds; a followed pair and a
// contract never take one.
func TestPlatformReference(t *testing.T) {
	ctx := context.Background()
	now := at("2026-10-02T10:00:00Z")
	svc := newService(t, newMemStore(), &now)
	refs := NewReferenceMap(newListing([]ports.Pair{
		{Symbol: "BTC-USDT", Base: "BTC", Quote: "USDT", Status: "TRADING", Reference: ref("BTC-USDT", "BTCUSDT")},
		{Symbol: "ASTRA-USDT", Base: "ASTRA", Quote: "USDT", Status: "TRADING"},
	}, []ports.Contract{{Symbol: "ASTRA-USDT-PERP", IndexSymbol: "ASTRA-USDT"}}), slog.New(slog.DiscardHandler))
	pr := &PlatformReference{Svc: svc, Refs: refs, Now: func() time.Time { return now }}

	if _, ok := pr.Price(ctx, "ASTRA-USDT"); ok {
		t.Fatal("nothing prices the pair yet")
	}
	for _, symbol := range []string{"BTC-USDT", "ASTRA-USDT-PERP", "NOPE-USDT"} {
		if err := pr.Report(ctx, symbol, d("1.1")); !errors.Is(err, ErrFollowed) {
			t.Fatalf("a simulated price of %s: %v", symbol, err)
		}
	}
	if err := pr.Report(ctx, "ASTRA-USDT", d("0")); err == nil {
		t.Fatal("a price of 0 was taken")
	}
	if err := pr.Report(ctx, "ASTRA-USDT", d("1.15")); err != nil {
		t.Fatal(err)
	}
	if r, ok := pr.Price(ctx, "ASTRA-USDT"); !ok || r.Source != SourceSimulation || !r.Price.Equal(d("1.15")) {
		t.Fatalf("the simulated price: %+v %v", r, ok)
	}
	svc.OnDepth(&marketv1.DepthSnapshot{
		Symbol: "ASTRA-USDT", Sequence: 1, Bids: []*marketv1.PriceLevel{{Price: "1.01", Quantity: "100"}},
		Asks: []*marketv1.PriceLevel{{Price: "1.03", Quantity: "100"}},
	})
	if r, ok := pr.Price(ctx, "ASTRA-USDT"); !ok || r.Source != SourcePlatform || !r.Price.Equal(d("1.02")) {
		t.Fatalf("the middle of the book comes first: %+v %v", r, ok)
	}
	svc.OnDepth(&marketv1.DepthSnapshot{Symbol: "ASTRA-USDT", Sequence: 2, Asks: []*marketv1.PriceLevel{{Price: "1.03", Quantity: "100"}}})
	if r, ok := pr.Price(ctx, "ASTRA-USDT"); !ok || r.Source != SourceSimulation {
		t.Fatalf("a one-sided book: the simulated price again: %+v %v", r, ok)
	}
	now = now.Add(SimulatedPriceAge + time.Second)
	if r, ok := pr.Price(ctx, "ASTRA-USDT"); ok {
		t.Fatalf("an old simulated price: %+v", r)
	}
	if _, ok := pr.Price(ctx, "BTC-USDT"); ok {
		t.Fatal("a followed pair took the platform's price")
	}
}
