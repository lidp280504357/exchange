package application

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
)

// Price overlays (design 2026-10-07, general price control; J0 contract
// §2): market-sim pushes each second the factor a price event puts on a
// followed pair, and the pair's reference data on the platform carries it
// (the book HOUSE quotes, trades, ticker, candles, the internal reference
// price; with risk the perpetuals' index and mark). A factor lasts until
// its until and 5 seconds past its push at most, and is 1 while the flag
// market.overlay is off; nothing is stored, so a restart is back at 1.

// The bounds of an overlay.
const (
	// overlayStale is how long a push holds without the next.
	overlayStale = 5 * time.Second
	// overlayMaxAhead is how far past the push its until may reach.
	overlayMaxAhead = 15 * time.Second
	// overlayLongest is how long an event runs at most: its end when its
	// pushes do not tell (MarketOverlayStuck).
	overlayLongest = 10 * time.Minute
)

var (
	overlayMin = decimal.RequireFromString("0.1")
	overlayMax = decimal.RequireFromString("1.9")
	one        = decimal.NewFromInt(1)
)

// Overlay errors (J0 contract §2.1).
var (
	ErrNotFollowed = apperr.New(apperr.KindConflict, "MARKET_NOT_FOLLOWED", "the pair follows no reference market: its price events are the simulated market's own")
	ErrOverlayOff  = apperr.New(apperr.KindConflict, "MARKET_OVERLAY_OFF", "price overlays are off (market.overlay)")
	ErrOverlayBusy = apperr.New(apperr.KindConflict, "MARKET_OVERLAY_BUSY", "another price event overlays the pair")
	// ErrOverlayNotReady answers while the listing of the followed pairs
	// is not read yet (a restart): push again (review C58 ①).
	ErrOverlayNotReady = apperr.New(apperr.KindUnavailable, "MARKET_OVERLAY_NOT_READY",
		"the followed pairs are not known yet: push again")
	errOverlayRange = apperr.Invalid("factor must be from 0.1 to 1.9")
)

// OverlayPush is one push of a price event's factor; EndsAt is when the
// event is to be back at 1 and Since when it began (zero: not told).
type OverlayPush struct {
	Factor  decimal.Decimal
	Until   time.Time
	Risk    bool
	EventID string
	Seq     int64
	EndsAt  time.Time
	Since   time.Time
}

// OverlayState is a pair's overlay as kept.
type OverlayState struct {
	Symbol   string
	Factor   decimal.Decimal
	Until    time.Time
	Risk     bool
	EventID  string
	Seq      int64
	Received time.Time
	// EndsAt is when the event is to be back at 1: as its pushes tell, or
	// overlayLongest past its first. Since is when it began: as its pushes
	// tell (a restart of market-data in the middle of it), or its first
	// push here.
	EndsAt time.Time
	Since  time.Time
}

// Overlay keeps the pairs' factors.
type Overlay struct {
	flags    Flags
	followed func(symbol string) bool
	listed   func() bool // whether the followed pairs are known (nil: always)
	ticks    func(symbol string) decimal.Decimal
	now      func() time.Time

	mu    sync.Mutex
	items map[string]OverlayState
	// closed is each pair's last event ended by a push back to 1, and
	// that push's seq: its older pushes are stale.
	closed map[string]OverlayState
	// peaks are the overlays' extremes by symbol (Ticker).
	peaks map[string][]peak

	factor *prometheus.GaugeVec
	endsAt *prometheus.GaugeVec
	pushes *prometheus.CounterVec
}

// NewOverlay returns the overlays of the pairs followed tells apart;
// register its metrics with reg.
func NewOverlay(fl Flags, followed func(symbol string) bool, reg prometheus.Registerer) *Overlay {
	o := &Overlay{
		flags: fl, followed: followed, now: time.Now, items: map[string]OverlayState{}, closed: map[string]OverlayState{},
		peaks: map[string][]peak{},
		factor: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "market_overlay_factor", Help: "The price event's factor on a followed pair's reference data (only while it is not 1).",
		}, []string{"symbol"}),
		endsAt: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "market_overlay_ends_at_seconds", Help: "When the price event on a pair is to be back at 1 (Unix seconds; only while it is not 1).",
		}, []string{"symbol"}),
		pushes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_overlay_pushes_total", Help: "Overlay pushes from market-sim by result: ok, stale (an older seq) or refused.",
		}, []string{"result"}),
	}
	reg.MustRegister(o.factor, o.endsAt, o.pushes)
	return o
}

