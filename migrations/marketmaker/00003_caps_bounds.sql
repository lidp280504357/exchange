-- The bounds of HOUSE's caps in the table too (review FP, C48; the API and
-- the environment check them, domain.Caps.Validate): the level cap at
-- least 0 (0: levels whole), the other amounts above 0, every amount at
-- most 10^15 USDT, the contract leverage from 1 to 125.

-- +goose Up
ALTER TABLE house_caps
    ADD CONSTRAINT house_caps_bounds CHECK (
        level <= 1e15 AND symbol > 0 AND symbol <= 1e15 AND total > 0 AND total <= 1e15 AND contract > 0
        AND contract <= 1e15 AND safety > 0 AND safety <= 1e15 AND contract_leverage BETWEEN 1 AND 125);

-- +goose Down
ALTER TABLE house_caps DROP CONSTRAINT house_caps_bounds;
