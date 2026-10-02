// Package domain is the simulated market of the platform coin ASTRA (ASTRA
// design §3, §4): the target price the bots trade around and what each
// kind of bot does. The model works in float64 (logarithms and Gaussian
// noise); prices and quantities leave it as decimals on the pair's grid.
package domain

import (
	"errors"
	"fmt"
	"math"
)

// MaxMinuteMove is the most max_minute_move may be (§6.2): 5% a minute.
const MaxMinuteMove = 0.05

// The hard limits of the other settings (review M4): the price's floor
// and ceiling stay within the defaults, and no setting sends the platform
// more orders, or bigger ones, than a busy real market would — whoever
// signs the change.
const (
	HardFloor           = 0.0001
	HardCeiling         = 1_000_000
	MaxOrdersPerSecond  = 100
	MaxCancelsPerSecond = 100
	MaxDailyVolume      = 100_000_000 // USDT a day, the takers and the trend followers
	MaxOrderSize        = 50_000      // USDT, a taker's median order and a maker's median level
	MaxBotUSDT          = 10_000_000
	MaxPerpDailyVolume  = 100_000_000
	MaxPerpBotCap       = 10_000_000
)

// Params are the simulated market's settings.
type Params struct {
	// The price model (§3): the anchor price; the market factor's
	// weights of BTC's and ETH's returns and its beta; the own deviation's
	// mean reversion (per hour), volatility (per square root of a day) and
	// drift (per day).
	P0    float64 `json:"p0"`
	WBTC  float64 `json:"w_btc"`
	WETH  float64 `json:"w_eth"`
	Beta  float64 `json:"beta"`
	Theta float64 `json:"theta"`
	Sigma float64 `json:"sigma"`
	Mu    float64 `json:"mu"`
	// Guards: the most the target moves in a minute, and its floor and
	// ceiling.
	MaxMinuteMove float64 `json:"max_minute_move"`
	Floor         float64 `json:"floor"`
	Ceiling       float64 `json:"ceiling"`
	// Makers (§4): levels per side, the spread between the best bid and
	// ask, the gap between levels in ticks, a level's median worth in USDT,
	// and how far the target moves (in ticks) before a maker requotes.
	Levels      int     `json:"levels"`
	Spread      float64 `json:"spread"`
	LevelTicks  int     `json:"level_ticks"`
	LevelSize   float64 `json:"level_size"`
	RequoteTick int     `json:"requote_ticks"`
	// Takers: the day's turnover to aim at, in USDT, and an order's median
	// worth; trend followers: the window in minutes and how likely they
	// act on it.
	DailyVolume   float64 `json:"daily_volume"`
	OrderSize     float64 `json:"order_size"`
	TrendMinutes  int     `json:"trend_minutes"`
	TrendStrength float64 `json:"trend_strength"`
	// Throttle: orders and cancels a second, all bots together.
	OrdersPerSecond  float64 `json:"orders_per_second"`
	CancelsPerSecond float64 `json:"cancels_per_second"`
	// BotUSDT is the USDT a bot keeps about: one with less leans to
	// selling the coin, one with more to buying it.
	BotUSDT float64 `json:"bot_usdt"`
	// The perpetual (A4): the takers' day of turnover on it, the most a
	// bot's position may be worth (beyond, it only reduces), the FUTURES
	// margin a bot is topped up to.
	PerpDailyVolume float64 `json:"perp_daily_volume"`
	PerpBotCap      float64 `json:"perp_bot_cap"`
	PerpMargin      float64 `json:"perp_margin"`
}

// DefaultParams are the design's (§3, §4): ASTRA near 1 USDT, following
// BTC and ETH half each, 2% volatility a day; makers 8 levels a side 0.2%
// apart; 2,000,000 USDT a day of taker flow; 20 orders and 10 cancels a
// second at most.
func DefaultParams() Params {
	return Params{
		P0: 1, WBTC: 0.5, WETH: 0.5, Beta: 1, Theta: 0.05, Sigma: 0.02, Mu: 0,
		MaxMinuteMove: 0.03, Floor: 0.0001, Ceiling: 1_000_000,
		Levels: 8, Spread: 0.002, LevelTicks: 5, LevelSize: 800, RequoteTick: 3,
		DailyVolume: 2_000_000, OrderSize: 400, TrendMinutes: 15, TrendStrength: 0.3,
		OrdersPerSecond: 20, CancelsPerSecond: 10, BotUSDT: 100_000,
		PerpDailyVolume: 1_000_000, PerpBotCap: 20_000, PerpMargin: 30_000,
	}
}

