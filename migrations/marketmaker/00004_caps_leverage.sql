-- HOUSE's contract leverage follows the contracts' specs (review C73; the
-- user's decision of 2026-10-10: leverage per contract as Binance has it,
-- 150 for BTC and ETH): the service bounds a new one by the highest
-- leverage of the contracts HOUSE quotes, and the table keeps a ceiling of
-- 1000 against a slip instead of 125.

-- +goose Up
ALTER TABLE house_caps DROP CONSTRAINT house_caps_bounds;
ALTER TABLE house_caps
    ADD CONSTRAINT house_caps_bounds CHECK (
        level <= 1e15 AND symbol > 0 AND symbol <= 1e15 AND total > 0 AND total <= 1e15 AND contract > 0
        AND contract <= 1e15 AND safety > 0 AND safety <= 1e15 AND contract_leverage BETWEEN 1 AND 1000);

-- +goose Down
ALTER TABLE house_caps DROP CONSTRAINT house_caps_bounds;
ALTER TABLE house_caps
    ADD CONSTRAINT house_caps_bounds CHECK (
        level <= 1e15 AND symbol > 0 AND symbol <= 1e15 AND total > 0 AND total <= 1e15 AND contract > 0
        AND contract <= 1e15 AND safety > 0 AND safety <= 1e15 AND contract_leverage BETWEEN 1 AND 125);
