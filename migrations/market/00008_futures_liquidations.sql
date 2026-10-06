-- The reference market's recent liquidation orders on the platform's
-- contracts (design 2026-10-06 §3.3), as the contracts' data panel lists
-- them (GET /v1/market/{symbol}/liquidations): Binance's liquidation
-- stream, the latest of a contract within a second, kept a day.
-- ClickHouse futures_liquidations keeps seven days.

-- +goose Up
CREATE TABLE futures_liquidations (
    symbol        text           NOT NULL,
    traded_at     timestamptz    NOT NULL,
    -- The side of the position closed: LONG when the order sold.
    position_side text           NOT NULL CHECK (position_side IN ('LONG', 'SHORT')),
    price         numeric(38,18) NOT NULL,
    average_price numeric(38,18) NOT NULL,
    -- Filled: in the base asset, or contracts of an inverse contract.
    quantity      numeric(38,18) NOT NULL CHECK (quantity > 0),
    value_usd     numeric(38,18) NOT NULL,
    PRIMARY KEY (symbol, traded_at, position_side)
);
CREATE INDEX futures_liquidations_traded ON futures_liquidations (traded_at);

-- +goose Down
DROP TABLE futures_liquidations;
