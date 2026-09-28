// Package ports declares what market-data-service's application layer
// needs.
package ports

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

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

// Pairs tells which trading pairs exist (instrument-service).
type Pairs interface {
	// Listed reports whether symbol is a listed pair (not delisted).
	Listed(ctx context.Context, symbol string) (bool, error)
	// Symbols lists the listed pairs.
	Symbols(ctx context.Context) ([]string, error)
}
