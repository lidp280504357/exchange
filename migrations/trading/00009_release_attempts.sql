-- A finished order whose release did not complete (a margin order's
-- repayment waiting for its trades' settlement, the ledger unreachable)
-- goes to the back of the recovery's queue once tried (B164): a pass takes
-- the 100 longest untried, since they finished or since their last try,
-- so ones that keep failing no longer starve the rest. NULL until a try
-- fails. orders is large: the indexes are built and dropped CONCURRENTLY,
-- outside a transaction, and each step can run again.

-- +goose NO TRANSACTION
-- +goose Up
ALTER TABLE orders ADD COLUMN IF NOT EXISTS release_attempted_at timestamptz;
CREATE INDEX CONCURRENTLY IF NOT EXISTS orders_unreleased_next_idx ON orders ((COALESCE(release_attempted_at, updated_at)))
    WHERE released = false AND status IN ('FILLED', 'CANCELED', 'REJECTED', 'EXPIRED') AND freeze_state = 'FROZEN';
DROP INDEX CONCURRENTLY IF EXISTS orders_unreleased_idx;

-- +goose Down
CREATE INDEX CONCURRENTLY IF NOT EXISTS orders_unreleased_idx ON orders (updated_at)
    WHERE released = false AND status IN ('FILLED', 'CANCELED', 'REJECTED', 'EXPIRED') AND freeze_state = 'FROZEN';
DROP INDEX CONCURRENTLY IF EXISTS orders_unreleased_next_idx;
ALTER TABLE orders DROP COLUMN IF EXISTS release_attempted_at;
