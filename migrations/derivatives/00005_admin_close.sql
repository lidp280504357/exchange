-- The admin console closes a position at the market (design 2026-10-02
-- §4.1, force close) with orders of kind ADMIN.

-- +goose Up
ALTER TABLE orders DROP CONSTRAINT orders_kind_check;
ALTER TABLE orders ADD CONSTRAINT orders_kind_check CHECK (kind IN ('USER', 'LIQUIDATION', 'ADL', 'TAKE_PROFIT', 'STOP_LOSS', 'ADMIN'));

-- +goose Down
ALTER TABLE orders DROP CONSTRAINT orders_kind_check;
ALTER TABLE orders ADD CONSTRAINT orders_kind_check CHECK (kind IN ('USER', 'LIQUIDATION', 'ADL', 'TAKE_PROFIT', 'STOP_LOSS'));
