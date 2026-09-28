-- Ledger lines for analysis and reconciliation (requirements §9, §5.9),
-- projected from ledger.EntryPosted by analytics-consumer. One row per
-- journal line; ReplacingMergeTree collapses redelivered copies.

-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE IF NOT EXISTS ledger_entries
(
    journal_id      UUID,
    seq             Int64,
    line_no         UInt16,
    entry_type      LowCardinality(String),
    account_id      UUID,
    owner_type      LowCardinality(String),
    owner_id        String,
    account_type    LowCardinality(String),
    asset           LowCardinality(String),
    amount          Decimal128(18),
    balance_kind    LowCardinality(String),
    available_after Decimal128(18),
    frozen_after    Decimal128(18),
    posted_at       DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(posted_at)
ORDER BY (asset, posted_at, journal_id, line_no);

-- +goose Down
DROP TABLE IF EXISTS ledger_entries;
