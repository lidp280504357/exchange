package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// Window is the ticker's rolling window (§11.8).
const Window = 24 * time.Hour

// Ticker is the rolling 24-hour summary of a symbol, at minute
// resolution: the window covers the current minute and the 1439 before.
type Ticker struct {
	Symbol string
	// Last is the latest trade price, kept when the window has no trades;
	// zero when the symbol never traded.
	Last decimal.Decimal
	// Open is the last trade price before the window, or the first price
	// in it when nothing traded before; zero without trades.
	Open        decimal.Decimal
	High        decimal.Decimal
	Low         decimal.Decimal
	Volume      decimal.Decimal
	QuoteVolume decimal.Decimal
	Trades      int64
	// Change is (Last - Open) / Open, zero without an open price.
	Change decimal.Decimal
	// Bid and Ask are the best prices, zero when that side is empty.
	Bid decimal.Decimal
	Ask decimal.Decimal
	// At is when a reference source computed the ticker; zero for the
	// platform's own, computed on demand.
	At time.Time
}

// WindowStart returns the open time of the window's first minute at now.
func WindowStart(now time.Time) time.Time {
	return Minute1.Start(now).Add(-Window + time.Minute)
}

// ComputeTicker summarizes minutes, the symbol's 1m candles opening at or
// after WindowStart(now), oldest first; before is the latest 1m candle
// opening before the window (nil: none).
func ComputeTicker(symbol string, minutes []Candle, before *Candle, last, bid, ask decimal.Decimal) Ticker {
	t := Ticker{Symbol: symbol, Last: last, Bid: bid, Ask: ask}
	switch {
	case before != nil:
		t.Open = before.Close
	case len(minutes) > 0:
		t.Open = minutes[0].Open
	}
	for _, c := range minutes {
		if c.Trades == 0 {
			continue
		}
		if t.Trades == 0 {
			t.High, t.Low = c.High, c.Low
		} else {
			t.High, t.Low = decimal.Max(t.High, c.High), decimal.Min(t.Low, c.Low)
		}
		t.Volume, t.QuoteVolume = t.Volume.Add(c.Volume), t.QuoteVolume.Add(c.QuoteVolume)
		t.Trades += c.Trades
	}
	if t.Trades == 0 {
		t.High, t.Low = last, last
	}
	if t.Open.IsPositive() {
		t.Change = last.Sub(t.Open).DivRound(t.Open, 8)
	}
	return t
}

// Trade is a public trade (the trades channel and list).
type Trade struct {
	Symbol    string
	Sequence  int64
	ID        string
	Number    uint64
	Price     decimal.Decimal
	Quantity  decimal.Decimal
	Quote     decimal.Decimal
	TakerSide string // BUY or SELL
	At        time.Time
}
