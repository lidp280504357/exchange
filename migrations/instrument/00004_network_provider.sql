-- The custody wallet (ADR-0011, design 2026-09-30 §9.3): a network's funds
-- are moved by the platform's own wallets (provider '') or by a custodian
-- (UDUN), which knows the coin by its own code ("mainCoinType:coinType").

-- +goose Up
ALTER TABLE networks
    ADD COLUMN provider      text NOT NULL DEFAULT '' CHECK (provider IN ('', 'UDUN')),
    ADD COLUMN provider_coin text NOT NULL DEFAULT '' CHECK (length(provider_coin) <= 128),
    ADD CONSTRAINT networks_provider_pair_check CHECK ((provider = '') = (provider_coin = ''));

-- +goose Down
ALTER TABLE networks
    DROP CONSTRAINT networks_provider_pair_check,
    DROP COLUMN provider_coin,
    DROP COLUMN provider;
