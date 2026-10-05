-- Margin trading (design 2026-10-06, batch E2): an order trades from the
-- user's SPOT account or one of their margin accounts (the isolated one of
-- its pair), and a margin order may borrow before its freeze (AUTO_BORROW)
-- or repay with what its fills bring (AUTO_REPAY). Orders placed before
-- this migration are SPOT orders without a side effect.

-- +goose Up
ALTER TABLE orders
    ADD COLUMN account_type TEXT NOT NULL DEFAULT 'SPOT'
        CHECK (account_type IN ('SPOT', 'MARGIN_CROSS', 'MARGIN_ISOLATED')),
    ADD COLUMN side_effect  TEXT NOT NULL DEFAULT 'NONE'
        CHECK (side_effect IN ('NONE', 'AUTO_BORROW', 'AUTO_REPAY')),
    ADD CONSTRAINT orders_side_effect_margin_only CHECK (account_type <> 'SPOT' OR side_effect = 'NONE');

-- +goose Down
ALTER TABLE orders
    DROP CONSTRAINT orders_side_effect_margin_only,
    DROP COLUMN side_effect,
    DROP COLUMN account_type;
