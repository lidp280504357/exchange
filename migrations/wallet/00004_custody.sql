-- The custody wallet (ADR-0011, design 2026-09-30 §9.3): deposit addresses
-- a custodian created (no derivation index), deposits it reported (keyed by
-- its trade ID, as one transaction may pay several addresses), withdrawals
-- handed to it (SUBMITTED, with the custodian's own status), the log of
-- its callbacks, and chain checks that cover every holder of an asset: the
-- platform's wallets and the custodian, and what is on its way out. The
-- custodian's networks are not all EVM: the 0x checks go.

-- +goose Up
ALTER TABLE deposit_addresses
    DROP CONSTRAINT deposit_addresses_address_check,
    ALTER COLUMN derivation_index DROP NOT NULL,
    ADD COLUMN provider text NOT NULL DEFAULT '',
    ADD CONSTRAINT deposit_addresses_source_check CHECK ((provider = '') = (derivation_index IS NOT NULL));

ALTER TABLE deposits
    ADD COLUMN provider_tx_id text,
    DROP CONSTRAINT deposits_network_tx_hash_log_index_key,
    DROP CONSTRAINT deposits_tx_hash_check,
    ADD CONSTRAINT deposits_tx_hash_check CHECK (kind = 'INTERNAL' OR provider_tx_id IS NOT NULL OR tx_hash ~ '^0x[0-9a-f]{64}$'),
    DROP CONSTRAINT deposits_block_number_check,
    ADD CONSTRAINT deposits_block_number_check CHECK (kind = 'INTERNAL' OR provider_tx_id IS NOT NULL OR block_number > 0);
CREATE UNIQUE INDEX deposits_transfer_idx ON deposits (network, tx_hash, log_index) WHERE provider_tx_id IS NULL;
CREATE UNIQUE INDEX deposits_provider_tx_idx ON deposits (provider_tx_id) WHERE provider_tx_id IS NOT NULL;

ALTER TABLE withdraw_addresses DROP CONSTRAINT withdraw_addresses_address_check;

ALTER TABLE withdrawals
    ADD COLUMN provider        text NOT NULL DEFAULT '',
    -- The custodian's last word: SUBMITTED until it acknowledges, then
    -- ACCEPTED, REVIEW, APPROVED, REJECTED, SUCCESS or FAILED.
    ADD COLUMN provider_status text NOT NULL DEFAULT '',
    ADD COLUMN submitted_at    timestamptz,
    DROP CONSTRAINT withdrawals_status_check,
    ADD CONSTRAINT withdrawals_status_check CHECK (status IN ('REQUESTED', 'PENDING_REVIEW', 'APPROVED', 'SIGNING', 'BROADCAST',
        'CONFIRMING', 'SUBMITTED', 'CONFIRMED', 'INTERNAL_TRANSFER', 'REJECTED', 'CANCELED', 'FAILED')),
    DROP CONSTRAINT withdrawals_check1,
    ADD CONSTRAINT withdrawals_check1 CHECK (status NOT IN ('BROADCAST', 'CONFIRMING', 'CONFIRMED') OR internal_user_id IS NOT NULL
        OR provider <> '' OR (nonce IS NOT NULL AND tx_hash IS NOT NULL)),
    ADD CONSTRAINT withdrawals_submitted_check CHECK (status <> 'SUBMITTED' OR (provider <> '' AND submitted_at IS NOT NULL));
DROP INDEX withdrawals_open_idx;
CREATE INDEX withdrawals_open_idx ON withdrawals (network, status)
    WHERE status IN ('REQUESTED', 'APPROVED', 'SIGNING', 'BROADCAST', 'CONFIRMING', 'SUBMITTED', 'REJECTED', 'CANCELED');

-- Every callback as received (at most 16 KiB of it), what its signature
-- said and what became of it. A verified one is kept once per trade and
-- status: the custodian's retries count as attempts.
CREATE TABLE custody_callbacks (
    id           uuid           PRIMARY KEY,
    provider     text           NOT NULL,
    trade_id     text           NOT NULL DEFAULT '',
    kind         text           NOT NULL DEFAULT '' CHECK (kind IN ('', 'DEPOSIT', 'WITHDRAWAL')),
    -- The custodian's status code (UDUN: 0 to 4); NULL when unreadable.
    status       int,
    business_id  text           NOT NULL DEFAULT '',
    coin         text           NOT NULL DEFAULT '',
    address      text           NOT NULL DEFAULT '',
    amount       numeric(38,18),
    tx_hash      text           NOT NULL DEFAULT '',
    raw          text           NOT NULL CHECK (length(raw) <= 16384),
    signature_ok boolean        NOT NULL,
    result       text           NOT NULL CHECK (result IN ('RECEIVED', 'APPLIED', 'IGNORED', 'UNMATCHED', 'REJECTED', 'FAILED')),
    detail       text           NOT NULL DEFAULT '',
    attempts     int            NOT NULL DEFAULT 1 CHECK (attempts > 0),
    received_at  timestamptz    NOT NULL,
    processed_at timestamptz,
    CHECK (signature_ok OR result = 'REJECTED')
);
CREATE UNIQUE INDEX custody_callbacks_trade_idx ON custody_callbacks (provider, trade_id, status) WHERE signature_ok;
CREATE INDEX custody_callbacks_result_idx ON custody_callbacks (result, id DESC);

ALTER TABLE chain_checks
    -- What the asset's other holders held at the same time (the custodian
    -- for the platform's wallets and the other way round).
    ADD COLUMN elsewhere numeric(38,18) NOT NULL DEFAULT 0,
    -- Withdrawals with the custodian that it may have sent already.
    ADD COLUMN in_flight numeric(38,18) NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE chain_checks DROP COLUMN in_flight, DROP COLUMN elsewhere;
DROP TABLE custody_callbacks;
DELETE FROM withdrawals WHERE provider <> '';
DROP INDEX withdrawals_open_idx;
CREATE INDEX withdrawals_open_idx ON withdrawals (network, status)
    WHERE status IN ('REQUESTED', 'APPROVED', 'SIGNING', 'BROADCAST', 'CONFIRMING', 'REJECTED', 'CANCELED');
ALTER TABLE withdrawals
    DROP CONSTRAINT withdrawals_submitted_check,
    DROP CONSTRAINT withdrawals_check1,
    ADD CONSTRAINT withdrawals_check1 CHECK (status NOT IN ('BROADCAST', 'CONFIRMING', 'CONFIRMED') OR internal_user_id IS NOT NULL
        OR (nonce IS NOT NULL AND tx_hash IS NOT NULL)),
    DROP CONSTRAINT withdrawals_status_check,
    ADD CONSTRAINT withdrawals_status_check CHECK (status IN ('REQUESTED', 'PENDING_REVIEW', 'APPROVED', 'SIGNING', 'BROADCAST',
        'CONFIRMING', 'CONFIRMED', 'INTERNAL_TRANSFER', 'REJECTED', 'CANCELED', 'FAILED')),
    DROP COLUMN submitted_at,
    DROP COLUMN provider_status,
    DROP COLUMN provider;
DELETE FROM withdraw_addresses WHERE address !~ '^0x[0-9a-fA-F]{40}$';
ALTER TABLE withdraw_addresses ADD CONSTRAINT withdraw_addresses_address_check CHECK (address ~ '^0x[0-9a-fA-F]{40}$');
DELETE FROM deposits WHERE provider_tx_id IS NOT NULL;
DROP INDEX deposits_provider_tx_idx;
DROP INDEX deposits_transfer_idx;
ALTER TABLE deposits
    DROP CONSTRAINT deposits_block_number_check,
    ADD CONSTRAINT deposits_block_number_check CHECK (kind = 'INTERNAL' OR block_number > 0),
    DROP CONSTRAINT deposits_tx_hash_check,
    ADD CONSTRAINT deposits_tx_hash_check CHECK (kind = 'INTERNAL' OR tx_hash ~ '^0x[0-9a-f]{64}$'),
    ADD CONSTRAINT deposits_network_tx_hash_log_index_key UNIQUE (network, tx_hash, log_index),
    DROP COLUMN provider_tx_id;
DELETE FROM deposit_addresses WHERE provider <> '';
ALTER TABLE deposit_addresses
    DROP CONSTRAINT deposit_addresses_source_check,
    DROP COLUMN provider,
    ALTER COLUMN derivation_index SET NOT NULL,
    ADD CONSTRAINT deposit_addresses_address_check CHECK (address ~ '^0x[0-9a-fA-F]{40}$');
