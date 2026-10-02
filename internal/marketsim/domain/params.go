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
	check(p.Floor > 0 && p.Ceiling > p.Floor, "0 < floor < ceiling")
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
	check(p.LevelSize > 0 && p.OrderSize > 0, "level_size and order_size must be positive")
	check(p.RequoteTick >= 1, "requote_ticks must be at least 1")
	check(p.DailyVolume >= 0, "daily_volume must not be negative")
	check(p.TrendMinutes >= 1 && p.TrendMinutes <= 240, "trend_minutes must be between 1 and 240")
	check(p.TrendStrength >= 0 && p.TrendStrength <= 1, "trend_strength must be between 0 and 1")
	check(p.OrdersPerSecond > 0 && p.CancelsPerSecond > 0, "the throttle must allow some orders and cancels")
	check(p.BotUSDT > 0, "bot_usdt must be positive")
	check(p.PerpDailyVolume >= 0 && p.PerpBotCap > 0 && p.PerpMargin > 0,
		"perp_daily_volume must not be negative, perp_bot_cap and perp_margin must be positive")
	return errors.Join(errs...)
}
