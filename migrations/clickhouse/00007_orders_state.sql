-- The latest state of every order kept as orders come in (review B58):
-- orders_current was a view that recomputed it from every order update
-- and order on each read (1.9 million rows, 2-14 s for the admin console's
-- orders list on the test server). Now two materialized views fold each
-- inserted order and order update into orders_state, an AggregatingMergeTree
-- with one row per order once merged: the static fields from the order,
-- the latest status, fills and reason by (sequence, occurred_at), the first
-- and the last update's times. orders_current keeps its name and columns,
-- read from orders_state with FINAL. Every fold is a max, min or any, so a
-- row folded twice (a redelivered event, the backfill below racing the
-- views) changes nothing. The key starts with the order ID's UUIDv7 time,
-- so new orders land in new key ranges and FINAL merges little.

-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE IF NOT EXISTS orders_state
(
    created_key     DateTime64(3, 'UTC'),
    order_id        UUID,
    user_id         SimpleAggregateFunction(any, UUID),
    symbol          SimpleAggregateFunction(any, LowCardinality(String)),
    client_order_id SimpleAggregateFunction(max, String),
    side            SimpleAggregateFunction(max, LowCardinality(String)),
    type            SimpleAggregateFunction(max, LowCardinality(String)),
    time_in_force   SimpleAggregateFunction(max, LowCardinality(String)),
    price           SimpleAggregateFunction(any, Nullable(Decimal128(18))),
    quantity        SimpleAggregateFunction(any, Nullable(Decimal128(18))),
    quote_amount    SimpleAggregateFunction(any, Nullable(Decimal128(18))),
    -- (sequence, occurred_at, status, filled_quantity, filled_quote, reason)
    -- of the latest update; an order's own row holds the least such tuple.
    -- Two updates of one order never share a sequence (the engine numbers
    -- each event; the trading service's 0 is either its OrderAccepted or
    -- its OrderRejected), so the status never decides; if two did, the
    -- greater string would win, NEW over CANCELED (review BK).
    last            SimpleAggregateFunction(max, Tuple(Int64, DateTime64(3, 'UTC'), String, Decimal128(18), Decimal128(18), String)),
    -- The first and the last update; an order's own row holds the far
    -- future and the epoch.
    first_at        SimpleAggregateFunction(min, DateTime64(3, 'UTC')),
    last_at         SimpleAggregateFunction(max, DateTime64(3, 'UTC'))
)
ENGINE = AggregatingMergeTree
PARTITION BY toYYYYMM(created_key)
ORDER BY (created_key, order_id);

CREATE MATERIALIZED VIEW IF NOT EXISTS orders_state_orders TO orders_state AS
SELECT
    UUIDv7ToDateTime(order_id, 'UTC') AS created_key,
    order_id, user_id, symbol, client_order_id, side, type, time_in_force, price, quantity, quote_amount,
    tuple(toInt64(-9223372036854775808), toDateTime64(0, 3, 'UTC'), '', toDecimal128(0, 18), toDecimal128(0, 18), '') AS last,
    toDateTime64('2299-12-31 00:00:00', 3, 'UTC') AS first_at,
    toDateTime64(0, 3, 'UTC') AS last_at
FROM orders;

CREATE MATERIALIZED VIEW IF NOT EXISTS orders_state_updates TO orders_state AS
SELECT
    UUIDv7ToDateTime(order_id, 'UTC') AS created_key,
    order_id, user_id, symbol,
    tuple(sequence, occurred_at, toString(status), filled_quantity, filled_quote, toString(reason)) AS last,
    occurred_at AS first_at,
    occurred_at AS last_at
FROM order_updates;

-- What came before the views.
INSERT INTO orders_state (created_key, order_id, user_id, symbol, client_order_id, side, type, time_in_force, price, quantity, quote_amount,
                          last, first_at, last_at)
SELECT
    UUIDv7ToDateTime(order_id, 'UTC'),
    order_id, user_id, symbol, client_order_id, side, type, time_in_force, price, quantity, quote_amount,
    tuple(toInt64(-9223372036854775808), toDateTime64(0, 3, 'UTC'), '', toDecimal128(0, 18), toDecimal128(0, 18), ''),
    toDateTime64('2299-12-31 00:00:00', 3, 'UTC'),
    toDateTime64(0, 3, 'UTC')
FROM orders;

INSERT INTO orders_state (created_key, order_id, user_id, symbol, last, first_at, last_at)
SELECT
    UUIDv7ToDateTime(order_id, 'UTC'),
    order_id, any(user_id), any(symbol),
    max(tuple(sequence, occurred_at, toString(status), filled_quantity, filled_quote, toString(reason))),
    min(occurred_at),
    max(occurred_at)
FROM order_updates
GROUP BY order_id;

OPTIMIZE TABLE orders_state FINAL;

-- The latest state of every order, with what it was accepted as (only
-- orders with an update, as before), in the old view's column types.
CREATE OR REPLACE VIEW orders_current AS
SELECT
    order_id,
    CAST(user_id AS UUID) AS user_id,
    CAST(symbol AS String) AS symbol,
    CAST(client_order_id AS String) AS client_order_id,
    CAST(side AS LowCardinality(String)) AS side,
    CAST(type AS LowCardinality(String)) AS type,
    CAST(time_in_force AS LowCardinality(String)) AS time_in_force,
    CAST(price AS Nullable(Decimal128(18))) AS price,
    CAST(quantity AS Nullable(Decimal128(18))) AS quantity,
    CAST(quote_amount AS Nullable(Decimal128(18))) AS quote_amount,
    last.3 AS status,
    last.4 AS filled_quantity,
    last.5 AS filled_quote,
    last.6 AS reason,
    CAST(first_at AS DateTime64(3, 'UTC')) AS created_at,
    CAST(last_at AS DateTime64(3, 'UTC')) AS updated_at
FROM orders_state FINAL
WHERE last.1 > toInt64(-9223372036854775808);

-- +goose Down
CREATE OR REPLACE VIEW orders_current AS
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
DROP VIEW IF EXISTS orders_state_updates;
DROP VIEW IF EXISTS orders_state_orders;
DROP TABLE IF EXISTS orders_state;
