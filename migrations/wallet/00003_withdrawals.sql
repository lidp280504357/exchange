-- wallet-service withdrawals (requirements §5.10, §11.6; plan §6.3 task
-- 10): users' withdrawal address books with their cooling-off period,
-- withdrawals with their risk assessment and approvals, every signed
-- attempt (replacements share the nonce), the hot wallet's nonces, the
-- day's USDT prices for the limits, and internal deposits: the payee's
-- record of a withdrawal to a platform deposit address.

-- +goose Up
CREATE TABLE withdraw_addresses (
    id         uuid        PRIMARY KEY,
    user_id    uuid        NOT NULL,
    network    text        NOT NULL,
    address    text        NOT NULL CHECK (address ~ '^0x[0-9a-fA-F]{40}$'),
    label      text        NOT NULL DEFAULT '' CHECK (length(label) <= 50),
    created_at timestamptz NOT NULL,
    usable_at  timestamptz NOT NULL,
    deleted_at timestamptz
);
CREATE UNIQUE INDEX withdraw_addresses_active_idx ON withdraw_addresses (user_id, network, lower(address)) WHERE deleted_at IS NULL;
CREATE INDEX withdraw_addresses_user_idx ON withdraw_addresses (user_id, created_at DESC);

CREATE TABLE withdrawals (
    id                     uuid           PRIMARY KEY,
    user_id                uuid           NOT NULL,
    asset                  text           NOT NULL,
    network                text           NOT NULL,
    address                text           NOT NULL,
    amount                 numeric(38,18) NOT NULL CHECK (amount > 0),
    fee                    numeric(38,18) NOT NULL CHECK (fee >= 0),
    internal_user_id       uuid,
    status                 text           NOT NULL CHECK (status IN ('REQUESTED', 'PENDING_REVIEW', 'APPROVED', 'SIGNING',
        'BROADCAST', 'CONFIRMING', 'CONFIRMED', 'INTERNAL_TRANSFER', 'REJECTED', 'CANCELED', 'FAILED')),
    risk_score             int            NOT NULL DEFAULT 0 CHECK (risk_score BETWEEN 0 AND 100),
    risk_reasons           text[]         NOT NULL DEFAULT '{}',
    approvals_required     int            NOT NULL DEFAULT 0 CHECK (approvals_required >= 0),
    approvals              text[]         NOT NULL DEFAULT '{}',
    reject_reason          text           NOT NULL DEFAULT '',
    value_usdt             numeric(38,18) NOT NULL DEFAULT 0,
    nonce                  bigint,
    tx_hash                text,
    block_number           bigint,
    confirmations          int            NOT NULL DEFAULT 0,
    required_confirmations int            NOT NULL,
    freeze_journal_id      uuid,
    settle_journal_id      uuid,
    unfreeze_journal_id    uuid,
    created_at             timestamptz    NOT NULL,
    updated_at             timestamptz    NOT NULL,
    approved_at            timestamptz,
    broadcast_at           timestamptz,
    confirmed_at           timestamptz,
    CHECK (internal_user_id IS NULL OR fee = 0),
    CHECK (status NOT IN ('BROADCAST', 'CONFIRMING', 'CONFIRMED') OR internal_user_id IS NOT NULL OR (nonce IS NOT NULL AND tx_hash IS NOT NULL))
);
CREATE INDEX withdrawals_user_idx ON withdrawals (user_id, id DESC);
CREATE INDEX withdrawals_open_idx ON withdrawals (network, status)
    WHERE status IN ('REQUESTED', 'APPROVED', 'SIGNING', 'BROADCAST', 'CONFIRMING', 'REJECTED', 'CANCELED');

CREATE TABLE withdrawal_attempts (
    tx_hash       text          PRIMARY KEY,
    withdrawal_id uuid          NOT NULL REFERENCES withdrawals (id),
    nonce         bigint        NOT NULL CHECK (nonce >= 0),
    max_fee       numeric(78,0) NOT NULL,
    max_tip       numeric(78,0) NOT NULL,
    raw_tx        text          NOT NULL,
    created_at    timestamptz   NOT NULL
);
CREATE INDEX withdrawal_attempts_withdrawal_idx ON withdrawal_attempts (withdrawal_id, created_at DESC);

CREATE TABLE nonces (
    address    text        PRIMARY KEY,
    next_nonce bigint      NOT NULL CHECK (next_nonce >= 0),
    updated_at timestamptz NOT NULL
);

CREATE TABLE price_snapshots (
    day        date           NOT NULL,
    asset      text           NOT NULL,
    usdt       numeric(38,18) NOT NULL CHECK (usdt > 0),
    source     text           NOT NULL,
    created_at timestamptz    NOT NULL DEFAULT now(),
    PRIMARY KEY (day, asset)
);

ALTER TABLE deposits ADD COLUMN kind text NOT NULL DEFAULT 'CHAIN' CHECK (kind IN ('CHAIN', 'INTERNAL'));
ALTER TABLE deposits DROP CONSTRAINT deposits_tx_hash_check,
    ADD CONSTRAINT deposits_tx_hash_check CHECK (kind = 'INTERNAL' OR tx_hash ~ '^0x[0-9a-f]{64}$');
ALTER TABLE deposits DROP CONSTRAINT deposits_block_number_check,
    ADD CONSTRAINT deposits_block_number_check CHECK (kind = 'INTERNAL' OR block_number > 0);

-- +goose Down
DELETE FROM deposits WHERE kind = 'INTERNAL';
ALTER TABLE deposits DROP CONSTRAINT deposits_block_number_check,
    ADD CONSTRAINT deposits_block_number_check CHECK (block_number > 0);
ALTER TABLE deposits DROP CONSTRAINT deposits_tx_hash_check,
    ADD CONSTRAINT deposits_tx_hash_check CHECK (tx_hash ~ '^0x[0-9a-f]{64}$');
ALTER TABLE deposits DROP COLUMN kind;
DROP TABLE price_snapshots;
DROP TABLE nonces;
DROP TABLE withdrawal_attempts;
DROP TABLE withdrawals;
DROP TABLE withdraw_addresses;
