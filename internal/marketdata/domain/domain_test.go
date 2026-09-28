package domain

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestIntervalsAlignToUTC(t *testing.T) {
	now := at("2026-09-30T13:47:29Z") // a Wednesday
	for _, c := range []struct {
		i           Interval
		start, next string
	}{
		{Minute1, "2026-09-30T13:47:00Z", "2026-09-30T13:48:00Z"},
		{Minute3, "2026-09-30T13:45:00Z", "2026-09-30T13:48:00Z"},
		{Minute15, "2026-09-30T13:45:00Z", "2026-09-30T14:00:00Z"},
		{Hour4, "2026-09-30T12:00:00Z", "2026-09-30T16:00:00Z"},
		{Hour12, "2026-09-30T12:00:00Z", "2026-10-01T00:00:00Z"},
		{Day1, "2026-09-30T00:00:00Z", "2026-10-01T00:00:00Z"},
		{Week1, "2026-09-28T00:00:00Z", "2026-10-05T00:00:00Z"},
		{Month1, "2026-09-01T00:00:00Z", "2026-10-01T00:00:00Z"},
	} {
		start := c.i.Start(now)
		if !start.Equal(at(c.start)) || !c.i.Next(start).Equal(at(c.next)) {
			t.Errorf("%s: start %s next %s", c.i, start, c.i.Next(start))
		}
	}
	// A time in another zone is aligned by its UTC value.
	if got := Day1.Start(at("2026-09-30T01:00:00+08:00")); !got.Equal(at("2026-09-29T00:00:00Z")) {
		t.Errorf("day of 01:00+08:00: %s", got)
	}
	if got := Week1.Start(at("2026-09-28T00:00:00Z")); !got.Equal(at("2026-09-28T00:00:00Z")) {
		t.Errorf("a Monday starts its own week: %s", got)
	}
	for _, s := range []string{"1m", "12h", "1w", "1M"} {
		if _, ok := ParseInterval(s); !ok {
			t.Errorf("%s not parsed", s)
		}
	}
	if _, ok := ParseInterval("2d"); ok {
		t.Error("2d is not an interval")
	}
}

func TestCandleAdd(t *testing.T) {
	c := Candle{}
	c.Add(d("100"), d("1"), d("100"))
	c.Add(d("105"), d("2"), d("210"))
	c.Add(d("98"), d("0.5"), d("49"))
	if !c.Open.Equal(d("100")) || !c.High.Equal(d("105")) || !c.Low.Equal(d("98")) || !c.Close.Equal(d("98")) ||
		!c.Volume.Equal(d("3.5")) || !c.QuoteVolume.Equal(d("359")) || c.Trades != 3 {
		t.Fatalf("candle %+v", c)
	}
	c.Interval, c.OpenTime = Minute1, at("2026-09-30T13:47:00Z")
	if c.Closed(at("2026-09-30T13:47:59Z")) || !c.Closed(at("2026-09-30T13:48:00Z")) {
		t.Fatal("closed at the next interval's start")
	}
}

func TestFillLeavesNoGaps(t *testing.T) {
	stored := []Candle{
		{Interval: Minute1, OpenTime: at("2026-09-30T10:01:00Z"), Open: d("10"), High: d("12"), Low: d("9"), Close: d("11"), Trades: 2},
		{Interval: Minute1, OpenTime: at("2026-09-30T10:04:00Z"), Open: d("11"), High: d("11"), Low: d("11"), Close: d("11"), Trades: 1},
	}
	got := Fill("X", Minute1, at("2026-09-30T10:00:00Z"), at("2026-09-30T10:06:00Z"), nil, stored, 100)
	// 10:00 has nothing before it; 10:02, 10:03 and 10:05 are flat.
	want := []string{"10:01 11 2", "10:02 11 0", "10:03 11 0", "10:04 11 1", "10:05 11 0"}
	if len(got) != len(want) {
		t.Fatalf("%d candles, want %d: %+v", len(got), len(want), got)
	}
	for i, c := range got {
		if s := c.OpenTime.Format("15:04") + " " + c.Close.String() + " " + decimal.NewFromInt(c.Trades).String(); s != want[i] {
			t.Errorf("candle %d = %s, want %s", i, s, want[i])
		}
	}
	if !got[1].Open.Equal(d("11")) || !got[1].Volume.IsZero() {
		t.Errorf("flat candle %+v", got[1])
	}
	// A candle before the range carries into it; limit keeps the latest.
	before := Candle{Close: d("7")}
	got = Fill("X", Minute1, at("2026-09-30T10:00:00Z"), at("2026-09-30T10:03:00Z"), &before, stored, 2)
	if len(got) != 2 || !got[0].OpenTime.Equal(at("2026-09-30T10:01:00Z")) || !got[1].Close.Equal(d("11")) {
		t.Fatalf("with before and limit: %+v", got)
	}
}

func TestTicker(t *testing.T) {
	minutes := []Candle{
		{Open: d("100"), High: d("110"), Low: d("95"), Close: d("105"), Volume: d("2"), QuoteVolume: d("205"), Trades: 3},
		{Open: d("105"), High: d("120"), Low: d("104"), Close: d("118"), Volume: d("1"), QuoteVolume: d("118"), Trades: 1},
	}
	before := Candle{Close: d("90")}
	tk := ComputeTicker("X", minutes, &before, d("118"), d("117"), d("119"))
	if !tk.Open.Equal(d("90")) || !tk.High.Equal(d("120")) || !tk.Low.Equal(d("95")) || !tk.Volume.Equal(d("3")) ||
		!tk.QuoteVolume.Equal(d("323")) || tk.Trades != 4 || !tk.Change.Equal(d("0.31111111")) || !tk.Bid.Equal(d("117")) {
		t.Fatalf("ticker %+v", tk)
	}
	// Without a trade before the window, the window's first price opens it.
	if tk := ComputeTicker("X", minutes, nil, d("118"), decimal.Zero, decimal.Zero); !tk.Open.Equal(d("100")) || !tk.Change.Equal(d("0.18")) {
		t.Fatalf("no trade before: %+v", tk)
	}
	// A quiet window keeps the last price, with zero volume.
	if tk := ComputeTicker("X", nil, &before, d("90"), decimal.Zero, decimal.Zero); !tk.High.Equal(d("90")) || !tk.Volume.IsZero() || !tk.Change.IsZero() {
		t.Fatalf("quiet window: %+v", tk)
	}
	if tk := ComputeTicker("X", nil, nil, decimal.Zero, decimal.Zero, decimal.Zero); !tk.Open.IsZero() || !tk.Last.IsZero() {
		t.Fatalf("never traded: %+v", tk)
	}
	if got := WindowStart(at("2026-09-30T13:47:29Z")); !got.Equal(at("2026-09-29T13:48:00Z")) {
		t.Fatalf("window start %s", got)
	}
}
