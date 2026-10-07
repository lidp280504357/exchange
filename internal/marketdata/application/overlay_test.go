package application

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
)

// overlayFlags turns market.overlay on and off.
type overlayFlags struct{ on atomic.Bool }

func (f *overlayFlags) Enabled(key string, _ flags.Subject) bool {
	return key == flags.KeyOverlay && f.on.Load()
}

func newOverlayRig() (*Overlay, *overlayFlags, *time.Time) {
	fl := &overlayFlags{}
	fl.on.Store(true)
	o := NewOverlay(fl, func(s string) bool { return s == "BTC-USDT" || s == "ETH-USDT" }, prometheus.NewRegistry())
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	o.now = func() time.Time { return now }
	return o, fl, &now
}

// A price event's factor holds while its pushes come, one event a pair,
// back to 1 past its until, 5 seconds without a push, a clear or the flag
// going off (J0 contract §2.1); the perpetuals' mark stays computed 30
// seconds past a risk overlay.
func TestOverlayFactors(t *testing.T) {
	o, fl, now := newOverlayRig()
	push := func(symbol, f, event string, seq int64, risk bool) error {
		return o.Set(symbol, OverlayPush{Factor: d(f), Until: now.Add(4 * time.Second), Risk: risk, EventID: event, Seq: seq})
	}
	if err := push("SOL-USDT", "1.1", "e1", 1, true); !errors.Is(err, ErrNotFollowed) {
		t.Fatalf("not followed: %v", err)
	}
	for _, bad := range []string{"0.09", "1.91"} {
		if err := push("BTC-USDT", bad, "e1", 1, true); apperr.From(err).Code != apperr.CodeInvalidArgument {
			t.Fatalf("factor %s: %v", bad, err)
		}
	}
	if err := o.Set("BTC-USDT", OverlayPush{Factor: d("1.1"), Until: now.Add(16 * time.Second), EventID: "e1", Seq: 1}); err == nil {
		t.Fatal("until too far")
	}
	if err := push("BTC-USDT", "1.05", "e1", 1, true); err != nil {
		t.Fatal(err)
	}
	if f, risk := o.Factor("BTC-USDT"); !f.Equal(d("1.05")) || !risk || !o.RiskFactor("BTC-USDT").Equal(d("1.05")) || !o.MarkComputed("BTC-USDT") {
		t.Fatalf("factor %s risk %v", f, risk)
	}
	if err := push("BTC-USDT", "1.1", "e2", 1, true); !errors.Is(err, ErrOverlayBusy) {
		t.Fatalf("another event: %v", err)
	}
	if err := push("BTC-USDT", "1.02", "e1", 1, true); err != nil { // an older push: dropped, no error
		t.Fatal(err)
	}
	if f, _ := o.Factor("BTC-USDT"); !f.Equal(d("1.05")) {
		t.Fatalf("an older push changed it: %s", f)
	}
	*now = now.Add(3 * time.Second)
	if err := push("BTC-USDT", "1.08", "e1", 2, true); err != nil {
		t.Fatal(err)
	}
	if list := o.List(); len(list) != 1 || !list[0].Factor.Equal(d("1.08")) || list[0].Seq != 2 {
		t.Fatalf("list %+v", list)
	}
	*now = now.Add(5*time.Second + time.Millisecond) // no push for 5 s
	if f, _ := o.Factor("BTC-USDT"); !f.Equal(one) {
		t.Fatalf("stale push still at %s", f)
	}
	if !o.MarkComputed("BTC-USDT") {
		t.Fatal("the mark went back at once")
	}
	*now = now.Add(MarkBackAfter)
	if o.MarkComputed("BTC-USDT") {
		t.Fatal("the mark still computed 30 s later")
	}

	// Without risk the perpetuals see nothing.
	if err := push("ETH-USDT", "0.9", "e3", 1, false); err != nil {
		t.Fatal(err)
	}
	if f, risk := o.Factor("ETH-USDT"); !f.Equal(d("0.9")) || risk || !o.RiskFactor("ETH-USDT").Equal(one) || o.MarkComputed("ETH-USDT") {
		t.Fatalf("no risk: %s %v", f, risk)
	}
	o.Clear("ETH-USDT")
	if f, _ := o.Factor("ETH-USDT"); !f.Equal(one) {
		t.Fatalf("cleared: %s", f)
	}
	// Off: refused, and what there was is 1.
	if err := push("ETH-USDT", "0.9", "e4", 1, true); err != nil {
		t.Fatal(err)
	}
	fl.on.Store(false)
	if f, _ := o.Factor("ETH-USDT"); !f.Equal(one) {
		t.Fatalf("flag off: %s", f)
	}
	if err := push("ETH-USDT", "0.9", "e4", 2, true); !errors.Is(err, ErrOverlayOff) {
		t.Fatalf("flag off: %v", err)
	}
}

