-- Where a settled funding rate came from (coin-margined design 2026-10-06
-- §3.1, batch G3a): PLATFORM, computed here from the period's premium
-- samples, or BINANCE, the rate of the Binance contract the contract
-- follows while market.reference_mark is on for it (the rate Binance
-- settled, or its last estimate for the period when the settled one did
-- not come within two minutes). The periods settled before are PLATFORM.

-- +goose Up
ALTER TABLE funding_periods ADD COLUMN source text NOT NULL DEFAULT 'PLATFORM' CHECK (source IN ('PLATFORM', 'BINANCE'));

-- +goose Down
ALTER TABLE funding_periods DROP COLUMN source;
