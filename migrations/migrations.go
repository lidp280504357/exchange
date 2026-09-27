// Package migrations embeds the SQL migrations. Each PostgreSQL schema has
// a directory named after it; ClickHouse migrations live in clickhouse/.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed clickhouse/*.sql
var clickhouse embed.FS

//go:embed config/*.sql
var config embed.FS

// ClickHouse holds the analytics tables applied by analytics-consumer.
func ClickHouse() fs.FS { return sub(clickhouse, "clickhouse") }

// Config holds the shared config schema (feature flags).
func Config() fs.FS { return sub(config, "config") }

func sub(fsys embed.FS, dir string) fs.FS {
	s, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err) // the directory is embedded at build time
	}
	return s
}
