-- ClickHouse read model fed by analytics-consumer (requirements §9).
-- Amounts in typed tables use Decimal128(18); PII arrives already masked.

-- +goose NO TRANSACTION
-- +goose Up

-- Every event from every business topic. ReplacingMergeTree collapses
-- redelivered copies of an event (same topic, occurred_at and event_id),
-- so re-ingesting a batch after a failure is safe; query with FINAL, or
-- count(DISTINCT event_id), when exact counts matter.
CREATE TABLE IF NOT EXISTS events
(
    event_id       UUID,
    event_type     LowCardinality(String),
    event_version  Int32,
    topic          LowCardinality(String),
    partition      Int32,
    offset         Int64,
    aggregate_type LowCardinality(String),
    aggregate_id   String,
    sequence       Int64,
    producer       String,
    correlation_id String,
    causation_id   String,
    occurred_at    DateTime64(3, 'UTC'),
    ingested_at    DateTime64(3, 'UTC') DEFAULT now64(3),
    payload        String CODEC(ZSTD(3))
)
ENGINE = ReplacingMergeTree(ingested_at)
PARTITION BY toYYYYMM(occurred_at)
ORDER BY (topic, occurred_at, event_id)
TTL toDateTime(occurred_at) + INTERVAL 1 YEAR;

-- Ingest log (topic, partition, offset), kept 30 days to trace what was
-- consumed; filled from events by a materialized view.
CREATE TABLE IF NOT EXISTS event_ingest_log
(
    topic       LowCardinality(String),
    partition   Int32,
    offset      Int64,
    event_id    UUID,
    ingested_at DateTime64(3, 'UTC')
)
ENGINE = MergeTree
PARTITION BY toYYYYMMDD(ingested_at)
ORDER BY (topic, partition, offset)
TTL toDateTime(ingested_at) + INTERVAL 30 DAY;

CREATE MATERIALIZED VIEW IF NOT EXISTS event_ingest_log_mv TO event_ingest_log AS
SELECT topic, partition, offset, event_id, ingested_at FROM events;

-- +goose Down
DROP VIEW IF EXISTS event_ingest_log_mv;
DROP TABLE IF EXISTS event_ingest_log;
DROP TABLE IF EXISTS events;
