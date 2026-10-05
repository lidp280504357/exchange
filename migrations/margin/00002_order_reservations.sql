-- Orders on margin accounts (margin design 2026-10-06 §5.1, batch E2):
-- what ReserveOrder answered spot-trading-service for each order, so that
-- a repeat (the trading service's recovery) gets the first answer, a zero
-- borrow included (review CH ④).

-- +goose Up
CREATE TABLE order_reservations (
    order_id     uuid           PRIMARY KEY,
    user_id      uuid           NOT NULL,
    account_type text           NOT NULL CHECK (account_type IN ('MARGIN_CROSS', 'MARGIN_ISOLATED')),
    symbol       text           NOT NULL,
    side_effect  text           NOT NULL CHECK (side_effect IN ('NONE', 'AUTO_BORROW', 'AUTO_REPAY')),
    -- What AUTO_BORROW borrowed of the frozen asset, and the borrow.
    borrowed     numeric(38,18) NOT NULL DEFAULT 0 CHECK (borrowed >= 0),
    borrow_id    uuid,
    -- The margin level after the borrow and the order filled; NULL
    -- without debts.
    margin_level numeric(20,8),
    created_at   timestamptz    NOT NULL DEFAULT now(),
    CHECK ((borrowed > 0) = (borrow_id IS NOT NULL))
);
CREATE INDEX order_reservations_user ON order_reservations (user_id, created_at DESC);

-- +goose Down
DROP TABLE order_reservations;
