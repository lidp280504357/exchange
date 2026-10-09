-- The trades of orders on margin accounts, by order (B163): RepayReleased
-- repays an order's borrow only once its trades are all settled. Partial,
-- so the spot trades, nearly all of them, cost no more to record.

-- +goose Up
CREATE INDEX trades_margin_buyer_order ON trades (buyer_order_id) WHERE buyer_account_type IN ('MARGIN_CROSS', 'MARGIN_ISOLATED');
CREATE INDEX trades_margin_seller_order ON trades (seller_order_id) WHERE seller_account_type IN ('MARGIN_CROSS', 'MARGIN_ISOLATED');

-- +goose Down
DROP INDEX trades_margin_seller_order;
DROP INDEX trades_margin_buyer_order;
