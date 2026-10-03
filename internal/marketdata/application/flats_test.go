package application

import (
	"context"
	"testing"
	"time"

	"github.com/lidp280504357/exchange/internal/marketdata/domain"
	"github.com/lidp280504357/exchange/internal/marketdata/ports"
)

// A minute without a trade of a symbol charting the platform's trades is
// stored flat once it ended FlatGrace ago (coordinator 2026-10-04): at the
// previous close, no volume, no trade; its event is queued with it; each
// longer interval takes it in as a rollup of the minutes would (one that
// opens on a flat minute opens at that price, and keeps it when a trade
// comes); the publisher closes it like any other; nothing for a symbol
// whose chart is the reference market's, nor further back than
// FlatCatchUp.
func TestQuietMinutesAreStoredFlat(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	now := at("2026-09-30T10:01:30Z")
	s := newService(t, store, &now)
	if _, err := s.OnTrades(ctx, []domain.Trade{
		trade(1, "70000", "0.1", "2026-09-30T10:01:10Z"), trade(2, "70500", "0.1", "2026-09-30T10:01:20Z"),
	}); err != nil {
		t.Fatal(err)
	}
	var emitted []domain.Candle
	emit := func(_ context.Context, _ ports.Repos, c domain.Candle) error {
		emitted = append(emitted, c)
		return nil
	}
	ours := func(string) bool { return true }
	// 10:02 ended 5 seconds ago: it waits for a late trade.
	now = at("2026-09-30T10:03:05Z")
	if flats, err := s.FlatMinutes(ctx, now, ours, emit); err != nil || len(flats) != 0 {
		t.Fatalf("within the grace: %+v %v", flats, err)
	}
	now = at("2026-09-30T10:04:05Z") // 10:02 ended a minute ago, 10:03 five seconds ago
	flats, err := s.FlatMinutes(ctx, now, ours, emit)
	if err != nil || len(flats) != 1 || len(emitted) != 1 {
		t.Fatalf("flats %+v %v, emitted %d", flats, err, len(emitted))
	}
	flat := store.candles["BTC-USDT|1m|2026-09-30T10:02:00Z"]
	if flat.Trades != 0 || !flat.Volume.IsZero() || !flat.Open.Equal(d("70500")) || !flat.High.Equal(d("70500")) ||
		!flat.Low.Equal(d("70500")) || !flat.Close.Equal(d("70500")) {
		t.Fatalf("the flat minute %+v", flat)
	}
	if five := store.candles["BTC-USDT|5m|2026-09-30T10:00:00Z"]; five.Trades != 2 || !five.Open.Equal(d("70000")) ||
		!five.High.Equal(d("70500")) || !five.Close.Equal(d("70500")) {
		t.Fatalf("5m %+v", five)
	}
	if again, err := s.FlatMinutes(ctx, now, ours, emit); err != nil || len(again) != 0 {
		t.Fatalf("twice: %+v %v", again, err)
	}
	// The publisher closes it: the chart and the stored candles agree.
	if got := kinds(s.Updates(now)); got["closed:1m"] != 1 {
		t.Fatalf("pushed %v", got)
	}

	// 10:03 and 10:04 quiet; 10:05 trades: the 5m candle from 10:05 opens
	// at its trade. 10:06–10:09 quiet; 10:10 quiet too, then a trade at
	// 10:11: the 5m candle from 10:10 opened flat at 70500 and keeps that
	// open, its high and low counting both.
	now = at("2026-09-30T10:05:30Z")
	if _, err := s.FlatMinutes(ctx, now, ours, emit); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OnTrades(ctx, []domain.Trade{trade(3, "71000", "0.1", "2026-09-30T10:05:10Z")}); err != nil {
		t.Fatal(err)
	}
	if five := store.candles["BTC-USDT|5m|2026-09-30T10:05:00Z"]; !five.Open.Equal(d("71000")) || five.Trades != 1 {
		t.Fatalf("5m from 10:05 %+v", five)
	}
	now = at("2026-09-30T10:11:30Z")
	if _, err := s.FlatMinutes(ctx, now, ours, emit); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OnTrades(ctx, []domain.Trade{trade(4, "69000", "0.1", "2026-09-30T10:11:10Z")}); err != nil {
		t.Fatal(err)
	}
	if five := store.candles["BTC-USDT|5m|2026-09-30T10:10:00Z"]; !five.Open.Equal(d("71000")) || !five.High.Equal(d("71000")) ||
		!five.Low.Equal(d("69000")) || !five.Close.Equal(d("69000")) || five.Trades != 1 {
		t.Fatalf("5m from 10:10, opened flat %+v", five)
	}
	for _, m := range []string{"10:06", "10:07", "10:08", "10:09", "10:10"} {
		if c, ok := store.candles["BTC-USDT|1m|2026-09-30T"+m+":00Z"]; !ok || c.Trades != 0 || !c.Close.Equal(d("71000")) {
			t.Fatalf("1m %s %+v", m, c)
		}
	}

	// A symbol charting the reference market gets none.
	now = at("2026-09-30T10:20:00Z")
	if flats, err := s.FlatMinutes(ctx, now, func(string) bool { return false }, emit); err != nil || len(flats) != 0 {
		t.Fatalf("a symbol on the reference market %+v %v", flats, err)
	}
	// Forward only: after three hours away, the minutes of the last hour
	// that ended FlatGrace ago (12:20 to 13:18).
	now = at("2026-09-30T13:20:00Z")
	flats, err = s.FlatMinutes(ctx, now, ours, emit)
	if err != nil || len(flats) != int(FlatCatchUp/time.Minute)-1 || !flats[0].OpenTime.Equal(at("2026-09-30T12:20:00Z")) {
		t.Fatalf("after three hours: %d flats from %v, %v", len(flats), flats[0].OpenTime, err)
	}
}
