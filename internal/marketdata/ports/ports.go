// Package ports declares what market-data-service's application layer
// needs.
package ports

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	eventv1 "github.com/skill/exchange/api/gen/go/exchange/event/v1"
	"github.com/skill/exchange/internal/marketdata/domain"
)

// Store is the unit of work over the market schema.
type Store interface {
	// Tx runs fn in one transaction.
	Tx(ctx context.Context, fn func(Repos) error) error
	// Read returns repositories outside any transaction.
	Read() Repos
}

// Repos groups the repositories of one transaction.
type Repos interface {
	Symbols() SymbolRepo
	Candles() CandleRepo
	Trades() TradeRepo
	References() ReferenceRepo
	Funding() FundingRepo
	// Halts are the pairs halted for the reference feed's loss, SimHalts
	// those halted for their simulated market's.
	Halts() HaltRepo
	SimHalts() HaltRepo
	// SimHeartbeats are when the simulated markets last reported.
	SimHeartbeats() HeartbeatRepo
	// Emit queues an event on the outbox (business events that must not
	// be lost, such as risk.events' SystemDegraded; derived market data
	// goes out directly).
	Emit(ctx context.Context, topic string, env *eventv1.Envelope) error
}

// SymbolState is what has been applied of a symbol's trades.
type SymbolState struct {
	Symbol string
	// Sequence is the engine sequence of the last trade applied; trades at
	// or below it are skipped (§11.8).
	Sequence  int64
	LastPrice decimal.Decimal
	LastAt    time.Time
}

// SymbolRepo stores the per-symbol progress.
type SymbolRepo interface {
	All(ctx context.Context) ([]SymbolState, error)
	Save(ctx context.Context, s SymbolState) error
}

// CandleRepo stores candles; only intervals with trades are stored.
type CandleRepo interface {
	// Upsert writes candles, replacing stored ones of the same interval.
	Upsert(ctx context.Context, candles []domain.Candle) error
	// Latest returns the latest stored candle of each interval of symbol.
	Latest(ctx context.Context, symbol string) ([]domain.Candle, error)
	// Range returns the stored candles opening in [from, to), oldest first.
	Range(ctx context.Context, symbol string, i domain.Interval, from, to time.Time) ([]domain.Candle, error)
	// Before returns the latest stored candle opening before t, or nil.
	Before(ctx context.Context, symbol string, i domain.Interval, t time.Time) (*domain.Candle, error)
}

// TradeRepo keeps recent public trades.
type TradeRepo interface {
	// Insert stores trades; ones already stored are ignored.
	Insert(ctx context.Context, trades []domain.Trade) error
	// Recent returns up to limit of the symbol's latest trades, newest first.
	Recent(ctx context.Context, symbol string, limit int) ([]domain.Trade, error)
	// Purge deletes trades executed before t.
	Purge(ctx context.Context, before time.Time) (int64, error)
}

// ReferenceRepo keeps the 1m candles of external reference sources
// (§11.9), apart from the platform's own.
type ReferenceRepo interface {
	// Upsert writes candles of source, replacing stored ones but those a
	// price event touched.
	Upsert(ctx context.Context, source string, candles []domain.Candle) error
	// UpsertOverlaid writes candles of source a price event touched
	// (design 2026-10-07, general price control): Upsert does not replace
	// them, Purge keeps them, and the charts lay them over the reference
	// market's (review GD ③).
	UpsertOverlaid(ctx context.Context, source string, candles []domain.Candle) error
	// Overlaid returns the candles of symbol from source a price event
	// touched, opening from from to before to, oldest first.
	Overlaid(ctx context.Context, source, symbol string, from, to time.Time) ([]domain.Candle, error)
	// Latest returns the latest stored candle of symbol from source, or nil.
	Latest(ctx context.Context, source, symbol string) (*domain.Candle, error)
	// Purge deletes candles opening before t.
	Purge(ctx context.Context, before time.Time) (int64, error)
}

// Reference is a pair's reference market (ADR-0010): the source's symbol
// and how its prices convert to the pair's.
type Reference struct {
	// Symbol is the platform pair or contract, e.g. 1000PEPE-USDT.
	Symbol string
	// Remote is the source's symbol, e.g. PEPEUSDT, BTCUSD_PERP.
	Remote string
	// Multiplier is a power of ten: platform prices are the source's times
	// it, platform quantities the source's divided by it (ADR-0014).
	Multiplier decimal.Decimal
	// Market is where Remote trades: MarketSpot for a pair, a futures
	// market for a contract (coin-M design §3.2).
	Market string
	// ContractSize is a COIN-M contract's face value in USD: its
	// quantities are whole contracts, their USD value quantity times it.
	ContractSize decimal.Decimal
}

