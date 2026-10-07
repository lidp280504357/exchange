package domain

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func candleAt(at time.Time, i Interval, ohlc ...string) Candle {
	p := decimal.RequireFromString
	return Candle{Interval: i, OpenTime: at, Open: p(ohlc[0]), High: p(ohlc[1]), Low: p(ohlc[2]), Close: p(ohlc[3]), Volume: p("7")}
}

// The minutes a price event touched stay in the closed candles (review GD
// ③): a 1m candle is the touched minute's prices with the reference
// market's volume; a 5m candle's high widens, its open and close are the
// touched minutes' only when they are its first and last.
func TestTouchedMinutesStayInTheCandles(t *testing.T) {
	at := time.Date(2026, 10, 7, 4, 0, 0, 0, time.UTC)
	touched := []Candle{
		candleAt(at.Add(4*time.Minute), Minute1, "84000", "97000", "84000", "96500"),
		candleAt(at.Add(5*time.Minute), Minute1, "96500", "97400", "84050", "84100"),
	}
	ones := MergeOverlaid([]Candle{
		candleAt(at.Add(3*time.Minute), Minute1, "84010", "84050", "83990", "84000"),
		candleAt(at.Add(4*time.Minute), Minute1, "84000", "84060", "83980", "84030"),
		candleAt(at.Add(5*time.Minute), Minute1, "84030", "84110", "84020", "84100"),
	}, Minute1, touched)
	if c := ones[1]; c.Open.String() != "84000" || c.High.String() != "97000" || c.Low.String() != "83980" || c.Close.String() != "96500" ||
		c.Volume.String() != "7" {
		t.Fatalf("the touched minute %+v", c)
	}
	if c := ones[0]; c.High.String() != "84050" || c.Close.String() != "84000" {
		t.Fatalf("the minute before %+v", c)
	}
	fives := MergeOverlaid([]Candle{
		candleAt(at, Minute5, "83900", "84060", "83890", "84030"),
		candleAt(at.Add(5*time.Minute), Minute5, "84030", "84200", "84000", "84150"),
	}, Minute5, touched)
	if c := fives[0]; c.Open.String() != "83900" || c.High.String() != "97000" || c.Close.String() != "96500" {
		t.Fatalf("the first five minutes %+v", c)
	}
	if c := fives[1]; c.Open.String() != "96500" || c.High.String() != "97400" || c.Close.String() != "84150" {
		t.Fatalf("the next five %+v", c)
	}
}
