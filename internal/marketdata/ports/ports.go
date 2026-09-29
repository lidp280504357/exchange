// Package ports declares what market-data-service's application layer
// needs.
package ports

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	"github.com/lidp280504357/exchange/internal/marketdata/domain"
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
	// Upsert writes candles of source, replacing stored ones.
	Upsert(ctx context.Context, source string, candles []domain.Candle) error
	// Latest returns the latest stored candle of symbol from source, or nil.
	Latest(ctx context.Context, source, symbol string) (*domain.Candle, error)
	// Purge deletes candles opening before t.
	Purge(ctx context.Context, before time.Time) (int64, error)
}

// ReferenceSource is an external market data source (§5.11: several may
// be configured; phase 2 has Binance public data, test environments only).
type ReferenceSource interface {
	// Name identifies the source, e.g. "binance".
	Name() string
	// Backfill returns the 1m candles of symbol opening at or after from,
	// oldest first.
	Backfill(ctx context.Context, symbol string, from time.Time) ([]domain.Candle, error)
	// Stream calls on with every live 1m candle update of symbols until
	// ctx ends or the connection fails.
	Stream(ctx context.Context, symbols []string, on func(domain.Candle)) error
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
	// ImpactNotional is the quote amount the impact prices trade.
	ImpactNotional decimal.Decimal
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
