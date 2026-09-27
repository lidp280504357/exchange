// Package migrations embeds the SQL migrations of every service. Each
// PostgreSQL service has a directory named after its schema; ClickHouse
// migrations live in clickhouse/.
package migrations

import "embed"

// ClickHouse holds the analytics tables applied by analytics-consumer.
//
//go:embed clickhouse/*.sql
var ClickHouse embed.FS
