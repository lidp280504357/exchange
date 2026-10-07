-- A market buy may be sized by its quantity too (B157, as Binance takes
-- one): one of quote_amount and quantity, and one by quantity has the
-- protection price it is frozen and matched at. The constraint is added
-- NOT VALID and validated in its own statement, so the orders stay
-- writable while the existing rows are checked.

-- +goose NO TRANSACTION
-- +goose Up
ALTER TABLE orders DROP CONSTRAINT orders_check;
ALTER TABLE orders ADD CONSTRAINT orders_shape CHECK (
    (type = 'LIMIT' AND price IS NOT NULL AND quantity IS NOT NULL AND quote_amount IS NULL)
    OR (type = 'MARKET' AND price IS NULL AND (
        (side = 'BUY' AND quote_amount IS NOT NULL AND quantity IS NULL)
        OR (side = 'BUY' AND quantity IS NOT NULL AND quote_amount IS NULL AND protection_price IS NOT NULL)
        OR (side = 'SELL' AND quantity IS NOT NULL AND quote_amount IS NULL)))) NOT VALID;
ALTER TABLE orders VALIDATE CONSTRAINT orders_shape;

-- +goose Down
ALTER TABLE orders DROP CONSTRAINT orders_shape;
ALTER TABLE orders ADD CONSTRAINT orders_check CHECK (
    (type = 'LIMIT' AND price IS NOT NULL AND quantity IS NOT NULL AND quote_amount IS NULL)
    OR (type = 'MARKET' AND price IS NULL AND (
        (side = 'BUY' AND quote_amount IS NOT NULL AND quantity IS NULL)
        OR (side = 'SELL' AND quantity IS NOT NULL AND quote_amount IS NULL)))) NOT VALID;
