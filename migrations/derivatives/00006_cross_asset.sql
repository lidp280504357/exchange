-- A user's cross accounts are one per settlement asset (coin-margined
-- design 2026-10-06 §2.3): USDT for the linear contracts, BTC, ETH and
-- ASTRA for the coin-margined ones, each warned on its own. The accounts
-- from before are USDT's.

-- +goose Up
ALTER TABLE cross_accounts ADD COLUMN asset text NOT NULL DEFAULT 'USDT';
ALTER TABLE cross_accounts ALTER COLUMN asset DROP DEFAULT;
ALTER TABLE cross_accounts DROP CONSTRAINT cross_accounts_pkey;
ALTER TABLE cross_accounts ADD PRIMARY KEY (user_id, asset);

-- +goose Down
DELETE FROM cross_accounts WHERE asset <> 'USDT';
ALTER TABLE cross_accounts DROP CONSTRAINT cross_accounts_pkey;
ALTER TABLE cross_accounts ADD PRIMARY KEY (user_id);
ALTER TABLE cross_accounts DROP COLUMN asset;
