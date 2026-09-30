-- HOUSE's virtual liquidity (ADR-0015): a trade against HOUSE has no order
-- on HOUSE's side (its order ID is the nil UUID) and records the side HOUSE
-- took: BUY, SELL, or '' between users.

-- +goose NO TRANSACTION
-- +goose Up
ALTER TABLE trades ADD COLUMN IF NOT EXISTS house_side LowCardinality(String) DEFAULT '' AFTER seller_fee;

-- +goose Down
ALTER TABLE trades DROP COLUMN IF EXISTS house_side;
