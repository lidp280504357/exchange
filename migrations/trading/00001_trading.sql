-- spot-trading-service (requirements §5.6, §11.1): orders and their
-- freeze state. Amounts are NUMERIC(38,18); IDs UUIDv7 (ADR-0008).

-- +goose Up
CREATE TABLE orders (
    id               UUID          PRIMARY KEY,
    user_id          UUID          NOT NULL,
    client_order_id  TEXT          NOT NULL CHECK (client_order_id ~ '^[A-Za-z0-9_-]{1,36}$'),
    symbol           TEXT          NOT NULL,
    side             TEXT          NOT NULL CHECK (side IN ('BUY', 'SELL')),
    type             TEXT          NOT NULL CHECK (type IN ('LIMIT', 'MARKET')),
    time_in_force    TEXT          NOT NULL CHECK (time_in_force IN ('GTC', 'IOC', 'FOK', 'POST_ONLY')),
    stp              TEXT          NOT NULL CHECK (stp IN ('CANCEL_NEWEST', 'CANCEL_OLDEST', 'CANCEL_BOTH')),
    price            NUMERIC(38,18) CHECK (price > 0),
    quantity         NUMERIC(38,18) CHECK (quantity > 0),
    quote_amount     NUMERIC(38,18) CHECK (quote_amount > 0),
    status           TEXT          NOT NULL CHECK (status IN ('NEW', 'OPEN', 'PARTIALLY_FILLED', 'FILLED', 'CANCELED', 'REJECTED', 'EXPIRED')),
    reject_reason    TEXT,
    filled_quantity  NUMERIC(38,18) NOT NULL DEFAULT 0 CHECK (filled_quantity >= 0),
    filled_quote     NUMERIC(38,18) NOT NULL DEFAULT 0 CHECK (filled_quote >= 0),
    frozen_asset     TEXT          NOT NULL,
    frozen_amount    NUMERIC(38,18) NOT NULL CHECK (frozen_amount >= 0),
    freeze_state     TEXT          NOT NULL CHECK (freeze_state IN ('PENDING', 'FROZEN', 'NONE')),
    maker_fee_rate   NUMERIC(38,18) NOT NULL,
    taker_fee_rate   NUMERIC(38,18) NOT NULL,
    base_decimals    INT           NOT NULL,
    quote_decimals   INT           NOT NULL,
    protection_price NUMERIC(38,18),
    cancel_requested BOOLEAN       NOT NULL DEFAULT false,
    sequence         BIGINT        NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ   NOT NULL,
    updated_at       TIMESTAMPTZ   NOT NULL,
    UNIQUE (user_id, client_order_id),
    -- Limit orders have a price and a quantity; market buys a quote amount,
    -- market sells a quantity.
    CHECK ((type = 'LIMIT' AND price IS NOT NULL AND quantity IS NOT NULL AND quote_amount IS NULL)
        OR (type = 'MARKET' AND price IS NULL AND ((side = 'BUY' AND quote_amount IS NOT NULL AND quantity IS NULL)
                                              OR (side = 'SELL' AND quantity IS NOT NULL AND quote_amount IS NULL))))
);
CREATE INDEX orders_user_idx ON orders (user_id, id DESC);
CREATE INDEX orders_active_idx ON orders (user_id, symbol) WHERE status IN ('NEW', 'OPEN', 'PARTIALLY_FILLED');
-- Orders whose freeze was not recorded yet, for the recovery loop.
CREATE INDEX orders_pending_freeze_idx ON orders (created_at) WHERE freeze_state = 'PENDING';

-- +goose Down
DROP TABLE orders;
