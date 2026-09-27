-- Audit trail (requirements §5.12, §9): every event of audit.events,
-- ordered by actor and time, kept without TTL.

-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE IF NOT EXISTS audit_logs
(
    event_id    UUID,
    event_type  LowCardinality(String),
    actor_id    String,
    target      String,
    occurred_at DateTime64(3, 'UTC'),
    payload     String CODEC(ZSTD(3))
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(occurred_at)
ORDER BY (actor_id, occurred_at, event_id);

-- +goose Down
DROP TABLE IF EXISTS audit_logs;
