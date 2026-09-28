-- market-data-service: platform candles and recent public trades built
-- from trade.events (requirements §5.11, §11.8; plan §6.3 task 5). Depth
-- is kept in memory from market.depth, tickers are computed from 1m
-- candles; nothing here is a source of truth: it can be rebuilt from
-- trade.events.

-- +goose Up
-- How far each symbol's trades have been applied: trades at or below the
-- sequence are skipped, so redeliveries do not count twice.
CREATE TABLE symbols (
    symbol        text           PRIMARY KEY,
    last_sequence bigint         NOT NULL,
    last_price    numeric(38,18) NOT NULL,
    last_trade_at timestamptz    NOT NULL,
    updated_at    timestamptz    NOT NULL DEFAULT now()
);

-- Candles of intervals that had trades; gaps are filled when read.
CREATE TABLE candles (
    symbol       text           NOT NULL,
    interval     text           NOT NULL CHECK (interval IN ('1m', '3m', '5m', '15m', '30m', '1h', '2h', '4h', '6h', '12h',
                                    '1d', '1w', '1M')),
    open_time    timestamptz    NOT NULL,
    open         numeric(38,18) NOT NULL,
    high         numeric(38,18) NOT NULL,
    low          numeric(38,18) NOT NULL,
    close        numeric(38,18) NOT NULL,
    volume       numeric(38,18) NOT NULL,
    quote_volume numeric(38,18) NOT NULL,
    trade_count  bigint         NOT NULL,
    updated_at   timestamptz    NOT NULL DEFAULT now(),
    PRIMARY KEY (symbol, interval, open_time)
);

-- Recent public trades (the trades list after a restart); purged after
-- seven days, ClickHouse keeps the history.
CREATE TABLE trades (
    symbol         text           NOT NULL,
    sequence       bigint         NOT NULL,
    trade_id       uuid           NOT NULL,
    trade_number   bigint         NOT NULL,
    price          numeric(38,18) NOT NULL,
    quantity       numeric(38,18) NOT NULL,
    quote_quantity numeric(38,18) NOT NULL,
    taker_side     text           NOT NULL CHECK (taker_side IN ('BUY', 'SELL')),
    executed_at    timestamptz    NOT NULL,
    PRIMARY KEY (symbol, sequence)
);
CREATE INDEX trades_executed ON trades (executed_at);

-- +goose Down
DROP TABLE trades;
DROP TABLE candles;
DROP TABLE symbols;
