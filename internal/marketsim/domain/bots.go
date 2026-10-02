package domain

import (
	"math"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/shopspring/decimal"
)

// Role is what a bot does (§4).
type Role string

// The roles: makers quote both sides around the target, takers buy and
// sell at the market at random, trend followers trade the target's recent
// direction, executors push the printed price after the target while an
// event moves it or the quotes walk the price band.
const (
	RoleMaker    Role = "MAKER"
	RoleTaker    Role = "TAKER"
	RoleTrend    Role = "TREND"
	RoleExecutor Role = "EXECUTOR"
)

// Side is an order's side.
type Side string

// The sides.
const (
	Buy  Side = "BUY"
	Sell Side = "SELL"
)

// Pair is what the bots need of the pair they trade: its rules, its price
// band (a share of the anchor a limit price may be off; 0: none) and
// whether it trades.
type Pair struct {
	Symbol        string
	Tick, Lot     decimal.Decimal
	MinQty        decimal.Decimal
	MinNotional   decimal.Decimal
	QuoteDecimals int32
	Band          float64
	Status        string
	Trading       bool
}

// Order is one of a bot's open orders.
type Order struct {
	ID        string
	Side      Side
	Price     decimal.Decimal
	Canceling bool
}

// LadderPrices are the prices a maker quotes around the target: Levels on
// each side, on a grid of LevelTicks ticks shifted by the maker's phase
// (makers on different phases fill the gaps between each other's levels),
// the best bid and ask about the spread apart. While the target moves less
// than a grid step most levels stay where they are, so a requote changes
// few orders.
func LadderPrices(target float64, phase int, p Params, pair Pair) (bids, asks []decimal.Decimal) {
	tick := pair.Tick.InexactFloat64()
	if target <= 0 || tick <= 0 {
		return nil, nil
	}
	grid := int64(max(p.LevelTicks, 1))
	ph := int64(phase) % grid
	mid := target / tick
	half := mid * p.Spread / 2
	bestBid := int64(math.Floor((mid-half-float64(ph))/float64(grid)))*grid + ph
	bestAsk := int64(math.Ceil((mid+half-float64(ph))/float64(grid)))*grid + ph
	if bestAsk <= bestBid {
		bestAsk = bestBid + grid
	}
	for i := range int64(p.Levels) {
		if b := bestBid - i*grid; b >= 1 {
			bids = append(bids, pair.Tick.Mul(decimal.NewFromInt(b)))
		}
		asks = append(asks, pair.Tick.Mul(decimal.NewFromInt(bestAsk+i*grid)))
	}
	return bids, asks
}

// Diff is how a maker gets from its open orders to the prices it wants:
// the orders to cancel (at prices no longer wanted, or a second one at a
// price) and the prices still to quote. Orders being canceled are left
// alone and do not count.
func Diff(open []Order, bids, asks []decimal.Decimal) (cancel []Order, placeBids, placeAsks []decimal.Decimal) {
	want := map[Side][]decimal.Decimal{Buy: slices.Clone(bids), Sell: slices.Clone(asks)}
	for _, o := range open {
		if o.Canceling {
			continue
		}
		if i := slices.IndexFunc(want[o.Side], o.Price.Equal); i >= 0 {
			want[o.Side] = slices.Delete(want[o.Side], i, i+1)
			continue
		}
		cancel = append(cancel, o)
	}
	return cancel, want[Buy], want[Sell]
}

// Quantity is how much of the base a worth (in the quote) buys at price,
// down to the lot, at least the pair's minimum quantity and notional; zero
// when the price is not positive.
func Quantity(worth float64, price decimal.Decimal, pair Pair) decimal.Decimal {
	if !price.IsPositive() || worth <= 0 {
		return decimal.Zero
	}
	q := decimal.NewFromFloat(worth).Div(price)
	if pair.Lot.IsPositive() {
		q = q.Div(pair.Lot).Floor().Mul(pair.Lot)
	}
	q = decimal.Max(q, pair.MinQty)
	if pair.MinNotional.IsPositive() && q.Mul(price).LessThan(pair.MinNotional) {
		need := pair.MinNotional.Div(price)
		if pair.Lot.IsPositive() {
			need = need.Div(pair.Lot).Ceil().Mul(pair.Lot)
		}
		q = need
	}
	return q
}

// Worth draws an order's worth: log-normal around median, its logarithm's
// spread sd.
func Worth(rng *rand.Rand, median, sd float64) float64 {
	return median * math.Exp(sd*rng.NormFloat64())
}

// The day's turnover (DailyVolume) is the takers' and the trend
// followers', TrendShare of it the trend followers'. Their orders' worths
// are log-normal with the spread OrderSpread, whose mean is the median
// times exp(OrderSpread²/2).
const (
	TrendShare  = 0.2
	OrderSpread = 0.8
)

// MeanWorth is the mean of a log-normal worth with median and spread sd.
func MeanWorth(median, sd float64) float64 { return median * math.Exp(sd*sd/2) }

