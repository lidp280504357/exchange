-- The retention run's batches (M1, B200): the orders it deletes - ended
-- and released, or never frozen - by their last change, and the fills by
-- when they were executed, so a batch reads the rows it deletes instead
-- of scanning the table from its start once vacuum lets new rows into old
-- space. The partial index's condition is the policy's (Retention's gone).
-- orders is large: built and dropped CONCURRENTLY, outside a transaction,
-- and a failed build's INVALID index is dropped first (as 00009).

-- +goose NO TRANSACTION
-- +goose Up
DROP INDEX CONCURRENTLY IF EXISTS orders_finished;
CREATE INDEX CONCURRENTLY orders_finished ON orders (updated_at)
    WHERE status IN ('FILLED', 'CANCELED', 'REJECTED', 'EXPIRED') AND (released OR freeze_state = 'NONE');
DROP INDEX CONCURRENTLY IF EXISTS fills_executed;
CREATE INDEX CONCURRENTLY fills_executed ON fills (executed_at);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS fills_executed;
DROP INDEX CONCURRENTLY IF EXISTS orders_finished;
