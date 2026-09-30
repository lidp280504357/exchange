-- Listing metadata for the market lists and reference market data
-- (ADR-0010, design 2026-09-30 §8.4-§8.5): an asset's market-cap rank and
-- sector tags; a pair's reference market (Binance symbol and price
-- multiplier) and listing time; a network's display name, address format,
-- usual credit time and block explorer links.

-- +goose Up
ALTER TABLE assets
    ADD COLUMN rank       int    NOT NULL DEFAULT 0 CHECK (rank BETWEEN 0 AND 100000),
    ADD COLUMN categories text[] NOT NULL DEFAULT '{}';

ALTER TABLE trading_pairs
    ADD COLUMN reference_symbol     text           NOT NULL DEFAULT '' CHECK (reference_symbol ~ '^([A-Z0-9]{2,20})?$'),
    ADD COLUMN reference_multiplier numeric(38,18) NOT NULL DEFAULT 1 CHECK (reference_multiplier >= 1),
    ADD COLUMN listed_at            timestamptz    NOT NULL DEFAULT now();

ALTER TABLE networks
    ADD COLUMN display_name         text NOT NULL DEFAULT '',
    ADD COLUMN address_format       text NOT NULL DEFAULT 'EVM' CHECK (address_format IN ('EVM', 'TRON', 'BTC')),
    ADD COLUMN eta_minutes          int  NOT NULL DEFAULT 0 CHECK (eta_minutes BETWEEN 0 AND 10080),
    ADD COLUMN explorer_tx_url      text NOT NULL DEFAULT '',
    ADD COLUMN explorer_address_url text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE networks
    DROP COLUMN explorer_address_url,
    DROP COLUMN explorer_tx_url,
    DROP COLUMN eta_minutes,
    DROP COLUMN address_format,
    DROP COLUMN display_name;

ALTER TABLE trading_pairs
    DROP COLUMN listed_at,
    DROP COLUMN reference_multiplier,
    DROP COLUMN reference_symbol;

ALTER TABLE assets
    DROP COLUMN categories,
    DROP COLUMN rank;
