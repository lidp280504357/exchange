-- The matching engine arrives (plan §6.3 task 3): orders carry their
-- pair's steps and assets to it, record why the engine canceled them and
-- whether their unused funds were released; fills keep each side of a
-- trade. Orders placed before this migration have no steps (the engine
-- falls back to the asset's smallest unit).

-- +goose Up
ALTER TABLE orders
    ADD COLUMN tick_size     NUMERIC(38,18),
    ADD COLUMN lot_size      NUMERIC(38,18),
    ADD COLUMN base_asset    TEXT,
    ADD COLUMN quote_asset   TEXT,
    ADD COLUMN cancel_reason TEXT,
    ADD COLUMN released      BOOLEAN NOT NULL DEFAULT false;
-- Finished orders whose unused funds are not released yet, for recovery.
CREATE INDEX orders_unreleased_idx ON orders (updated_at)
    WHERE released = false AND status IN ('FILLED', 'CANCELED', 'REJECTED', 'EXPIRED') AND freeze_state = 'FROZEN';

CREATE TABLE fills (
    trade_id       UUID           NOT NULL,
    order_id       UUID           NOT NULL,
    user_id        UUID           NOT NULL,
    symbol         TEXT           NOT NULL,
    side           TEXT           NOT NULL CHECK (side IN ('BUY', 'SELL')),
    maker          BOOLEAN        NOT NULL,
    price          NUMERIC(38,18) NOT NULL CHECK (price > 0),
    quantity       NUMERIC(38,18) NOT NULL CHECK (quantity > 0),
    quote_quantity NUMERIC(38,18) NOT NULL CHECK (quote_quantity > 0),
    fee_asset      TEXT           NOT NULL,
    fee            NUMERIC(38,18) NOT NULL CHECK (fee >= 0),
    sequence       BIGINT         NOT NULL,
    executed_at    TIMESTAMPTZ    NOT NULL,
    PRIMARY KEY (trade_id, order_id)
);
CREATE INDEX fills_order_idx ON fills (order_id, sequence);
CREATE INDEX fills_user_idx ON fills (user_id, executed_at DESC, trade_id);

-- +goose Down
DROP TABLE fills;
DROP INDEX orders_unreleased_idx;
ALTER TABLE orders
    DROP COLUMN tick_size,
    DROP COLUMN lot_size,
    DROP COLUMN base_asset,
    DROP COLUMN quote_asset,
    DROP COLUMN cancel_reason,
    DROP COLUMN released;