func (o *Overlay) enabled() bool { return o.flags.Enabled(flags.KeyOverlay, flags.Subject{}) }

// WithListing has the pushes wait for the listing of the followed pairs
// (listed): until it was read once they are answered ErrOverlayNotReady,
// not ErrNotFollowed, which market-sim takes for good. Call it before
// serving.
func (o *Overlay) WithListing(listed func() bool) { o.listed = listed }

// WithTicks has the scaled prices of a symbol on its price step (ticks:
// the pairs' and contracts' as instrument-service has them). Call it
// before serving.
func (o *Overlay) WithTicks(ticks func(symbol string) decimal.Decimal) { o.ticks = ticks }

// Tick is symbol's price step, zero when not known (the scaled prices keep
// the reference market's precision then).
func (o *Overlay) Tick(symbol string) decimal.Decimal {
	if o == nil || o.ticks == nil {
		return decimal.Zero
	}
	return o.ticks(symbol)
}

// Set takes a push for symbol (J0 contract §2.1).
func (o *Overlay) Set(symbol string, p OverlayPush) error {
	symbol = strings.ToUpper(symbol)
	err := o.set(symbol, p)
	if errors.Is(err, errStalePush) {
		o.pushes.WithLabelValues("stale").Inc()
		return nil
	}
	if err != nil {
		o.pushes.WithLabelValues("refused").Inc()
		return err
	}
	o.pushes.WithLabelValues("ok").Inc()
	return nil
}

var errStalePush = apperr.Invalid("an older push")

