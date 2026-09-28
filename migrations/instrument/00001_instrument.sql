-- instrument-service: assets, networks, trading pairs and fee schedules
-- (requirements §5.5). Amounts are NUMERIC(38,18) (ADR-0008). Every row
-- carries a version bumped on each change; config_history keeps every
-- version with who changed it and why.

-- +goose Up
CREATE TABLE fee_schedules (
    tier           text           PRIMARY KEY CHECK (tier ~ '^[a-z0-9_-]{1,32}$'),
    maker_fee_rate numeric(38,18) NOT NULL CHECK (maker_fee_rate >= 0 AND maker_fee_rate < 0.1),
    taker_fee_rate numeric(38,18) NOT NULL CHECK (taker_fee_rate >= 0 AND taker_fee_rate < 0.1),
    version        bigint         NOT NULL DEFAULT 1,
    updated_at     timestamptz    NOT NULL DEFAULT now()
);

CREATE TABLE assets (
    asset_code       text        PRIMARY KEY CHECK (asset_code ~ '^[A-Z0-9]{2,10}$'),
    name             text        NOT NULL,
    decimals         int         NOT NULL CHECK (decimals BETWEEN 0 AND 18),
    deposit_enabled  boolean     NOT NULL DEFAULT false,
    withdraw_enabled boolean     NOT NULL DEFAULT false,
    trading_enabled  boolean     NOT NULL DEFAULT false,
    -- Risk switch: blocks new exposure to the asset while set.
    risk_restricted  boolean     NOT NULL DEFAULT false,
    version          bigint      NOT NULL DEFAULT 1,
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- An asset on one chain.
CREATE TABLE networks (
    asset_code       text           NOT NULL REFERENCES assets (asset_code),
    network          text           NOT NULL CHECK (network ~ '^[A-Z0-9_-]{2,24}$'),
    chain            text           NOT NULL,
    contract_address text           NOT NULL DEFAULT '',
    confirmations    int            NOT NULL CHECK (confirmations >= 0),
    min_deposit      numeric(38,18) NOT NULL CHECK (min_deposit >= 0),
    min_withdraw     numeric(38,18) NOT NULL CHECK (min_withdraw >= 0),
    withdraw_fee     numeric(38,18) NOT NULL CHECK (withdraw_fee >= 0),
    memo_required    boolean        NOT NULL DEFAULT false,
    deposit_enabled  boolean        NOT NULL DEFAULT false,
    withdraw_enabled boolean        NOT NULL DEFAULT false,
    version          bigint         NOT NULL DEFAULT 1,
    updated_at       timestamptz    NOT NULL DEFAULT now(),
    PRIMARY KEY (asset_code, network)
);

CREATE TABLE trading_pairs (
    symbol       text           PRIMARY KEY CHECK (symbol ~ '^[A-Z0-9]{2,10}-[A-Z0-9]{2,10}$'),
    base_asset   text           NOT NULL REFERENCES assets (asset_code),
    quote_asset  text           NOT NULL REFERENCES assets (asset_code),
    tick_size    numeric(38,18) NOT NULL CHECK (tick_size > 0),
    lot_size     numeric(38,18) NOT NULL CHECK (lot_size > 0),
    min_quantity numeric(38,18) NOT NULL CHECK (min_quantity > 0),
    max_quantity numeric(38,18) NOT NULL CHECK (max_quantity > min_quantity),
    min_notional numeric(38,18) NOT NULL CHECK (min_notional >= 0),
    price_band   numeric(38,18) NOT NULL CHECK (price_band > 0 AND price_band <= 1),
    fee_tier     text           NOT NULL REFERENCES fee_schedules (tier),
    status       text           NOT NULL DEFAULT 'PREPARE'
                                CHECK (status IN ('PREPARE', 'TRADING', 'HALT', 'CANCEL_ONLY', 'DELISTED')),
    version      bigint         NOT NULL DEFAULT 1,
    updated_at   timestamptz    NOT NULL DEFAULT now(),
    CHECK (base_asset <> quote_asset),
    CHECK (symbol = base_asset || '-' || quote_asset)
);

-- Every version of every configuration row (§5.5: 所有配置变更版本化).
CREATE TABLE config_history (
    id         bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity     text        NOT NULL CHECK (entity IN ('FEE_SCHEDULE', 'ASSET', 'NETWORK', 'TRADING_PAIR')),
    key        text        NOT NULL,
    version    bigint      NOT NULL,
    value      jsonb       NOT NULL,
    actor      text        NOT NULL,
    reason     text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (entity, key, version)
);

-- +goose Down
DROP TABLE config_history;
DROP TABLE trading_pairs;
DROP TABLE networks;
DROP TABLE assets;
DROP TABLE fee_schedules;
