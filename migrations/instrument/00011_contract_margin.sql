-- Coin-margined (inverse) perpetuals beside the linear ones (design
-- 2026-10-06 §2.1, batch G0): margin_type USDT (linear, settled in the
-- quote asset) or COIN (inverse: quoted in USD, settled in the base
-- asset, whole contracts of contract_size USD each), the settlement
-- asset, and the Binance contract the market data follows. An inverse
-- contract's quote USD is no asset: the quote asset loses its foreign key,
-- which the settlement asset, equal to it on a linear contract, carries;
-- its index is the base asset's USDT pair. margin_type, settle_asset and
-- contract_size never change once listed (instrument-service checks it).

-- +goose Up
ALTER TABLE contracts
    ADD COLUMN margin_type      text           NOT NULL DEFAULT 'USDT' CHECK (margin_type IN ('USDT', 'COIN')),
    ADD COLUMN settle_asset     text           REFERENCES assets (asset_code),
    ADD COLUMN contract_size    numeric(38,18) NOT NULL DEFAULT 0 CHECK (contract_size >= 0),
    ADD COLUMN reference_symbol text           NOT NULL DEFAULT '' CHECK (reference_symbol ~ '^([A-Z0-9_]{2,20})?$');
UPDATE contracts SET settle_asset = quote_asset;
ALTER TABLE contracts ALTER COLUMN settle_asset SET NOT NULL;
ALTER TABLE contracts DROP CONSTRAINT contracts_quote_asset_fkey;
-- contracts_check3 was index_symbol = base_asset || '-' || quote_asset.
ALTER TABLE contracts DROP CONSTRAINT contracts_check3;
ALTER TABLE contracts ADD CONSTRAINT contracts_margin_check CHECK (
    (margin_type = 'USDT' AND settle_asset = quote_asset AND contract_size = 0
        AND index_symbol = base_asset || '-' || quote_asset)
    OR (margin_type = 'COIN' AND quote_asset = 'USD' AND settle_asset = base_asset AND contract_size > 0
        AND index_symbol = base_asset || '-USDT'));

-- +goose Down
DELETE FROM contracts WHERE margin_type = 'COIN';
ALTER TABLE contracts DROP CONSTRAINT contracts_margin_check;
ALTER TABLE contracts ADD CONSTRAINT contracts_check3 CHECK (index_symbol = base_asset || '-' || quote_asset);
ALTER TABLE contracts ADD CONSTRAINT contracts_quote_asset_fkey FOREIGN KEY (quote_asset) REFERENCES assets (asset_code);
ALTER TABLE contracts
    DROP COLUMN reference_symbol,
    DROP COLUMN contract_size,
    DROP COLUMN settle_asset,
    DROP COLUMN margin_type;
