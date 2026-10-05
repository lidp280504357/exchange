-- Margin liquidations (design 2026-10-06 §4.5, E0 §3.4, batch E3):
-- margin-service closes a margin account with market orders placed through
-- the trading service's internal endpoint; such an order names its
-- liquidation and is funded without a reservation.

-- +goose Up
ALTER TABLE orders
    ADD COLUMN liquidation_id UUID,
    ADD CONSTRAINT orders_liquidation_margin_market CHECK (liquidation_id IS NULL OR (account_type <> 'SPOT' AND type = 'MARKET'));

-- +goose Down
ALTER TABLE orders
    DROP CONSTRAINT orders_liquidation_margin_market,
    DROP COLUMN liquidation_id;
