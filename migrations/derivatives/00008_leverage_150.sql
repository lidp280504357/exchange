-- B174 (review IR): the contracts' leverage goes to 150x (B168, the BTC
-- and ETH USDT perpetuals' first tier), but a user's setting kept 00001's
-- CHECK of 1 to 125, so choosing 126x to 150x failed in the store (500).
-- The services bound it (LeverageCap, a contract's first tier); this only
-- keeps the column within the same cap.

-- +goose Up
ALTER TABLE settings DROP CONSTRAINT settings_leverage_check,
    ADD CONSTRAINT settings_leverage_check CHECK (leverage BETWEEN 1 AND 150);

-- +goose Down
-- Back to 1 to 125 for what is written next; a setting above stays as it
-- is (NOT VALID) rather than the way down failing on it.
ALTER TABLE settings DROP CONSTRAINT settings_leverage_check,
    ADD CONSTRAINT settings_leverage_check CHECK (leverage BETWEEN 1 AND 125) NOT VALID;
