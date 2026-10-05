-- What triggered each margin liquidation (review CX, B93): AUTO at the
-- liquidation level, MANUAL by an administrator's approved request, with
-- that approval. MarginLiquidationStarted sets them and Completed repeats
-- them; events published before they did leave them NULL.

-- +goose NO TRANSACTION
-- +goose Up
ALTER TABLE margin_liquidations ADD COLUMN IF NOT EXISTS trigger SimpleAggregateFunction(anyLast, Nullable(String)) AFTER symbol;
ALTER TABLE margin_liquidations ADD COLUMN IF NOT EXISTS approval_id SimpleAggregateFunction(anyLast, Nullable(String)) AFTER trigger;

-- +goose Down
ALTER TABLE margin_liquidations DROP COLUMN IF EXISTS approval_id;
ALTER TABLE margin_liquidations DROP COLUMN IF EXISTS trigger;
