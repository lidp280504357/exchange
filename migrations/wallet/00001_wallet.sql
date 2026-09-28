-- wallet-service deposits (requirements §5.10, §11.5; plan §6.3 task 9):
-- deposit addresses derived per user and network from the deposit
-- account's xpub (m/44'/60'/0'/0/index), deposits keyed by their transfer,
-- and the block scanner's cursor with the recent block hashes it checks
-- for reorganizations. Amounts are NUMERIC(38,18); raw chain amounts
-- NUMERIC(78,0) fit any uint256. IDs UUIDv7 (ADR-0008).

-- +goose Up
CREATE TABLE address_indexes (
    network    text PRIMARY KEY,
    next_index int  NOT NULL CHECK (next_index >= 0)
);

CREATE TABLE deposit_addresses (
    user_id          uuid        NOT NULL,
    network          text        NOT NULL,
    derivation_index int         NOT NULL CHECK (derivation_index >= 0),
    address          text        NOT NULL CHECK (address ~ '^0x[0-9a-fA-F]{40}$'),
    created_at       timestamptz NOT NULL,
    PRIMARY KEY (user_id, network),
    UNIQUE (network, derivation_index)
);
CREATE UNIQUE INDEX deposit_addresses_address_idx ON deposit_addresses (network, lower(address));

CREATE TABLE deposits (
    id                     uuid           PRIMARY KEY,
    user_id                uuid           NOT NULL,
    -- NULL for a token no asset is configured for.
    asset                  text,
    network                text           NOT NULL,
    address                text           NOT NULL,
    -- NULL for the chain's coin.
    contract               text,
    tx_hash                text           NOT NULL CHECK (tx_hash ~ '^0x[0-9a-f]{64}$'),
    -- The token log's index; -1 for the chain's coin.
    log_index              bigint         NOT NULL CHECK (log_index >= -1),
    block_number           bigint         NOT NULL CHECK (block_number > 0),
    block_hash             text           NOT NULL,
    amount                 numeric(38,18) NOT NULL CHECK (amount >= 0),
    raw_amount             numeric(78,0)  NOT NULL CHECK (raw_amount > 0),
    confirmations          int            NOT NULL DEFAULT 0 CHECK (confirmations >= 0),
    required_confirmations int            NOT NULL CHECK (required_confirmations >= 0),
    unclaimed              boolean        NOT NULL DEFAULT false,
    reason                 text           CHECK (reason IN ('BELOW_MINIMUM', 'ACCOUNT_CLOSED', 'NOT_ELIGIBLE', 'UNSUPPORTED_TOKEN')),
    status                 text           NOT NULL
        CHECK (status IN ('DETECTED', 'CONFIRMING', 'CONFIRMED', 'CREDITED', 'ORPHANED', 'REJECTED')),
    journal_id             uuid,
    credit_requested_at    timestamptz,
    detected_at            timestamptz    NOT NULL,
    confirmed_at           timestamptz,
    credited_at            timestamptz,
    updated_at             timestamptz    NOT NULL DEFAULT now(),
    UNIQUE (network, tx_hash, log_index),
    CHECK (asset IS NOT NULL OR (status = 'REJECTED' AND reason = 'UNSUPPORTED_TOKEN')),
    CHECK (unclaimed = false OR reason IS NOT NULL),
    CHECK (status <> 'CREDITED' OR (journal_id IS NOT NULL AND NOT unclaimed))
);
CREATE INDEX deposits_user_idx ON deposits (user_id, id DESC);
CREATE INDEX deposits_open_idx ON deposits (network, status) WHERE status IN ('DETECTED', 'CONFIRMING', 'CONFIRMED');

CREATE TABLE scan_cursors (
    network    text        PRIMARY KEY,
    block      bigint      NOT NULL CHECK (block >= 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE scanned_blocks (
    network text   NOT NULL,
    number  bigint NOT NULL,
    hash    text   NOT NULL,
    PRIMARY KEY (network, number)
);

-- +goose Down
DROP TABLE scanned_blocks;
DROP TABLE scan_cursors;
DROP TABLE deposits;
DROP TABLE deposit_addresses;
DROP TABLE address_indexes;
