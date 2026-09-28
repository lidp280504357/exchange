// Package migrations embeds the SQL migrations. Each PostgreSQL schema has
// a directory named after it; ClickHouse migrations live in clickhouse/.
package migrations

import (
	"embed"
	"io/fs"
)

var (
	//go:embed clickhouse/*.sql
	clickhouse embed.FS
	//go:embed config/*.sql
	config embed.FS
	//go:embed auth/*.sql
	auth embed.FS
	//go:embed users/*.sql
	users embed.FS
	//go:embed notify/*.sql
	notify embed.FS
	//go:embed instrument/*.sql
	instrument embed.FS
	//go:embed ledger/*.sql
	ledger embed.FS
	//go:embed risk/*.sql
	risk embed.FS
	//go:embed trading/*.sql
	trading embed.FS
	//go:embed matching/*.sql
	matching embed.FS
	//go:embed market/*.sql
	market embed.FS
	//go:embed wallet/*.sql
	wallet embed.FS
)

// ClickHouse holds the analytics tables applied by analytics-consumer.
func ClickHouse() fs.FS { return sub(clickhouse, "clickhouse") }

// Config holds the shared config schema (feature flags).
func Config() fs.FS { return sub(config, "config") }

// Auth holds auth-service's schema.
func Auth() fs.FS { return sub(auth, "auth") }

// Users holds user-service's schema.
func Users() fs.FS { return sub(users, "users") }

// Notify holds notification-service's schema.
func Notify() fs.FS { return sub(notify, "notify") }

// Instrument holds instrument-service's schema.
func Instrument() fs.FS { return sub(instrument, "instrument") }

// Ledger holds ledger-service's schema.
func Ledger() fs.FS { return sub(ledger, "ledger") }

// Risk holds risk-service's schema.
func Risk() fs.FS { return sub(risk, "risk") }

// Trading holds spot-trading-service's schema.
func Trading() fs.FS { return sub(trading, "trading") }

// Matching holds matching-engine's schema.
func Matching() fs.FS { return sub(matching, "matching") }

// Market holds market-data-service's schema.
func Market() fs.FS { return sub(market, "market") }

// Wallet holds wallet-service's schema.
func Wallet() fs.FS { return sub(wallet, "wallet") }

func sub(fsys embed.FS, dir string) fs.FS {
	s, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err) // the directory is embedded at build time
	}
	return s
}
