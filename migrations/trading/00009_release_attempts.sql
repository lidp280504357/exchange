-- A finished order whose release did not complete (a margin order's
-- repayment waiting for its trades' settlement, the ledger unreachable)
-- goes to the back of the recovery's queue once tried (B164): a pass takes
-- the 100 longest untried, since they finished or since their last try,
-- so ones that keep failing no longer starve the rest. NULL until a try
-- fails. orders is large: the indexes are built and dropped CONCURRENTLY,
-- outside a transaction, and each step can run again. A concurrent build
-- that fails leaves an INVALID index of its name, which IF NOT EXISTS
-- would keep: it is dropped first, so a run after a failure builds it
-- again (B166).

-- +goose NO TRANSACTION
-- +goose Up
ALTER TABLE orders ADD COLUMN IF NOT EXISTS release_attempted_at timestamptz;
DROP INDEX CONCURRENTLY IF EXISTS orders_unreleased_next_idx;
CREATE INDEX CONCURRENTLY orders_unreleased_next_idx ON orders ((COALESCE(release_attempted_at, updated_at)))
    WHERE released = false AND status IN ('FILLED', 'CANCELED', 'REJECTED', 'EXPIRED') AND freeze_state = 'FROZEN';
DROP INDEX CONCURRENTLY IF EXISTS orders_unreleased_idx;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS orders_unreleased_idx;
CREATE INDEX CONCURRENTLY orders_unreleased_idx ON orders (updated_at)
    WHERE released = false AND status IN ('FILLED', 'CANCELED', 'REJECTED', 'EXPIRED') AND freeze_state = 'FROZEN';
DROP INDEX CONCURRENTLY IF EXISTS orders_unreleased_next_idx;
ALTER TABLE orders DROP COLUMN IF EXISTS release_attempted_at;
