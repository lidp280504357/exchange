-- orders_current shows each order's created_key, the time in its UUIDv7 ID
-- that orders_state is keyed and partitioned by (review BK): a read of a
-- time range that names it too reads only those parts, not every order.
-- An order's first update comes after its ID was made, minutes at most
-- (first_at >= created_key), so a range on created_at can start a margin
-- earlier on created_key.

-- +goose NO TRANSACTION
-- +goose Up
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
    CAST(last_at AS DateTime64(3, 'UTC')) AS updated_at,
    created_key
FROM orders_state FINAL
WHERE last.1 > toInt64(-9223372036854775808);

-- +goose Down
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