func (o *Overlay) set(symbol string, p OverlayPush) error {
	if !o.enabled() {
		return ErrOverlayOff
	}
	if o.listed != nil && !o.listed() {
		return ErrOverlayNotReady
	}
	if !o.followed(symbol) {
		return ErrNotFollowed
	}
	if p.Factor.LessThan(overlayMin) || p.Factor.GreaterThan(overlayMax) {
		return errOverlayRange
	}
	if p.EventID == "" {
		return apperr.Invalid("event_id is required")
	}
	now := o.now()
	if !p.Until.After(now) || p.Until.After(now.Add(overlayMaxAhead)) {
		return apperr.Invalid("until must be within 15 seconds from now")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if c, ok := o.closed[symbol]; ok && c.EventID == p.EventID && p.Seq <= c.Seq {
		return errStalePush
	}
	cur, ok := o.live(symbol, now)
	if ok && cur.EventID != p.EventID {
		return ErrOverlayBusy
	}
	if ok && p.Seq <= cur.Seq {
		return errStalePush
	}
	if p.Factor.Equal(one) {
		// The event is back at 1: it ends now, not when the push would
		// lapse.
		o.endLocked(symbol)
		o.closed[symbol] = OverlayState{Symbol: symbol, EventID: p.EventID, Seq: p.Seq, Received: now}
		return nil
	}
	ends, since := p.EndsAt, p.Since
	if ends.IsZero() {
		ends = now.Add(overlayLongest)
		if ok && !cur.EndsAt.IsZero() {
			ends = cur.EndsAt
		}
	}
	if since.IsZero() || since.After(now) {
		since = now
		if ok && !cur.Since.IsZero() {
			since = cur.Since
		}
	}
	s := OverlayState{
		Symbol: symbol, Factor: p.Factor, Until: p.Until, Risk: p.Risk, EventID: p.EventID, Seq: p.Seq, Received: now, EndsAt: ends,
		Since: since,
	}
	o.items[symbol] = s
	o.showLocked(s)
	return nil
}

// Follows reports whether a reference market follows symbol: whether a
// price event may overlay it.
func (o *Overlay) Follows(symbol string) bool { return o.followed(strings.ToUpper(symbol)) }

// Clear takes symbol back to 1 at once.
func (o *Overlay) Clear(symbol string) {
	symbol = strings.ToUpper(symbol)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.endLocked(symbol)
}

// live is symbol's overlay while it lasts and is not 1; o.mu held. One
// past its time is dropped (its end noted).
func (o *Overlay) live(symbol string, now time.Time) (OverlayState, bool) {
	s, ok := o.items[symbol]
	if !ok {
		return OverlayState{}, false
	}
	if now.After(s.Until) || now.Sub(s.Received) > overlayStale || !o.enabled() {
		o.endLocked(symbol)
		return OverlayState{}, false
	}
	return s, !s.Factor.Equal(one)
}

func (o *Overlay) endLocked(symbol string) {
	if _, ok := o.items[symbol]; !ok {
		return
	}
	delete(o.items, symbol)
	o.factor.DeleteLabelValues(symbol)
	o.endsAt.DeleteLabelValues(symbol)
}

func (o *Overlay) showLocked(s OverlayState) {
	o.factor.WithLabelValues(s.Symbol).Set(s.Factor.InexactFloat64())
	o.endsAt.WithLabelValues(s.Symbol).Set(float64(s.EndsAt.Unix()))
}

// Factor is the factor on symbol's display and trading data now (1
// without an overlay) and whether it reaches its perpetuals (risk).
func (o *Overlay) Factor(symbol string) (decimal.Decimal, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	s, ok := o.live(symbol, o.now())
	if !ok {
		return one, false
	}
	return s.Factor, s.Risk
}

// Since is when the price event on symbol began, false without one.
func (o *Overlay) Since(symbol string) (time.Time, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	s, ok := o.live(symbol, o.now())
	return s.Since, ok
}

// RiskFactor is the factor on the risk data of the perpetuals indexed on
// pair (1 without a risk overlay).
func (o *Overlay) RiskFactor(pair string) decimal.Decimal {
	f, risk := o.Factor(pair)
	if !risk {
		return one
	}
	return f
}

// MarkComputed reports whether the perpetuals indexed on pair have their
// mark computed: during a risk overlay. After it they follow their own
// source again as after the source's loss (Marks: its live mark for
// referenceRecover first; review GD, J0 contract §8 question 2).
func (o *Overlay) MarkComputed(pair string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	s, ok := o.live(pair, o.now())
	return ok && s.Risk
}

// peak is the highest and lowest last price an overlay showed of a
// symbol, kept a day: the 24-hour high and low include it after the event
// as they would a real spike.
type peak struct {
	event     string
	at        time.Time
	high, low decimal.Decimal
}

// Ticker is a reference ticker of symbol with the factor f on it (the
// caller's: a pair's own, a perpetual's from its index pair): the last
// price (and, scaleQuotes, the best bid and ask) scaled, the overlay's
// peaks of the day in the high and low, the change from the open again.
func (o *Overlay) Ticker(symbol string, tk domain.Ticker, f decimal.Decimal, scaleQuotes bool) domain.Ticker {
	now := o.now()
	o.mu.Lock()
	defer o.mu.Unlock()
	if !f.Equal(one) {
		tick := o.Tick(symbol)
		tk.Last = domain.ScalePrice(tk.Last, f, tick)
		if scaleQuotes {
			tk.Bid, tk.Ask = domain.ScalePrice(tk.Bid, f, tick), domain.ScalePrice(tk.Ask, f, tick)
		}
		event := ""
		if s, ok := o.items[domain.IndexPairOf(symbol)]; ok {
			event = s.EventID
		}
		list := o.peaks[symbol]
		if k := len(list); k > 0 && list[k-1].event == event {
			list[k-1].high, list[k-1].low = decimal.Max(list[k-1].high, tk.Last), decimal.Min(list[k-1].low, tk.Last)
			list[k-1].at = now
		} else {
			o.peaks[symbol] = append(list, peak{event: event, at: now, high: tk.Last, low: tk.Last})
		}
	}
	kept := o.peaks[symbol][:0]
	for _, p := range o.peaks[symbol] {
		if now.Sub(p.at) > 24*time.Hour {
			continue
		}
		kept = append(kept, p)
		if tk.High.IsPositive() {
			tk.High = decimal.Max(tk.High, p.high)
		}
		if tk.Low.IsPositive() {
			tk.Low = decimal.Min(tk.Low, p.low)
		}
	}
	if len(kept) == 0 {
		delete(o.peaks, symbol)
	} else {
		o.peaks[symbol] = kept
	}
	if !f.Equal(one) && tk.Open.IsPositive() {
		tk.Change = tk.Last.Sub(tk.Open).Div(tk.Open).Round(8)
	}
	return tk
}

// List returns the overlays not at 1, by symbol.
func (o *Overlay) List() []OverlayState {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := o.now()
	var out []OverlayState
	for symbol := range o.items {
		if s, ok := o.live(symbol, now); ok {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b OverlayState) int { return strings.Compare(a.Symbol, b.Symbol) })
	return out
}
