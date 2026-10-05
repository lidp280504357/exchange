-- Margin trading (design 2026-10-06, batch E2; review CM): an order on a
-- margin account with side_effect AUTO_BORROW keeps what margin-service
-- borrowed for it before its freeze, and the borrow's ID, also when the
-- freeze was then refused (the borrow stays; the user repays it).

-- +goose Up
ALTER TABLE orders
    ADD COLUMN borrowed  NUMERIC(38,18) CHECK (borrowed > 0),
    ADD COLUMN borrow_id TEXT;

-- +goose Down
ALTER TABLE orders
    DROP COLUMN borrow_id,
    DROP COLUMN borrowed;
