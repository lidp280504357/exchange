-- External reference prices (requirements §5.11, §11.9; plan §6.3 task 7):
-- the 1m candles of each configured source, kept apart from the
-- platform's own candles. Binance public data may only be used in test
-- environments until a data license is in place.

-- +goose Up
CREATE TABLE reference_candles (
    source       text           NOT NULL,
    symbol       text           NOT NULL,
    open_time    timestamptz    NOT NULL,
    open         numeric(38,18) NOT NULL,
    high         numeric(38,18) NOT NULL,
    low          numeric(38,18) NOT NULL,
    close        numeric(38,18) NOT NULL,
    volume       numeric(38,18) NOT NULL,
    quote_volume numeric(38,18) NOT NULL,
    trade_count  bigint         NOT NULL,
    updated_at   timestamptz    NOT NULL DEFAULT now(),
    PRIMARY KEY (source, symbol, open_time)
);

-- +goose Down
DROP TABLE reference_candles;
