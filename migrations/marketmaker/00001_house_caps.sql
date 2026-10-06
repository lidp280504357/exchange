-- HOUSE's caps (ADR-0013, ADR-0015) set at runtime (the user's decision of
-- 2026-10-07, review C45): the console changes them (two people, A69) on
-- market-maker's internal API; the service's environment (HOUSE_*) gives
-- the first values. One row, and every change kept.

-- +goose Up
CREATE TABLE house_caps (
    id                boolean        PRIMARY KEY DEFAULT true CHECK (id),
    level             numeric(38,18) NOT NULL CHECK (level >= 0),
    symbol            numeric(38,18) NOT NULL CHECK (symbol >= 0),
    total             numeric(38,18) NOT NULL CHECK (total >= 0),
    contract          numeric(38,18) NOT NULL CHECK (contract >= 0),
    safety            numeric(38,18) NOT NULL CHECK (safety >= 0),
    contract_leverage numeric(38,18) NOT NULL CHECK (contract_leverage >= 0),
    version           bigint         NOT NULL CHECK (version > 0),
    updated_by        text           NOT NULL,
    updated_at        timestamptz    NOT NULL
);

CREATE TABLE house_caps_changes (
    version     bigint      PRIMARY KEY,
    -- The values set, and those before (null for the first, from the
    -- environment).
    caps        jsonb       NOT NULL,
    previous    jsonb,
    actor       text        NOT NULL,
    approver    text        NOT NULL DEFAULT '',
    approval_id text        NOT NULL DEFAULT '',
    reason      text        NOT NULL,
    at          timestamptz NOT NULL
);

-- +goose Down
DROP TABLE house_caps_changes;
DROP TABLE house_caps;
