-- Linear perpetual contracts settled in their quote asset (requirements
-- §5.8, §11.7; implementation plan §7.3 task 1), e.g. BTC-USDT-PERP. The
-- risk limit ladder is a JSON array of {max_notional, max_leverage, mmr}
-- ordered by max_notional, validated by instrument-service.

-- +goose Up
CREATE TABLE contracts (
    symbol                 text           PRIMARY KEY CHECK (symbol ~ '^[A-Z0-9]{2,10}-[A-Z0-9]{2,10}-PERP$'),
    type                   text           NOT NULL CHECK (type IN ('PERPETUAL')),
    base_asset             text           NOT NULL REFERENCES assets (asset_code),
    quote_asset            text           NOT NULL REFERENCES assets (asset_code),
    index_symbol           text           NOT NULL,
    tick_size              numeric(38,18) NOT NULL CHECK (tick_size > 0),
    lot_size               numeric(38,18) NOT NULL CHECK (lot_size > 0),
    min_quantity           numeric(38,18) NOT NULL CHECK (min_quantity > 0),
    max_quantity           numeric(38,18) NOT NULL CHECK (max_quantity > min_quantity),
    min_notional           numeric(38,18) NOT NULL CHECK (min_notional >= 0),
    price_band             numeric(38,18) NOT NULL CHECK (price_band > 0 AND price_band <= 1),
    risk_tiers             jsonb          NOT NULL CHECK (jsonb_typeof(risk_tiers) = 'array' AND jsonb_array_length(risk_tiers) > 0),
    funding_interval_hours int            NOT NULL CHECK (funding_interval_hours IN (1, 4, 8)),
    interest_rate          numeric(38,18) NOT NULL CHECK (interest_rate >= 0 AND interest_rate <= 0.01),
    funding_cap            numeric(38,18) NOT NULL CHECK (funding_cap > 0 AND funding_cap <= 0.05),
    impact_notional        numeric(38,18) NOT NULL CHECK (impact_notional > 0),
    fee_tier               text           NOT NULL REFERENCES fee_schedules (tier),
    status                 text           NOT NULL DEFAULT 'PREPARE'
                                          CHECK (status IN ('PREPARE', 'TRADING', 'HALT', 'CANCEL_ONLY', 'DELISTED')),
    version                bigint         NOT NULL DEFAULT 1,
    updated_at             timestamptz    NOT NULL DEFAULT now(),
    CHECK (base_asset <> quote_asset),
    CHECK (symbol = base_asset || '-' || quote_asset || '-PERP'),
    CHECK (index_symbol = base_asset || '-' || quote_asset)
);

ALTER TABLE config_history DROP CONSTRAINT config_history_entity_check;
ALTER TABLE config_history ADD CONSTRAINT config_history_entity_check
    CHECK (entity IN ('FEE_SCHEDULE', 'ASSET', 'NETWORK', 'TRADING_PAIR', 'CONTRACT'));

-- +goose Down
DELETE FROM config_history WHERE entity = 'CONTRACT';
ALTER TABLE config_history DROP CONSTRAINT config_history_entity_check;
ALTER TABLE config_history ADD CONSTRAINT config_history_entity_check
    CHECK (entity IN ('FEE_SCHEDULE', 'ASSET', 'NETWORK', 'TRADING_PAIR'));
DROP TABLE contracts;