// The reference markets of a Reference.
const (
	MarketSpot  = ""
	MarketUSDM  = "USDM"  // USDⓈ-M perpetuals: quantities in the base asset
	MarketCoinM = "COINM" // COIN-M perpetuals: quantities in contracts
)

// SameReference reports whether a and b follow the same market alike.
func SameReference(a, b Reference) bool {
	return a.Symbol == b.Symbol && a.Remote == b.Remote && a.Multiplier.Equal(b.Multiplier) && a.Market == b.Market &&
		a.ContractSize.Equal(b.ContractSize)
}

// StreamHandlers take a reference stream's updates, already in the
// platform's symbols and units.
type StreamHandlers struct {
	// Candle gets every 1m candle update.
	Candle func(domain.Candle)
	// Ticker gets every rolling 24-hour ticker update.
	Ticker func(domain.Ticker)
}

// BookHandlers take a reference book stream's updates, already in the
// platform's symbols and units.
type BookHandlers struct {
	// Depth gets every depth update of a symbol's book.
	Depth func(symbol string, d domain.DepthDiff)
	// Trade gets every (aggregate) trade.
	Trade func(domain.Trade)
	// Failed, when set, gets the failures of a trade connection the
	// source keeps up of its own (it reconnects; the books go on).
	Failed func(error)
}

// BookSource is the reference market's order books and trades (ADR-0010,
// ADR-0015), of spot pairs and perpetual contracts, each on its
// reference's market.
type BookSource interface {
	// DepthSnapshot returns ref's book and the update ID it stands at.
	DepthSnapshot(ctx context.Context, ref Reference) (lastID int64, bids, asks []domain.Level, err error)
	// RecentTrades returns ref's latest trades, oldest first.
	RecentTrades(ctx context.Context, ref Reference, limit int) ([]domain.Trade, error)
	// BookStream passes every depth update and trade of refs, all of one
	// market, to on until ctx ends or the connection fails.
	BookStream(ctx context.Context, refs []Reference, on BookHandlers) error
}

// MarkSource is the reference market's mark prices and settled funding
// rates of perpetual contracts (coin-M design §3.1); the refs of a call
// are all of one futures market.
type MarkSource interface {
	// MarkStream passes every mark price update of refs to on, about one
	// a second each, until ctx ends or the connection fails.
	MarkStream(ctx context.Context, refs []Reference, on func(domain.ReferenceMark)) error
	// SettledFunding returns the rates refs settled with funding times in
	// [from, to], oldest first.
	SettledFunding(ctx context.Context, refs []Reference, from, to time.Time) ([]domain.SettledFunding, error)
}

// Halt is a pair market-data-service halted when the reference feed was
// lost.
type Halt struct {
	Symbol   string
	HaltedAt time.Time
}

// HaltRepo remembers the pairs halted on feed loss.
type HaltRepo interface {
	List(ctx context.Context) ([]Halt, error)
	// Add records a halt; one recorded already is kept.
	Add(ctx context.Context, symbol string, at time.Time) error
	Remove(ctx context.Context, symbol string) error
}

// HeartbeatRepo remembers when each simulated market last reported.
type HeartbeatRepo interface {
	List(ctx context.Context) (map[string]time.Time, error)
	// Save records a report at at, unless a later one is recorded.
	Save(ctx context.Context, symbol string, at time.Time) error
}

// ReferenceSource is an external market data source (§5.11: several may
// be configured; Binance public data, test environments only until a
// license exists, ADR-0004 and ADR-0010). Everything it returns is in the
// platform's symbols and units.
type ReferenceSource interface {
	// Name identifies the source, e.g. "binance".
	Name() string
	// Backfill returns the 1m candles of ref opening in [from, to), oldest
	// first.
	Backfill(ctx context.Context, ref Reference, from, to time.Time) ([]domain.Candle, error)
	// Tickers returns the current rolling 24-hour tickers of refs.
	Tickers(ctx context.Context, refs []Reference) ([]domain.Ticker, error)
	// Stream passes every live 1m candle and ticker update of refs to on
	// until ctx ends or the connection fails.
	Stream(ctx context.Context, refs []Reference, on StreamHandlers) error
}

// ReferenceHistory returns a reference source's candles of any interval,
// for reference K-lines (market.reference_kline).
type ReferenceHistory interface {
	// Klines returns the latest limit candles of ref at interval opening
	// before to (up to now when zero), oldest first.
	Klines(ctx context.Context, ref Reference, interval domain.Interval, to time.Time, limit int) ([]domain.Candle, error)
}

