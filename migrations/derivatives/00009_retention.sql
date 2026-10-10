-- M1 (user decision 2026-10-10: Postgres keeps the last 15 days).
--
-- The fills older than that are deleted, but the engine's trade events
-- stay on their topic 30 days and may come again (a redelivery, a replay
-- from the dead-letter topic). A fill is applied once by its key
-- (trade_id, side); the retention moves the keys of the fills it deletes
-- here, kept 90 days, and a trade looks here too before it is applied
-- (HOUSE's side has no order to tell it was).
--
-- The retention deletes the finished orders by their last change, a
-- batch at a time: without an index each batch would scan the table from
-- its start, rows of every age once vacuum lets new ones into old space.

-- +goose Up
CREATE TABLE fill_keys (
    trade_id    uuid        NOT NULL,
    side        text        NOT NULL CHECK (side IN ('BUY', 'SELL')),
    executed_at timestamptz NOT NULL,
    PRIMARY KEY (trade_id, side)
);
CREATE INDEX fill_keys_executed ON fill_keys (executed_at);
CREATE INDEX orders_finished ON orders (updated_at) WHERE status IN ('FILLED', 'CANCELED', 'REJECTED', 'EXPIRED');

-- +goose Down
DROP INDEX orders_finished;
DROP TABLE fill_keys;
