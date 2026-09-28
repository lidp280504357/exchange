-- wallet-service operations (requirements §5.10, §11.4; plan §6.3 task
-- 10): operator commands carried out by the network's processor, sweeps
-- of deposit addresses to the hot wallet, the gas of platform
-- transactions until the ledger books it, platform fundings of system
-- accounts, and the chain checks of invariant 4.

-- +goose Up
CREATE TABLE commands (
    id           uuid        PRIMARY KEY,
    network      text        NOT NULL,
    kind         text        NOT NULL CHECK (kind IN ('SWEEP', 'FUND', 'RECONCILE')),
    args         jsonb       NOT NULL DEFAULT '{}',
    status       text        NOT NULL CHECK (status IN ('PENDING', 'DONE', 'FAILED')),
    result       text        NOT NULL DEFAULT '',
    requested_by text        NOT NULL,
    created_at   timestamptz NOT NULL,
    done_at      timestamptz
);
CREATE INDEX commands_pending_idx ON commands (network, created_at) WHERE status = 'PENDING';

CREATE TABLE sweeps (
    id               uuid           PRIMARY KEY,
    network          text           NOT NULL,
    address          text           NOT NULL,
    derivation_index int            NOT NULL CHECK (derivation_index >= 0),
    asset            text           NOT NULL,
    amount           numeric(38,18) NOT NULL CHECK (amount > 0),
    nonce            bigint         NOT NULL CHECK (nonce >= 0),
    tx_hash          text           NOT NULL UNIQUE,
    raw_tx           text           NOT NULL,
    status           text           NOT NULL CHECK (status IN ('BROADCAST', 'CONFIRMED', 'FAILED')),
    command_id       uuid,
    created_at       timestamptz    NOT NULL,
    updated_at       timestamptz    NOT NULL
);
CREATE INDEX sweeps_open_idx ON sweeps (network) WHERE status = 'BROADCAST';

CREATE TABLE chain_fees (
    tx_hash    text           PRIMARY KEY,
    network    text           NOT NULL,
    asset      text           NOT NULL,
    amount     numeric(38,18) NOT NULL CHECK (amount >= 0),
    purpose    text           NOT NULL CHECK (purpose IN ('SWEEP', 'WITHDRAWAL')),
    reference  text           NOT NULL,
    journal_id uuid,
    booked_at  timestamptz,
    created_at timestamptz    NOT NULL DEFAULT now(),
    CHECK ((journal_id IS NULL) = (booked_at IS NULL))
);
CREATE INDEX chain_fees_unbooked_idx ON chain_fees (network) WHERE booked_at IS NULL;

CREATE TABLE fundings (
    tx_hash      text           PRIMARY KEY,
    network      text           NOT NULL,
    asset        text           NOT NULL,
    account_type text           NOT NULL,
    amount       numeric(38,18) NOT NULL CHECK (amount > 0),
    journal_id   uuid           NOT NULL,
    command_id   uuid,
    created_at   timestamptz    NOT NULL
);

CREATE TABLE chain_checks (
    id         bigserial      PRIMARY KEY,
    network    text           NOT NULL,
    asset      text           NOT NULL,
    chain      numeric(38,18) NOT NULL,
    ledger     numeric(38,18) NOT NULL,
    unbooked   numeric(38,18) NOT NULL,
    shortfall  numeric(38,18) NOT NULL,
    addresses  int            NOT NULL,
    checked_at timestamptz    NOT NULL
);
CREATE INDEX chain_checks_latest_idx ON chain_checks (network, asset, checked_at DESC);

-- +goose Down
DROP TABLE chain_checks;
DROP TABLE fundings;
DROP TABLE chain_fees;
DROP TABLE sweeps;
DROP TABLE commands;
