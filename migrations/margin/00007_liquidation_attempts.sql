-- A liquidation's orders numbered per pair and side (review DD C19 ①): the
-- next part of a trade, or another try after an order that was refused or
-- executed short, is a new attempt (spot-trading-service's attempt, B92).
-- What an order executed is read from the trading service once it
-- executes no more (DONE), never inferred from the account's balances;
-- what stays unsold is sold again until nothing sellable is left.

-- +goose Up
ALTER TABLE liquidation_orders
    ADD COLUMN attempt         int            NOT NULL DEFAULT 1 CHECK (attempt BETWEEN 1 AND 1000),
    ADD COLUMN order_status    text           NOT NULL DEFAULT '',
    ADD COLUMN filled_quantity numeric(38,18) NOT NULL DEFAULT 0,
    ADD COLUMN filled_quote    numeric(38,18) NOT NULL DEFAULT 0,
    ADD COLUMN updated_at      timestamptz;
UPDATE liquidation_orders SET updated_at = created_at;
ALTER TABLE liquidation_orders ALTER COLUMN updated_at SET NOT NULL;
ALTER TABLE liquidation_orders DROP CONSTRAINT liquidation_orders_pkey;
ALTER TABLE liquidation_orders ADD PRIMARY KEY (liquidation_id, symbol, side, attempt);
ALTER TABLE liquidation_orders DROP CONSTRAINT liquidation_orders_status_check;
ALTER TABLE liquidation_orders ADD CONSTRAINT liquidation_orders_status_check
    CHECK (status IN ('PLANNED', 'SENT', 'DONE', 'REFUSED'));

-- +goose Down
DELETE FROM liquidation_orders WHERE attempt > 1;
UPDATE liquidation_orders SET status = 'SENT' WHERE status = 'DONE';
ALTER TABLE liquidation_orders DROP CONSTRAINT liquidation_orders_status_check;
ALTER TABLE liquidation_orders ADD CONSTRAINT liquidation_orders_status_check CHECK (status IN ('PLANNED', 'SENT', 'REFUSED'));
ALTER TABLE liquidation_orders DROP CONSTRAINT liquidation_orders_pkey;
ALTER TABLE liquidation_orders ADD PRIMARY KEY (liquidation_id, symbol, side);
ALTER TABLE liquidation_orders DROP COLUMN updated_at, DROP COLUMN filled_quote, DROP COLUMN filled_quantity,
    DROP COLUMN order_status, DROP COLUMN attempt;
