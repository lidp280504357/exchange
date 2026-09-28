package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var btc = Pair{TickSize: d("0.01"), LotSize: d("0.0001")}

func keys(qs []Quote) []string {
	out := make([]string, len(qs))
	for i, q := range qs {
		out[i] = q.Key() + " " + q.Quantity.String()
	}
	return out
}

func TestPlanQuotesBothSidesAroundTheReference(t *testing.T) {
	p := Defaults("BTC-USDT")
	p.Levels = 2
	got := keys(Plan(p, btc, d("83928.57"), d("1"), d("80000")))
	// 0.1% and 0.2% away, rounded away from the reference to the cent.
	want := []string{"BUY@83844.64 0.002", "BUY@83760.71 0.002", "SELL@84012.5 0.002", "SELL@84096.43 0.002"}
	if len(got) != len(want) {
		t.Fatalf("plan %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("plan %v, want %v", got, want)
		}
	}
}

func TestPlanStopsTheSideThatWouldLeanFurther(t *testing.T) {
	p := Defaults("BTC-USDT")
	sides := func(qs []Quote) (b, s int) {
		for _, q := range qs {
			if q.Side == Buy {
				b++
			} else {
				s++
			}
		}
		return b, s
	}
	// 90% of the value in BTC: no more bids.
	if b, s := sides(Plan(p, btc, d("80000"), d("1"), d("8888"))); b != 0 || s != 5 {
		t.Fatalf("heavy in base: %d bids, %d asks", b, s)
	}
	// 95% in USDT: no more asks.
	if b, s := sides(Plan(p, btc, d("80000"), d("0.05"), d("76000"))); b != 5 || s != 0 {
		t.Fatalf("heavy in quote: %d bids, %d asks", b, s)
	}
	// At the inventory ceiling: no bids even when balanced.
	if b, _ := sides(Plan(p, btc, d("1"), d("5"), d("5"))); b != 0 {
		t.Fatalf("at max_base: %d bids", b)
	}
	if got := Plan(p, btc, decimal.Zero, d("1"), d("1")); got != nil {
		t.Fatalf("no reference, no quotes: %v", got)
	}
	p.Quantity = d("0.00005") // below one lot
	if got := Plan(p, btc, d("80000"), d("1"), d("80000")); got != nil {
		t.Fatalf("a size below the lot quotes nothing: %v", got)
	}
}

func TestMovedAndValidate(t *testing.T) {
	p := Defaults("BTC-USDT")
	if Moved(p, d("80000"), d("80030")) || !Moved(p, d("80000"), d("80040")) || !Moved(p, decimal.Zero, d("1")) {
		t.Fatal("requote at 0.05%")
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []func(*Params){
		func(p *Params) { p.Symbol = "" },
		func(p *Params) { p.Spread = decimal.Zero },
		func(p *Params) { p.Levels = 0 },
		func(p *Params) { p.MaxSkew = d("0.5") },
		func(p *Params) { p.Quantity = decimal.Zero },
	} {
		q := Defaults("BTC-USDT")
		bad(&q)
		if q.Validate() == nil {
			t.Fatalf("accepted %+v", q)
		}
	}
}
