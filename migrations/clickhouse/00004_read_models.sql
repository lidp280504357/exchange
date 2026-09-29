-- Read models of trading and the wallet (implementation plan §6.3 task 12,
-- requirements §9), projected by analytics-consumer from order.events,
-- trade.events and wallet.*.events; candles_1m is recomputed from trades
-- for the minutes each batch touches. All tables are ReplacingMergeTree:
-- redelivered events collapse, so query with FINAL. Creation times of
-- orders, deposits and withdrawals are in their UUIDv7 IDs
-- (UUIDv7ToDateTime).

-- +goose NO TRANSACTION
-- +goose Up

-- One row per trade (trade.TradeExecuted). Fees: the buyer pays in base,
-- the seller in quote.
CREATE TABLE IF NOT EXISTS trades
(
    trade_id        UUID,
    symbol          LowCardinality(String),
    base_asset      LowCardinality(String),
    quote_asset     LowCardinality(String),
    trade_number    UInt64,
    sequence        Int64,
    price           Decimal128(18),
    quantity        Decimal128(18),
    quote_quantity  Decimal128(18),
    taker_side      LowCardinality(String),
    buyer_order_id  UUID,
    buyer_user_id   UUID,
    seller_order_id UUID,
    seller_user_id  UUID,
    buyer_is_maker  Bool,
    buyer_fee       Decimal128(18),
    seller_fee      Decimal128(18),
    executed_at     DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(executed_at)
ORDER BY (symbol, executed_at, trade_id);

-- Orders as accepted (order.OrderAccepted). An order refused before it was
-- accepted appears only in order_updates.
CREATE TABLE IF NOT EXISTS orders
(
    order_id        UUID,
    client_order_id String,
    user_id         UUID,
    symbol          LowCardinality(String),
    side            LowCardinality(String),
    type            LowCardinality(String),
    time_in_force   LowCardinality(String),
    price           Nullable(Decimal128(18)),
    quantity        Nullable(Decimal128(18)),
    quote_amount    Nullable(Decimal128(18)),
    frozen_asset    LowCardinality(String),
    frozen_amount   Decimal128(18),
    accepted_at     DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(accepted_at)
ORDER BY (symbol, accepted_at, order_id);

-- Every change of an order: NEW (accepted), OPEN, PARTIALLY_FILLED,
-- FILLED, CANCELED or REJECTED, with the fills so far; the engine's
-- sequence orders them (0 for the trading service's events).
CREATE TABLE IF NOT EXISTS order_updates
(
    order_id        UUID,
    user_id         UUID,
    symbol          LowCardinality(String),
    sequence        Int64,
    status          LowCardinality(String),
    filled_quantity Decimal128(18),
    filled_quote    Decimal128(18),
    trade_id        String,
    reason          LowCardinality(String),
    event_id        UUID,
    occurred_at     DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(occurred_at)
ORDER BY (symbol, order_id, sequence, event_id);

-- The latest state of every order, with what it was accepted as.
CREATE VIEW IF NOT EXISTS orders_current AS
SELECT
    order_id,
    u.user_id AS user_id,
    u.symbol AS symbol,
    o.client_order_id AS client_order_id,
    o.side AS side,
    o.type AS type,
    o.time_in_force AS time_in_force,
    o.price AS price,
    o.quantity AS quantity,
    o.quote_amount AS quote_amount,
    u.last_status AS status,
    u.last_filled_quantity AS filled_quantity,
    u.last_filled_quote AS filled_quote,
    u.last_reason AS reason,
    u.first_at AS created_at,
    u.last_at AS updated_at
FROM
(
    SELECT
        order_id,
        any(user_id) AS user_id,
        any(symbol) AS symbol,
        argMax(status, (sequence, occurred_at)) AS last_status,
        argMax(filled_quantity, (sequence, occurred_at)) AS last_filled_quantity,
        argMax(filled_quote, (sequence, occurred_at)) AS last_filled_quote,
        argMax(reason, (sequence, occurred_at)) AS last_reason,
        min(occurred_at) AS first_at,
        max(occurred_at) AS last_at
    FROM order_updates FINAL
    GROUP BY order_id
) AS u
LEFT JOIN (SELECT * FROM orders FINAL) AS o USING (order_id);

-- Deposits (wallet.deposit.events) in their latest state. version orders
-- the events: the time in milliseconds, then the status's progress.
CREATE TABLE IF NOT EXISTS wallet_deposits
(
    deposit_id             UUID,
    user_id                String,
    asset                  LowCardinality(String),
    network                LowCardinality(String),
    kind                   LowCardinality(String),
    address                String,
    tx_hash                String,
    log_index              Int64,
    block_number           UInt64,
    amount                 Decimal128(18),
    status                 LowCardinality(String),
    unclaimed              Bool,
    reason                 LowCardinality(String),
    confirmations          UInt32,
    required_confirmations UInt32,
    journal_id             String,
    updated_at             DateTime64(3, 'UTC'),
    version                UInt64
)
ENGINE = ReplacingMergeTree(version)
ORDER BY deposit_id;

-- Withdrawals (wallet.withdrawal.events) in their latest state.
CREATE TABLE IF NOT EXISTS wallet_withdrawals
(
    withdrawal_id          UUID,
    user_id                String,
    asset                  LowCardinality(String),
    network                LowCardinality(String),
    address                String,
    amount                 Decimal128(18),
    fee                    Decimal128(18),
    status                 LowCardinality(String),
    internal               Bool,
    tx_hash                String,
    confirmations          UInt32,
    required_confirmations UInt32,
    risk_reasons           Array(LowCardinality(String)),
    reject_reason          String,
    updated_at             DateTime64(3, 'UTC'),
    version                UInt64
)
ENGINE = ReplacingMergeTree(version)
ORDER BY withdrawal_id;

-- One-minute candles from trades, rewritten whenever a batch brings trades
-- of that minute (the newest computation wins).
CREATE TABLE IF NOT EXISTS candles_1m
(
    symbol       LowCardinality(String),
    open_time    DateTime('UTC'),
    open         Decimal128(18),
    high         Decimal128(18),
    low          Decimal128(18),
    close        Decimal128(18),
    volume       Decimal128(18),
    quote_volume Decimal128(18),
    trades       UInt32,
    updated_at   DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY toYYYYMM(open_time)
ORDER BY (symbol, open_time);

-- Candles of any interval (seconds, epoch-aligned; 86400 starts at 00:00
-- UTC), for example:
--   SELECT * FROM candles(symbol = 'BTC-USDT', seconds = 3600) ORDER BY open_time DESC LIMIT 24
CREATE VIEW IF NOT EXISTS candles AS
SELECT symbol, bucket AS open_time, o AS open, h AS high, l AS low, c AS close, v AS volume, qv AS quote_volume, n AS trades
FROM
(
    SELECT
        symbol,
        toStartOfInterval(open_time, toIntervalSecond({seconds:UInt32})) AS bucket,
        argMin(open, open_time) AS o,
        max(high) AS h,
        min(low) AS l,
        argMax(close, open_time) AS c,
        sum(volume) AS v,
        sum(quote_volume) AS qv,
        sum(trades) AS n
    FROM candles_1m FINAL
    WHERE symbol = {symbol:String}
    GROUP BY symbol, bucket
);

-- Backfills of the read models from events, run once each by
-- analytics-consumer.
CREATE TABLE IF NOT EXISTS read_model_backfills
(
    name    String,
    done_at DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree
ORDER BY name;

-- +goose Down
DROP TABLE IF EXISTS read_model_backfills;
DROP VIEW IF EXISTS candles;
DROP TABLE IF EXISTS candles_1m;
DROP TABLE IF EXISTS wallet_withdrawals;
DROP TABLE IF EXISTS wallet_deposits;
DROP VIEW IF EXISTS orders_current;
DROP TABLE IF EXISTS order_updates;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS trades;
