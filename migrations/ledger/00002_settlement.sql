-- Settlement (plan §6.3 task 4): the engine trades the ledger has booked,
-- or parked when it refused them. A trade's journals are keyed
-- trade:<id>, trade-fee:<id> and trade-release:<id>; this table makes
-- redeliveries harmless and gives reconciliation the engine's amounts
-- (invariant 5) and trade numbers (gaps mean a missing trade).

-- +goose Up
CREATE TABLE trades (
    trade_id          uuid           PRIMARY KEY,
    symbol            text           NOT NULL,
    -- The symbol's trades counted from 1; 0 for trades from before the
    -- engine numbered them.
    trade_number      bigint         NOT NULL,
    base_asset        text           NOT NULL,
    quote_asset       text           NOT NULL,
    price             numeric(38,18) NOT NULL,
    quantity          numeric(38,18) NOT NULL,
    quote_quantity    numeric(38,18) NOT NULL,
    buyer_order_id    text           NOT NULL,
    buyer_user_id     text           NOT NULL,
    seller_order_id   text           NOT NULL,
    seller_user_id    text           NOT NULL,
    buyer_is_maker    boolean        NOT NULL,
    buyer_fee         numeric(38,18) NOT NULL,
    seller_fee        numeric(38,18) NOT NULL,
    -- The buy order's limit price; NULL for a market buy.
    buyer_limit_price numeric(38,18),
    event_id          text           NOT NULL,
    executed_at       timestamptz    NOT NULL,
    status            text           NOT NULL CHECK (status IN ('SETTLED', 'FAILED')),
    error_code        text           NOT NULL DEFAULT '',
    error             text           NOT NULL DEFAULT '',
    attempts          int            NOT NULL DEFAULT 1,
    recorded_at       timestamptz    NOT NULL DEFAULT now(),
    settled_at        timestamptz
);
-- Not unique on purpose: a numbering fault must not stop settlement;
-- reconciliation reports it (TRADES_NUMBERED).
CREATE INDEX trades_number ON trades (symbol, trade_number);
CREATE INDEX trades_recorded ON trades (recorded_at DESC);
CREATE INDEX trades_failed ON trades (recorded_at) WHERE status = 'FAILED';

-- +goose Down
DROP TABLE trades;
