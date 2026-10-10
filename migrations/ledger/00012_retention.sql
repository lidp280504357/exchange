-- The retention run (M1, ADR-0022): journals, their lines and their line
-- types older than the window may be deleted, by the run alone - it sets
-- ledger.retention for its own transactions; nothing else deletes them and
-- nobody updates or truncates them. What the deleted history added up to
-- stays as checkpoints, so the reconciliation still adds up, and the keys
-- of the deleted journals stay, so a posting with one is still a replay.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ledger_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND current_setting('ledger.retention', true) = 'on' THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'ledger entries are append-only (ADR-0001)';
END;
$$;
-- +goose StatementEnd

-- The keys of the journals the run deleted, kept for the keys' window.
CREATE TABLE journal_keys (
    idem_key     text        PRIMARY KEY,
    request_hash bytea       NOT NULL,
    journal_id   uuid        NOT NULL,
    seq          bigint      NOT NULL,
    entry_type   text        NOT NULL,
    posted_at    timestamptz NOT NULL
);
CREATE INDEX journal_keys_posted ON journal_keys (posted_at);

-- What the deleted history added up to, by check: ACCOUNT_AVAILABLE and
-- ACCOUNT_FROZEN by account (the lines' amounts), TRADE_SETTLE_BOOKED and
-- TRADE_FEE_BOOKED by asset (the journals'), TRADE_SETTLE_EXPECTED and
-- TRADE_FEE_EXPECTED by asset and TRADES by symbol (the trades').
CREATE TABLE checkpoints (
    name       text           NOT NULL,
    key        text           NOT NULL,
    amount     numeric(38,18) NOT NULL DEFAULT 0,
    count      bigint         NOT NULL DEFAULT 0,
    updated_at timestamptz    NOT NULL DEFAULT now(),
    PRIMARY KEY (name, key)
);

-- +goose Down
-- Once the run has deleted anything, the keys and the checkpoints are what
-- stands for it: going back would lose them (B199).
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM journal_keys) OR EXISTS (SELECT 1 FROM checkpoints) THEN
        RAISE EXCEPTION 'the retention run has deleted journals: their keys and checkpoints cannot be dropped (ADR-0022)';
    END IF;
END;
$$;
-- +goose StatementEnd
DROP TABLE checkpoints;
DROP TABLE journal_keys;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ledger_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'ledger entries are append-only (ADR-0001)';
END;
$$;
-- +goose StatementEnd
