-- A market buy may be sized by its quantity too (B157, as Binance takes
-- one): one of quote_amount and quantity, and one by quantity has the
-- protection price it is frozen and matched at. The new constraint goes on
-- NOT VALID before the old one comes off (no moment without one) and is
-- validated in a statement of its own, the orders writable meanwhile; each
-- step can run again (B162), the migration having no transaction.

-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'orders_shape' AND conrelid = 'orders'::regclass) THEN
        ALTER TABLE orders ADD CONSTRAINT orders_shape CHECK (
            (type = 'LIMIT' AND price IS NOT NULL AND quantity IS NOT NULL AND quote_amount IS NULL)
            OR (type = 'MARKET' AND price IS NULL AND (
                (side = 'BUY' AND quote_amount IS NOT NULL AND quantity IS NULL)
                OR (side = 'BUY' AND quantity IS NOT NULL AND quote_amount IS NULL AND protection_price IS NOT NULL)
                OR (side = 'SELL' AND quantity IS NOT NULL AND quote_amount IS NULL)))) NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_check;
ALTER TABLE orders VALIDATE CONSTRAINT orders_shape;

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'orders_check' AND conrelid = 'orders'::regclass) THEN
        ALTER TABLE orders ADD CONSTRAINT orders_check CHECK (
            (type = 'LIMIT' AND price IS NOT NULL AND quantity IS NOT NULL AND quote_amount IS NULL)
            OR (type = 'MARKET' AND price IS NULL AND (
                (side = 'BUY' AND quote_amount IS NOT NULL AND quantity IS NULL)
                OR (side = 'SELL' AND quantity IS NOT NULL AND quote_amount IS NULL)))) NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_shape;
