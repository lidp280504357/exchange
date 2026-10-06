-- A liquidation whose insurance fund lacks an asset to cover what the
-- account could not repay waits as SHORTFALL (review CY (a)): the debt
-- stays owed, an operator funds the asset, and the next attempt goes on.
-- It is still the account's liquidation under way.

-- +goose Up
ALTER TABLE liquidations DROP CONSTRAINT liquidations_status_check;
ALTER TABLE liquidations ADD CONSTRAINT liquidations_status_check CHECK (status IN ('STARTED', 'SHORTFALL', 'COMPLETED'));
DROP INDEX liquidations_running;
CREATE UNIQUE INDEX liquidations_running ON liquidations (user_id, account_type, symbol) WHERE status <> 'COMPLETED';

-- +goose Down
DROP INDEX liquidations_running;
UPDATE liquidations SET status = 'STARTED' WHERE status = 'SHORTFALL';
CREATE UNIQUE INDEX liquidations_running ON liquidations (user_id, account_type, symbol) WHERE status = 'STARTED';
ALTER TABLE liquidations DROP CONSTRAINT liquidations_status_check;
ALTER TABLE liquidations ADD CONSTRAINT liquidations_status_check CHECK (status IN ('STARTED', 'COMPLETED'));
