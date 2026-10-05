-- The hourly charges of an asset and hour, which the interest run reads
-- back to post them as stored (review CK ②).

-- +goose Up
CREATE INDEX interest_charges_asset_hour ON interest_charges (asset, hour) WHERE borrow_id IS NULL;

-- +goose Down
DROP INDEX interest_charges_asset_hour;
