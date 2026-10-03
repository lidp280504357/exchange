-- Switching a custodian from the stand-in to the real gateway (the real
-- gateway's integration, 2026-10-03, decisions B1 and B2; runbook
-- custody.md).

-- +goose Up
-- B1: simulated deposits the ledger no longer expects a custodian to hold
-- (exchangectl ledger custody-reset: DEPOSIT_PENDING up, ADJUSTMENT down),
-- one row per journal, a reverse negative. The custody check shows their
-- sum per asset on a line of its own, simulated and at no custodian, so an
-- expectation of 0 says why.
CREATE TABLE custody_baselines (
    journal_id uuid           PRIMARY KEY,
    provider   text           NOT NULL,
    asset      text           NOT NULL,
    amount     numeric(38,18) NOT NULL CHECK (amount <> 0),
    actor      text           NOT NULL,
    reason     text           NOT NULL,
    created_at timestamptz    NOT NULL
);
CREATE INDEX custody_baselines_asset_idx ON custody_baselines (provider, asset);
ALTER TABLE chain_checks ADD COLUMN baseline numeric(38,18) NOT NULL DEFAULT 0;

-- B2: a custodian's deposit addresses taken out of use (the stand-in's,
-- which no real chain knows: a deposit to one would never arrive), kept
-- for a rollback; withdrawals to them are refused.
CREATE TABLE retired_deposit_addresses (
    network    text        NOT NULL,
    address    text        NOT NULL,
    user_id    uuid        NOT NULL,
    provider   text        NOT NULL,
    created_at timestamptz NOT NULL,
    retired_at timestamptz NOT NULL,
    retired_by text        NOT NULL,
    reason     text        NOT NULL,
    PRIMARY KEY (network, address)
);
CREATE UNIQUE INDEX retired_deposit_addresses_lower_idx ON retired_deposit_addresses (network, lower(address));

-- +goose Down
DROP TABLE retired_deposit_addresses;
ALTER TABLE chain_checks DROP COLUMN baseline;
DROP TABLE custody_baselines;