// Pair is what market data needs of a spot trading pair.
type Pair struct {
	Symbol string
	Base   string
	Quote  string
	Status string
	// Rank is the base asset's market-cap rank, 0 when unranked.
	Rank int32
	// Reference is the market the pair's data follows; Remote is empty
	// when it follows none.
	Reference Reference
}

// Instruments tells which trading pairs and contracts exist
// (instrument-service).
type Instruments interface {
	// Listed reports whether symbol is a listed pair or contract (not
	// delisted).
	Listed(ctx context.Context, symbol string) (bool, error)
	// Symbols lists the listed pairs and contracts.
	Symbols(ctx context.Context) ([]string, error)
	// Contracts lists the perpetual contracts that are not delisted.
	Contracts(ctx context.Context) ([]Contract, error)
	// Pairs lists the spot pairs that are not delisted.
	Pairs(ctx context.Context) ([]Pair, error)
	// Ranks returns the base asset's rank of every listed pair and
	// contract (0: unranked).
	Ranks(ctx context.Context) (map[string]int32, error)
	// SetPairStatus moves a pair along its status machine for
	// market-data-service itself (a halt on reference feed loss) and
	// returns the previous status.
	SetPairStatus(ctx context.Context, symbol, to, reason string) (string, error)
	// SetContractStatus does the same for a contract (the platform coin's
	// perpetual halts with its index pair).
	SetContractStatus(ctx context.Context, symbol, to, reason string) (string, error)
}

// Contract is what the mark price and funding need of a perpetual
// contract (requirements §11.7).
type Contract struct {
	Symbol string
	// IndexSymbol is the spot symbol of the index, e.g. BTC-USDT.
	IndexSymbol          string
	Status               string
	FundingIntervalHours int32
	// InterestRate per funding period; FundingCap bounds the rate.
	InterestRate decimal.Decimal
	FundingCap   decimal.Decimal
	// ImpactNotional is the quote amount the impact prices trade, or the
	// contracts of an inverse contract (coin-M design §2.1).
	ImpactNotional decimal.Decimal
	// MarginType is USDT (linear) or COIN (inverse); ContractSize an
	// inverse contract's face value in USD, zero for a linear one.
	MarginType   string
	ContractSize decimal.Decimal
	// ReferenceSymbol is the Binance contract the contract's market data
	// and, with market.reference_mark, its mark price follow: BTCUSDT
	// (USDⓈ-M) or BTCUSD_PERP (COIN-M); empty when none does.
	ReferenceSymbol string
}

// MarginCoin is an inverse (coin-margined) contract's margin type.
const MarginCoin = "COIN"

// Inverse reports whether the contract is coin-margined.
func (c Contract) Inverse() bool { return c.MarginType == MarginCoin }

// Reference is the contract's own reference market, false when it has
// none.
func (c Contract) Reference() (Reference, bool) {
	if c.ReferenceSymbol == "" {
		return Reference{}, false
	}
	r := Reference{Symbol: c.Symbol, Remote: c.ReferenceSymbol, Multiplier: decimal.NewFromInt(1), Market: MarketUSDM}
	if c.Inverse() {
		r.Market, r.ContractSize = MarketCoinM, c.ContractSize
	}
	return r, true
}

// FundingPeriod is a contract's premium index samples over one funding
// period and, once the period has ended, its settled rate.
type FundingPeriod struct {
	Symbol string
	// FundingTime is the end of the period, when it settles.
	FundingTime time.Time
	PremiumSum  decimal.Decimal
	Samples     int64
	// Set when settled.
	Settled      bool
	Rate         decimal.Decimal
	Premium      decimal.Decimal
	InterestRate decimal.Decimal
	MarkPrice    decimal.Decimal
	IndexPrice   decimal.Decimal
	SettledAt    time.Time
	// Source is where the settled rate came from: PLATFORM (the samples)
	// or BINANCE (coin-M design §3.1).
	Source string
}

// FundingRepo stores the funding periods.
type FundingRepo interface {
	// Save writes a running period's samples; a settled period is left
	// alone.
	Save(ctx context.Context, p FundingPeriod) error
	// Unsettled returns the periods not settled yet, oldest first.
	Unsettled(ctx context.Context) ([]FundingPeriod, error)
	// Settle records a period's rate and final samples; false when it was
	// settled already.
	Settle(ctx context.Context, p FundingPeriod) (bool, error)
	// Settled returns up to limit settled periods of symbol with funding
	// times in [from, to), newest first.
	Settled(ctx context.Context, symbol string, from, to time.Time, limit int) ([]FundingPeriod, error)
}