// A ticker under a factor shows the scaled last price (and a pair's best
// bid and ask), its change from the open again; the event's highest and
// lowest stay in the 24-hour high and low after it ends.
func TestATickerCarriesTheEventAndItsPeak(t *testing.T) {
	o, _, now := newOverlayRig()
	raw := domain.Ticker{Last: d("86000"), Open: d("84000"), High: d("87000"), Low: d("83000"), Bid: d("85999.99"), Ask: d("86000.01")}
	if err := o.Set("BTC-USDT", OverlayPush{Factor: d("1.16"), Until: now.Add(4 * time.Second), Risk: true, EventID: "e1", Seq: 1}); err != nil {
		t.Fatal(err)
	}
	f, _ := o.Factor("BTC-USDT")
	tk := o.Ticker("BTC-USDT", raw, f, true)
	if !tk.Last.Equal(d("99760")) || !tk.High.Equal(d("99760")) || !tk.Low.Equal(d("83000")) || !tk.Bid.Equal(d("99759.99")) ||
		!tk.Change.Equal(d("0.18761905")) {
		t.Fatalf("during %+v", tk)
	}
	o.Clear("BTC-USDT")
	*now = now.Add(time.Hour)
	f, _ = o.Factor("BTC-USDT")
	if after := o.Ticker("BTC-USDT", raw, f, true); !after.Last.Equal(d("86000")) || !after.High.Equal(d("99760")) {
		t.Fatalf("after %+v", after)
	}
	*now = now.Add(24 * time.Hour)
	if later := o.Ticker("BTC-USDT", raw, f, true); !later.High.Equal(d("87000")) {
		t.Fatalf("a day later %+v", later)
	}
}

// A minute a factor reached follows the scaled closes, so the spike stays
// in the candle; a minute none reached is the reference market's.
func TestACandleKeepsTheSpike(t *testing.T) {
	o, _, now := newOverlayRig()
	oc := newOverlayCandles(o)
	minute := now.Truncate(time.Minute)
	c := domain.Candle{Symbol: "BTC-USDT", OpenTime: minute, Open: d("86000"), High: d("86010"), Low: d("85990"), Close: d("86000")}
	if got := oc.apply(c); !got.Close.Equal(d("86000")) || !got.High.Equal(d("86010")) {
		t.Fatalf("untouched %+v", got)
	}
	if err := o.Set("BTC-USDT", OverlayPush{Factor: d("1.1"), Until: now.Add(4 * time.Second), EventID: "e1", Seq: 1}); err != nil {
		t.Fatal(err)
	}
	if got := oc.apply(c); !got.Close.Equal(d("94600")) || !got.High.Equal(d("94611")) || !got.Low.Equal(d("85990")) {
		t.Fatalf("touched %+v", got)
	}
	o.Clear("BTC-USDT")
	c.Close = d("86005")
	if got := oc.apply(c); !got.Close.Equal(d("86005")) || !got.High.Equal(d("94611")) {
		t.Fatalf("back at 1 within the minute %+v", got)
	}
	c.OpenTime, c.Open, c.High, c.Low, c.Close = minute.Add(time.Minute), d("86005"), d("86006"), d("86004"), d("86005")
	if got := oc.apply(c); !got.High.Equal(d("86006")) {
		t.Fatalf("the next minute %+v", got)
	}
}
