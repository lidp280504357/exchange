// Package domain holds the platform market data model (requirements
// §5.11, §11.8): candles of every interval built from the platform's own
// trades, the rolling 24-hour ticker, and the public trade list. Amounts
// are decimals (ADR-0008).
package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// Interval is a candle period, aligned to UTC (§11.8).
type Interval string

// The intervals, shortest first.
const (
	Minute1  Interval = "1m"
	Minute3  Interval = "3m"
	Minute5  Interval = "5m"
	Minute15 Interval = "15m"
	Minute30 Interval = "30m"
	Hour1    Interval = "1h"
	Hour2    Interval = "2h"
	Hour4    Interval = "4h"
	Hour6    Interval = "6h"
	Hour12   Interval = "12h"
	Day1     Interval = "1d"
	Week1    Interval = "1w"
	Month1   Interval = "1M"
)

// Intervals lists every interval, shortest first.
var Intervals = []Interval{Minute1, Minute3, Minute5, Minute15, Minute30, Hour1, Hour2, Hour4, Hour6, Hour12, Day1, Week1, Month1}

var fixed = map[Interval]time.Duration{
	Minute1: time.Minute, Minute3: 3 * time.Minute, Minute5: 5 * time.Minute, Minute15: 15 * time.Minute,
	Minute30: 30 * time.Minute, Hour1: time.Hour, Hour2: 2 * time.Hour, Hour4: 4 * time.Hour, Hour6: 6 * time.Hour,
	Hour12: 12 * time.Hour, Day1: 24 * time.Hour,
}

// ParseInterval reports whether s names an interval.
func ParseInterval(s string) (Interval, bool) {
	i := Interval(s)
	_, ok := fixed[i]
	return i, ok || i == Week1 || i == Month1
}

// Start returns the open time of the interval that contains t. Days start
// at 00:00 UTC, weeks on Monday, months on the first.
func (i Interval) Start(t time.Time) time.Time {
	t = t.UTC()
	switch i {
	case Month1:
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	case Week1:
		day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		return day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
	}
	// Every fixed interval divides a day, and Truncate counts from
	// midnight of January 1, year 1, so the result is aligned to UTC days.
	return t.Truncate(fixed[i])
}

// Next returns the open time of the interval after the one opening at
// start.
func (i Interval) Next(start time.Time) time.Time {
	switch i {
	case Month1:
		return start.AddDate(0, 1, 0)
	case Week1:
		return start.AddDate(0, 0, 7)
	}
	return start.Add(fixed[i])
}

// Candle is one interval of a symbol's trades. A candle without trades
// repeats the previous close with zero volume (Flat).
type Candle struct {
	Symbol      string
	Interval    Interval
	OpenTime    time.Time
	Open        decimal.Decimal
	High        decimal.Decimal
	Low         decimal.Decimal
	Close       decimal.Decimal
	Volume      decimal.Decimal // base
	QuoteVolume decimal.Decimal
	Trades      int64
}

// Add folds a trade into the candle: the first sets the open.
func (c *Candle) Add(price, quantity, quote decimal.Decimal) {
	if c.Trades == 0 {
		c.Open, c.High, c.Low = price, price, price
	} else {
		c.High, c.Low = decimal.Max(c.High, price), decimal.Min(c.Low, price)
	}
	c.Close = price
	c.Volume, c.QuoteVolume = c.Volume.Add(quantity), c.QuoteVolume.Add(quote)
	c.Trades++
}

// Closed reports whether the candle's interval has ended at now.
func (c Candle) Closed(now time.Time) bool { return !c.Interval.Next(c.OpenTime).After(now) }

// Flat is a candle without trades at price, the previous close.
func Flat(symbol string, i Interval, openTime time.Time, price decimal.Decimal) Candle {
	return Candle{Symbol: symbol, Interval: i, OpenTime: openTime, Open: price, High: price, Low: price, Close: price}
}

// Fill returns the candles from the interval containing from to the last
// one opening before to: stored
// ones where they exist, flat ones in the gaps, so a chart is continuous
// (§11.8). before is the latest stored candle opening before from (nil:
// none), stored the stored candles in the range, oldest first. Intervals
// before the first trade are left out. At most limit candles are
// returned, the latest ones.
func Fill(symbol string, i Interval, from, to time.Time, before *Candle, stored []Candle, limit int) []Candle {
	var out []Candle
	prev := before
	next := 0
	for t := i.Start(from); t.Before(to); t = i.Next(t) {
		for next < len(stored) && stored[next].OpenTime.Before(t) {
			next++
		}
		switch {
		case next < len(stored) && stored[next].OpenTime.Equal(t):
			c := stored[next]
			out = append(out, c)
			prev = &out[len(out)-1]
		case prev != nil:
			out = append(out, Flat(symbol, i, t, prev.Close))
		}
		if len(out) > limit {
			out = out[1:]
		}
	}
	return out
}
