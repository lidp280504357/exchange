-- Take-profit and stop-loss (requirements §5.8; plan §7.3 task 8):
-- conditional orders that close a position once the mark price (or the
-- last trade price) reaches their trigger, as a market (protected IOC) or
-- limit order.

-- +goose Up
CREATE TABLE conditional_orders (
    conditional_id uuid           PRIMARY KEY,
    user_id        uuid           NOT NULL,
    symbol         text           NOT NULL,
    position_side  text           NOT NULL CHECK (position_side IN ('BOTH', 'LONG', 'SHORT')),
    -- The side of the order it places: the opposite of the position.
    side           text           NOT NULL CHECK (side IN ('BUY', 'SELL')),
    kind           text           NOT NULL CHECK (kind IN ('TAKE_PROFIT', 'STOP_LOSS')),
    trigger_price  numeric(38,18) NOT NULL CHECK (trigger_price > 0),
    trigger_by     text           NOT NULL CHECK (trigger_by IN ('MARK', 'LAST')),
    order_type     text           NOT NULL CHECK (order_type IN ('MARKET', 'LIMIT')),
    -- The limit price of a LIMIT order.
    price          numeric(38,18),
    -- NULL closes the whole position when it triggers.
    quantity       numeric(38,18),
    status         text           NOT NULL CHECK (status IN ('ACTIVE', 'TRIGGERED', 'CANCELED', 'FAILED')),
    -- Why it was canceled or failed (an appendix C code, or NO_POSITION).
    reason         text           NOT NULL DEFAULT '',
    order_id       uuid,
    created_at     timestamptz    NOT NULL,
    updated_at     timestamptz    NOT NULL
);
CREATE INDEX conditional_orders_active ON conditional_orders (symbol) WHERE status = 'ACTIVE';
CREATE INDEX conditional_orders_user ON conditional_orders (user_id, conditional_id DESC);

-- +goose Down
DROP TABLE conditional_orders;
