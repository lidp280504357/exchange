-- The read models keep the last 15 days, as Postgres does (M1, ADR-0022):
-- the raw events and their ingest log, the ledger entries, the orders and
-- their updates, the trades and fills, funding, liquidations, interest and
-- the audit log. Kept whatever their age: the orders' current state
-- (orders_state, read through orders_current: an order still open is
-- there, and the console counts a pair's open orders off it - its rows
-- come from orders and order_updates through materialized views, so
-- their expiry leaves it be), the positions, deposits, withdrawals and
-- margin liquidations by ID, and the candles the charts draw.
-- futures_liquidations keeps its 7 days.

-- +goose NO TRANSACTION
-- +goose Up
ALTER TABLE events MODIFY TTL toDateTime(occurred_at) + INTERVAL 15 DAY;
ALTER TABLE event_ingest_log MODIFY TTL toDateTime(ingested_at) + INTERVAL 15 DAY;
ALTER TABLE ledger_entries MODIFY TTL toDateTime(posted_at) + INTERVAL 15 DAY;
ALTER TABLE orders MODIFY TTL toDateTime(accepted_at) + INTERVAL 15 DAY;
ALTER TABLE order_updates MODIFY TTL toDateTime(occurred_at) + INTERVAL 15 DAY;
ALTER TABLE trades MODIFY TTL toDateTime(executed_at) + INTERVAL 15 DAY;
ALTER TABLE derivatives_fills MODIFY TTL toDateTime(executed_at) + INTERVAL 15 DAY;
ALTER TABLE derivatives_funding MODIFY TTL toDateTime(funding_time) + INTERVAL 15 DAY;
ALTER TABLE derivatives_liquidations MODIFY TTL toDateTime(occurred_at) + INTERVAL 15 DAY;
ALTER TABLE margin_interest MODIFY TTL toDateTime(hour) + INTERVAL 15 DAY;
ALTER TABLE audit_logs MODIFY TTL toDateTime(occurred_at) + INTERVAL 15 DAY;

-- +goose Down
ALTER TABLE audit_logs REMOVE TTL;
ALTER TABLE margin_interest REMOVE TTL;
ALTER TABLE derivatives_liquidations REMOVE TTL;
ALTER TABLE derivatives_funding REMOVE TTL;
ALTER TABLE derivatives_fills REMOVE TTL;
ALTER TABLE trades REMOVE TTL;
ALTER TABLE order_updates REMOVE TTL;
ALTER TABLE orders REMOVE TTL;
ALTER TABLE ledger_entries REMOVE TTL;
ALTER TABLE event_ingest_log MODIFY TTL toDateTime(ingested_at) + INTERVAL 30 DAY;
ALTER TABLE events MODIFY TTL toDateTime(occurred_at) + INTERVAL 1 YEAR;
