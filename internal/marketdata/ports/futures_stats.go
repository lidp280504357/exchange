package ports

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

// The reference market's statistics and liquidations of the perpetual
// contracts it trades (design 2026-10-06 §3.3), for the contracts' data
// panel: Binance's figures as they are, display only.

// The statistics.
const (
	MetricOpenInterest         = "open_interest"
	MetricLongShortAccount     = "long_short_account"
	MetricTopLongShortAccount  = "top_long_short_account"
	MetricTopLongShortPosition = "top_long_short_position"
	MetricTakerRatio           = "taker_ratio"
	MetricBasis                = "basis"
	// MetricFunding is the settled funding rates: one point a settlement,
	// with no period.
	MetricFunding = "funding"
)

// FuturesContract is what the statistics need of a listed contract, of
// either margin type.
type FuturesContract struct {
	Symbol string
	// CoinMargined is an inverse contract (margin_type COIN).
	CoinMargined bool
	// ReferenceSymbol is the Binance contract it follows (BTCUSDT,
	// BTCUSD_PERP); empty when none does (the platform coin's).
	ReferenceSymbol string
	// ContractSize is an inverse contract's face value in USD, zero for a
	// linear one.
	ContractSize decimal.Decimal
}

// FuturesContracts lists the contracts that are not delisted, linear and
// inverse.
type FuturesContracts interface {
	FuturesContracts(ctx context.Context) ([]FuturesContract, error)
}

// FuturesMarket is a platform contract as the reference market trades it.
type FuturesMarket struct {
	// Symbol is the platform's contract, e.g. BTC-USD-PERP.
	Symbol string
	// CoinMargined: a COIN-M contract (Binance's dapi; its statistics are
	// by pair), else a USDⓈ-M one (fapi, by symbol).
	CoinMargined bool
	// Remote is the source's contract (BTCUSDT, BTCUSD_PERP) and Pair its
	// pair (BTCUSDT, BTCUSD).
	Remote string
	Pair   string
	// ContractSize is a COIN-M contract's face value in USD.
	ContractSize decimal.Decimal
}

// FuturesStat is one point of a contract's statistic.
type FuturesStat struct {
	Symbol string
	Metric string
	// Period is 5m, 15m, 1h, 4h or 1d; empty for a funding rate.
	Period string
	// At is the source's time of the point: a snapshot's, or the start of
	// the period a volume was traded in.
	At time.Time
	// Values by name (api/openapi/market.yaml FuturesDataPoint):
	// open_interest (base asset, or contracts of a COIN-M contract) and
	// open_interest_value (USD); long_short_ratio, long and short;
	// buy_vol, sell_vol and buy_sell_ratio; basis, basis_rate,
	// futures_price and index_price; funding_rate and mark_price.
	Values map[string]decimal.Decimal
}

// Liquidation is a liquidation order of the reference market on a
// platform contract (market.v1.LiquidationOccurred).
type Liquidation struct {
	Symbol string
	// PositionSide is the side of the position closed: LONG when the
	// liquidation order sold.
	PositionSide string
	Price        decimal.Decimal
	AvgPrice     decimal.Decimal
	// Quantity is filled: in the base asset, or in contracts of a COIN-M
	// contract.
	Quantity decimal.Decimal
	// ValueUSD is the average price times the quantity, or the contracts
	// times their face value.
	ValueUSD decimal.Decimal
	At       time.Time
}

// FuturesStatsRepo stores the statistics and the recent liquidations.
type FuturesStatsRepo interface {
	// Upsert writes points, replacing stored ones.
	Upsert(ctx context.Context, stats []FuturesStat) error
	// Last returns the time of a series' latest point, zero without one.
	Last(ctx context.Context, symbol, metric, period string) (time.Time, error)
	// Recent returns a series' latest limit points, oldest first.
	Recent(ctx context.Context, symbol, metric, period string, limit int) ([]FuturesStat, error)
	// Purge deletes the points of a period (empty: the funding rates)
	// older than before.
	Purge(ctx context.Context, period string, before time.Time) (int64, error)
	// AddLiquidations stores liquidations; one stored already is kept.
	AddLiquidations(ctx context.Context, list []Liquidation) error
	// RecentLiquidations returns a contract's latest limit liquidations,
	// newest first.
	RecentLiquidations(ctx context.Context, symbol string, limit int) ([]Liquidation, error)
	// PurgeLiquidations deletes the liquidations older than before.
	PurgeLiquidations(ctx context.Context, before time.Time) (int64, error)
}

// ForcedOrder is a liquidation order of the reference market, as its
// liquidation stream reports it: the latest of a contract within a second.
type ForcedOrder struct {
	// Remote is the source's contract (BTCUSDT, BTCUSD_PERP).
	Remote string
	// Side is the order's: SELL closes a long position.
	Side     string
	Price    decimal.Decimal
	AvgPrice decimal.Decimal
	// Filled is the quantity filled: in the base asset for a USDⓈ-M
	// contract, in contracts for a COIN-M one.
	Filled decimal.Decimal
	At     time.Time
}

// Perpetual is a perpetual contract the reference market trades.
type Perpetual struct {
	// Remote is its symbol (BTCUSDT, BTCUSD_PERP) and Pair its pair
	// (BTCUSDT, BTCUSD).
	Remote string
	Pair   string
	// ContractSize is a COIN-M contract's face value in USD, zero for a
	// USDⓈ-M one.
	ContractSize decimal.Decimal
}

// ErrQuiet ends a liquidation stream that sent nothing for a while (a
// quiet market, or a connection lost without a word); the caller
// connects again.
var ErrQuiet = errors.New("liquidation stream quiet")

// FuturesSource is the reference market's futures data.
type FuturesSource interface {
	// Perpetuals lists the perpetual contracts the source trades: the
	// USDⓈ-M ones or the COIN-M ones.
	Perpetuals(ctx context.Context, coinMargined bool) ([]Perpetual, error)
	// Stats returns up to limit points of a series after after, oldest
	// first; the latest limit when after is zero.
	Stats(ctx context.Context, m FuturesMarket, metric, period string, after time.Time, limit int) ([]FuturesStat, error)
	// OpenInterest returns the open interest now (base asset or contracts)
	// and when the source counted it.
	OpenInterest(ctx context.Context, m FuturesMarket) (decimal.Decimal, time.Time, error)
	// ForcedOrders passes the liquidation orders of every USDⓈ-M or COIN-M
	// contract to on until ctx ends, the connection fails or it is quiet
	// (ErrQuiet).
	ForcedOrders(ctx context.Context, coinMargined bool, on func(ForcedOrder)) error
}