// TrendWorth is the median worth of a trend follower's order that makes
// the trend followers' day TrendShare of DailyVolume: n of them, each
// acting with TrendStrength's chance once every every on average.
func TrendWorth(p Params, n int, every time.Duration) float64 {
	if n <= 0 || p.TrendStrength <= 0 || every <= 0 || p.DailyVolume <= 0 {
		return 0
	}
	perDay := float64(24*time.Hour) / float64(every)
	return p.DailyVolume * TrendShare / (perDay * float64(n) * p.TrendStrength) / MeanWorth(1, OrderSpread)
}

// hourWeights shape the day's taker flow (UTC hours): quieter late in the
// American evening, busiest while Europe and America overlap.
var hourWeights = func() [24]float64 {
	w := [24]float64{
		1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, // Asia
		1.1, 1.1, 1.1, 1.1, 1.1, 1.1, // Europe
		1.4, 1.4, 1.4, 1.4, // Europe and America
		1.2, 1.2, 1.2, 1.2, // America
		0.6, 0.6, 0.6, // late
	}
	sum := 0.0
	for _, x := range w {
		sum += x
	}
	for i := range w {
		w[i] *= 24 / sum // they average 1
	}
	return w
}()

// Arrivals draws how many taker orders arrive in dt: Poisson, so that a
// day of them is worth daily in orders of the median worth orderSize (the
// mean is MeanWorth), shaped by the hour's weight.
func Arrivals(rng *rand.Rand, daily, orderSize float64, dt time.Duration, now time.Time) int {
	if daily <= 0 || orderSize <= 0 || dt <= 0 {
		return 0
	}
	perSecond := daily / MeanWorth(orderSize, OrderSpread) / 86400 * hourWeights[now.UTC().Hour()]
	return poisson(rng, perSecond*dt.Seconds())
}

// poisson draws a Poisson count with mean lambda (Knuth; lambda is small).
func poisson(rng *rand.Rand, lambda float64) int {
	if lambda <= 0 {
		return 0
	}
	l, k, prod := math.Exp(-lambda), 0, 1.0
	for {
		prod *= rng.Float64()
		if prod <= l {
			return k
		}
		k++
	}
}

// TakerSide picks a taker order's side: even odds, tilted by the drift (up
// to ten points) and by the bot's lean (−1 to sell what it has too much
// of, +1 to buy).
func TakerSide(rng *rand.Rand, p Params, lean float64) Side {
	buy := 0.5 + math.Max(-0.1, math.Min(0.1, p.Mu*2)) + 0.2*math.Max(-1, math.Min(1, lean))
	if rng.Float64() < math.Max(0.1, math.Min(0.9, buy)) {
		return Buy
	}
	return Sell
}

// Lean is which way a bot should trade to keep its USDT near target: −1
// (sell the coin for USDT) at none left, 0 at target, +1 at twice it.
func Lean(usdt, target float64) float64 {
	if target <= 0 {
		return 0
	}
	return math.Max(-1, math.Min(1, usdt/target-1))
}

// TrendSide is the side a trend follower takes: the direction of the
// target over the window, from the oldest mark in it to the newest; false
// when the history does not cover the window or nothing moved.
func TrendSide(history []Mark, now time.Time, window time.Duration) (Side, bool) {
	if len(history) < 2 || history[0].At.After(now.Add(-window)) {
		return "", false
	}
	i, _ := slices.BinarySearchFunc(history, now.Add(-window), func(m Mark, t time.Time) int { return m.At.Compare(t) })
	if i >= len(history)-1 {
		return "", false
	}
	from, to := history[i].P, history[len(history)-1].P
	switch {
	case to > from:
		return Buy, true
	case to < from:
		return Sell, true
	}
	return "", false
}

// Bucket is a token bucket: Rate tokens a second, at most Burst saved.
type Bucket struct {
	Rate, Burst float64
	tokens      float64
	at          time.Time
}

// Take spends a token if there is one.
func (b *Bucket) Take(now time.Time) bool { return b.TakeLeaving(now, 0) }

// TakeLeaving spends a token if keep more are left after it (at most all
// but one of the burst): the makers leave the other roles their share, so
// that requotes cannot starve them.
func (b *Bucket) TakeLeaving(now time.Time, keep float64) bool {
	if b.at.IsZero() {
		b.tokens, b.at = b.Burst, now
	}
	b.tokens = math.Min(b.Burst, b.tokens+now.Sub(b.at).Seconds()*b.Rate)
	b.at = now
	if b.tokens < 1+math.Max(0, math.Min(keep, b.Burst-1)) {
		return false
	}
	b.tokens--
	return true
}

// Return gives back the token of an order the platform refused: only
// orders that reach the book count against the throttle.
func (b *Bucket) Return() { b.tokens = math.Min(b.Burst, b.tokens+1) }

// Backoff is how long a bot waits after a refused order: twice its last
// wait, from a second up to a minute.
func Backoff(last time.Duration) time.Duration { return min(max(2*last, time.Second), time.Minute) }