// Validate keeps the settings within what the model and the bots can
// work with.
func (p Params) Validate() error {
	var errs []error
	check := func(ok bool, format string, args ...any) {
		if !ok {
			errs = append(errs, fmt.Errorf(format, args...))
		}
	}
	finite := func(vs ...float64) bool {
		for _, v := range vs {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return false
			}
		}
		return true
	}
	check(finite(p.P0, p.WBTC, p.WETH, p.Beta, p.Theta, p.Sigma, p.Mu, p.MaxMinuteMove, p.Floor, p.Ceiling, p.Spread,
		p.LevelSize, p.DailyVolume, p.OrderSize, p.TrendStrength, p.OrdersPerSecond, p.CancelsPerSecond, p.BotUSDT,
		p.PerpDailyVolume, p.PerpBotCap, p.PerpMargin),
		"every number must be finite")
	check(p.Floor >= HardFloor && p.Ceiling <= HardCeiling && p.Ceiling > p.Floor,
		"%g <= floor < ceiling <= %g", float64(HardFloor), float64(HardCeiling))
	check(p.P0 >= p.Floor && p.P0 <= p.Ceiling, "p0 must be between the floor and the ceiling")
	check(p.WBTC >= 0 && p.WETH >= 0 && p.WBTC+p.WETH <= 1+1e-9, "the weights are not negative and add up to 1 at most")
	check(p.Beta >= 0 && p.Beta <= 5, "beta must be between 0 and 5")
	check(p.Theta >= 0 && p.Theta <= 10, "theta must be between 0 and 10 an hour")
	check(p.Sigma >= 0 && p.Sigma <= 1, "sigma must be between 0 and 1 (a day)")
	check(math.Abs(p.Mu) <= 1, "mu must be within ±1 (a day)")
	check(p.MaxMinuteMove > 0 && p.MaxMinuteMove <= MaxMinuteMove, "max_minute_move must be above 0 and at most 0.05 (5%% a minute)")
	check(p.Levels >= 1 && p.Levels <= 30, "levels must be between 1 and 30")
	check(p.Spread > 0 && p.Spread <= 0.05, "spread must be above 0 and at most 0.05")
	check(p.LevelTicks >= 1 && p.LevelTicks <= 100, "level_ticks must be between 1 and 100")
	check(p.LevelSize > 0 && p.OrderSize > 0 && p.LevelSize <= MaxOrderSize && p.OrderSize <= MaxOrderSize,
		"level_size and order_size must be above 0 and at most %d", MaxOrderSize)
	check(p.RequoteTick >= 1, "requote_ticks must be at least 1")
	check(p.DailyVolume >= 0 && p.DailyVolume <= MaxDailyVolume, "daily_volume must be between 0 and %d", MaxDailyVolume)
	check(p.TrendMinutes >= 1 && p.TrendMinutes <= 240, "trend_minutes must be between 1 and 240")
	check(p.TrendStrength >= 0 && p.TrendStrength <= 1, "trend_strength must be between 0 and 1")
	check(p.OrdersPerSecond > 0 && p.CancelsPerSecond > 0 && p.OrdersPerSecond <= MaxOrdersPerSecond && p.CancelsPerSecond <= MaxCancelsPerSecond,
		"the throttle must allow some orders and cancels, at most %d and %d a second", MaxOrdersPerSecond, MaxCancelsPerSecond)
	check(p.BotUSDT > 0 && p.BotUSDT <= MaxBotUSDT, "bot_usdt must be above 0 and at most %d", MaxBotUSDT)
	check(p.PerpDailyVolume >= 0 && p.PerpDailyVolume <= MaxPerpDailyVolume && p.PerpBotCap > 0 && p.PerpBotCap <= MaxPerpBotCap &&
		p.PerpMargin > 0 && p.PerpMargin <= MaxPerpBotCap,
		"perp_daily_volume must be between 0 and %d, perp_bot_cap and perp_margin above 0 and at most %d", MaxPerpDailyVolume, MaxPerpBotCap)
	return errors.Join(errs...)
}

// Clamp brings settings stored before a hard limit existed, or written
// around the API, within the hard limits (review of 2efbf2e: Start loads
// what is stored without UpdateParams' check) and names what it changed.
// What still fails Validate after that is the caller's to refuse.
func (p Params) Clamp() (Params, []string) {
	var changed []string
	clamp := func(name string, v *float64, lo, hi float64) {
		if c := math.Max(lo, math.Min(hi, *v)); c != *v && !math.IsNaN(*v) {
			changed = append(changed, fmt.Sprintf("%s %g -> %g", name, *v, c))
			*v = c
		}
	}
	clamp("floor", &p.Floor, HardFloor, HardCeiling)
	clamp("ceiling", &p.Ceiling, HardFloor, HardCeiling)
	clamp("p0", &p.P0, p.Floor, p.Ceiling)
	clamp("max_minute_move", &p.MaxMinuteMove, 0, MaxMinuteMove)
	clamp("orders_per_second", &p.OrdersPerSecond, 0, MaxOrdersPerSecond)
	clamp("cancels_per_second", &p.CancelsPerSecond, 0, MaxCancelsPerSecond)
	clamp("daily_volume", &p.DailyVolume, 0, MaxDailyVolume)
	clamp("order_size", &p.OrderSize, 0, MaxOrderSize)
	clamp("level_size", &p.LevelSize, 0, MaxOrderSize)
	clamp("bot_usdt", &p.BotUSDT, 0, MaxBotUSDT)
	clamp("perp_daily_volume", &p.PerpDailyVolume, 0, MaxPerpDailyVolume)
	clamp("perp_bot_cap", &p.PerpBotCap, 0, MaxPerpBotCap)
	clamp("perp_margin", &p.PerpMargin, 0, MaxPerpBotCap)
	return p, changed
}
