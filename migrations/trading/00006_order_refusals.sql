-- Review CN (B82): an order refused while it was funded keeps the
-- refusal's HTTP status and details (margin-service's max_borrowable,
-- margin_level and the like), so a request repeating its client_order_id
-- gets the same answer; and a borrow on record has both its amount and
-- its ID.

-- +goose Up
ALTER TABLE orders
    ADD COLUMN reject_status  INT CHECK (reject_status BETWEEN 400 AND 599),
    ADD COLUMN reject_details JSONB,
    ADD CONSTRAINT orders_borrow_complete CHECK ((borrowed IS NULL) = (borrow_id IS NULL));

-- +goose Down
ALTER TABLE orders
    DROP CONSTRAINT orders_borrow_complete,
    DROP COLUMN reject_details,
    DROP COLUMN reject_status;
