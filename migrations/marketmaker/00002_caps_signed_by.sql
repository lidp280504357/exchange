-- Which service key signed each change of HOUSE's caps (review FL, C47):
-- "ops" (exchangectl in market-maker's container) or "admin" (the admin
-- console's service, the only one that may name an approver); empty for
-- the first values, from the environment.

-- +goose Up
ALTER TABLE house_caps_changes ADD COLUMN signed_by text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE house_caps_changes DROP COLUMN signed_by;
