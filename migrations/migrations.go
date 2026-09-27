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

func sub(fsys embed.FS, dir string) fs.FS {
	s, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err) // the directory is embedded at build time
	}
	return s
}
