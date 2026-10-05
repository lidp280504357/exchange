-- Orders on margin accounts (margin design 2026-10-06 §5.1, batch E2):
-- each side of a trade settles on the account its order traded from —
-- SPOT, the cross margin account or the isolated one of the trade's pair
-- (MARGIN_TRADE_SETTLE) — and an order with side_effect AUTO_REPAY repays
-- that account's debt of what it received in the same transaction. The
-- trade records keep both, so a parked trade settles the same on retry.

-- +goose Up
ALTER TABLE trades
    -- SPOT, MARGIN_CROSS or MARGIN_ISOLATED; '' for HOUSE's side and the
    -- trades from before (SPOT).
    ADD COLUMN buyer_account_type  text NOT NULL DEFAULT '',
    ADD COLUMN seller_account_type text NOT NULL DEFAULT '',
    ADD COLUMN buyer_auto_repay    boolean NOT NULL DEFAULT false,
    ADD COLUMN seller_auto_repay   boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE trades DROP COLUMN seller_auto_repay, DROP COLUMN buyer_auto_repay, DROP COLUMN seller_account_type,
    DROP COLUMN buyer_account_type;
