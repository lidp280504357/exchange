-- Margin trading's read models (design 2026-10-06 §5.3), projected by
-- analytics-consumer from margin.events, and the scope of ledger lines
-- (the pair of an isolated margin account; ledger.EntryPosted carries it).

-- +goose NO TRANSACTION
-- +goose Up
ALTER TABLE ledger_entries ADD COLUMN IF NOT EXISTS scope LowCardinality(String) DEFAULT '' AFTER account_type;

-- Every hour's interest of a margin account's asset (MarginInterestAccrued):
-- the principal it was charged on, the model and rate, the interest and
-- what was owed after it. ReplacingMergeTree collapses redelivered copies.
CREATE TABLE IF NOT EXISTS margin_interest
(
    interest_id    UUID,
    user_id        UUID,
    account_type   LowCardinality(String),
    symbol         LowCardinality(String),
    asset          LowCardinality(String),
    principal      Decimal128(18),
    interest_model LowCardinality(String),
    hourly_rate    Decimal128(18),
    interest       Decimal128(18),
    interest_owed  Decimal128(18),
    hour           DateTime64(3, 'UTC'),
    journal_id     String
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (user_id, hour, interest_id);

-- One row per liquidation, merged from its two events: MarginLiquidationStarted
-- sets the level and values that triggered it, MarginLiquidationCompleted
-- what was repaid, the fee, the insurance fund's part and what was left
-- (asset amounts as JSON arrays). Each column keeps its last value that is
-- not NULL, so the order the events arrive in and redeliveries do not
-- matter; read with FINAL (or GROUP BY liquidation_id with anyLast).
-- status is COMPLETED once completed_at is set.
CREATE TABLE IF NOT EXISTS margin_liquidations
(
    liquidation_id    UUID,
    user_id           SimpleAggregateFunction(anyLast, Nullable(UUID)),
    account_type      SimpleAggregateFunction(anyLast, Nullable(String)),
    symbol            SimpleAggregateFunction(anyLast, Nullable(String)),
    margin_level      SimpleAggregateFunction(anyLast, Nullable(Decimal128(18))),
    total_asset       SimpleAggregateFunction(anyLast, Nullable(Decimal128(18))),
    total_liability   SimpleAggregateFunction(anyLast, Nullable(Decimal128(18))),
    started_at        SimpleAggregateFunction(anyLast, Nullable(DateTime64(3, 'UTC'))),
    repaid            SimpleAggregateFunction(anyLast, Nullable(String)),
    fee               SimpleAggregateFunction(anyLast, Nullable(Decimal128(18))),
    insurance_covered SimpleAggregateFunction(anyLast, Nullable(Decimal128(18))),
    remaining         SimpleAggregateFunction(anyLast, Nullable(String)),
    completed_at      SimpleAggregateFunction(anyLast, Nullable(DateTime64(3, 'UTC')))
)
ENGINE = AggregatingMergeTree
ORDER BY liquidation_id;

-- +goose Down
DROP TABLE IF EXISTS margin_liquidations;
DROP TABLE IF EXISTS margin_interest;
ALTER TABLE ledger_entries DROP COLUMN IF EXISTS scope;
