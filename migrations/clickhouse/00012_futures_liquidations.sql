-- futures_liquidations takes LiquidationOccurred's shape as G0's contract
-- settled it (04f9063b, review EJ ①): the side of the position closed and
-- the quantity filled, where 00011 drafted a liquidation order's fields
-- (side, quantity and filled_quantity, status). The table holds seven days
-- of the reference market's liquidations, kept nowhere else for the
-- console: it is recreated empty, and analytics-consumer fills it again
-- from market.liquidations as they come.

-- +goose NO TRANSACTION
-- +goose Up
DROP TABLE IF EXISTS futures_liquidations;
CREATE TABLE futures_liquidations
(
    symbol        LowCardinality(String),
    position_side LowCardinality(String),
    price         Decimal128(18),
    average_price Decimal128(18),
    quantity      Decimal128(18),
    value_usd     Decimal128(18),
    traded_at     DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMMDD(traded_at)
ORDER BY (symbol, traded_at, position_side, price, quantity)
TTL toDateTime(traded_at) + INTERVAL 7 DAY;

-- +goose Down
DROP TABLE IF EXISTS futures_liquidations;
CREATE TABLE futures_liquidations
(
    symbol          LowCardinality(String),
    side            LowCardinality(String),
    price           Decimal128(18),
    average_price   Decimal128(18),
    quantity        Decimal128(18),
    filled_quantity Decimal128(18),
    value_usd       Decimal128(18),
    status          LowCardinality(String),
    traded_at       DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMMDD(traded_at)
ORDER BY (symbol, traded_at, side, price, quantity)
TTL toDateTime(traded_at) + INTERVAL 7 DAY;
