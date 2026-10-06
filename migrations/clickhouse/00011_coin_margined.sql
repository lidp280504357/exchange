-- Coin-margined perpetuals and Binance's liquidations (design 2026-10-06
-- §2.5, §3.3, batch G0). settle_asset: the asset a contract's amounts are
-- in (USDT for a linear contract, the base asset for an inverse one such
-- as BTC-USD-PERP, whose quantities are whole contracts); projected from
-- the events' settle_asset from batch G1. Rows written before carry ''
-- (read as USDT on a contract; nothing on a spot trade).
-- futures_liquidations: the reference market's forced orders
-- (market.liquidations, LiquidationOccurred, from batch G3b), kept 7 days.

-- +goose NO TRANSACTION
-- +goose Up
ALTER TABLE derivatives_positions ADD COLUMN IF NOT EXISTS settle_asset LowCardinality(String) DEFAULT '' AFTER symbol;
ALTER TABLE derivatives_fills ADD COLUMN IF NOT EXISTS settle_asset LowCardinality(String) DEFAULT '' AFTER symbol;
ALTER TABLE derivatives_funding ADD COLUMN IF NOT EXISTS settle_asset LowCardinality(String) DEFAULT '' AFTER symbol;
ALTER TABLE trades ADD COLUMN IF NOT EXISTS settle_asset LowCardinality(String) DEFAULT '' AFTER quote_asset;

CREATE TABLE IF NOT EXISTS futures_liquidations
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

-- +goose Down
DROP TABLE IF EXISTS futures_liquidations;
ALTER TABLE trades DROP COLUMN IF EXISTS settle_asset;
ALTER TABLE derivatives_funding DROP COLUMN IF EXISTS settle_asset;
ALTER TABLE derivatives_fills DROP COLUMN IF EXISTS settle_asset;
ALTER TABLE derivatives_positions DROP COLUMN IF EXISTS settle_asset;
