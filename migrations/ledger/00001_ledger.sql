-- ledger-service: immutable double-entry bookkeeping, the only source of
-- balances (requirements §5.9, §11.4; ADR-0001). Journals and lines are
-- append-only (enforced by triggers); the lines of one journal sum to zero
-- per asset (enforced at commit); accounts carry the running balances and
-- may not go negative except the counterparty accounts DEPOSIT_PENDING and
-- ADJUSTMENT.

-- +goose Up
CREATE TABLE accounts (
    id           uuid           PRIMARY KEY,
    owner_type   text           NOT NULL CHECK (owner_type IN ('USER', 'SYSTEM')),
    -- The user ID, or SYSTEM.
    owner_id     text           NOT NULL,
    account_type text           NOT NULL CHECK (account_type IN ('SPOT', 'FUTURES', 'FEE_REVENUE', 'INSURANCE_FUND',
                                    'DEPOSIT_PENDING', 'WITHDRAWAL_PENDING', 'UNCLAIMED_DEPOSIT', 'FUNDING_CLEARING',
                                    'MARKET_MAKER', 'GAS_SUPPLY', 'ADJUSTMENT')),
    asset        text           NOT NULL,
    available    numeric(38,18) NOT NULL DEFAULT 0,
    frozen       numeric(38,18) NOT NULL DEFAULT 0,
    -- Lines applied so far.
    version      bigint         NOT NULL DEFAULT 0,
    created_at   timestamptz    NOT NULL DEFAULT now(),
    updated_at   timestamptz    NOT NULL DEFAULT now(),
    UNIQUE (owner_type, owner_id, account_type, asset),
    CHECK ((owner_type = 'USER') = (account_type IN ('SPOT', 'FUTURES'))),
    -- Invariant 3 (§11.4).
    CONSTRAINT accounts_non_negative CHECK (account_type IN ('DEPOSIT_PENDING', 'ADJUSTMENT') OR (available >= 0 AND frozen >= 0))
);
CREATE INDEX accounts_owner ON accounts (owner_id);

CREATE TABLE journals (
    id              uuid        PRIMARY KEY,
    -- Ledger-wide posting order.
    seq             bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    -- Business idempotency key: a repeated posting returns this journal.
    idem_key        text        NOT NULL UNIQUE,
    -- SHA-256 of the posting's content, to tell a replay from a conflict.
    request_hash    bytea       NOT NULL,
    entry_type      text        NOT NULL CHECK (entry_type IN ('DEPOSIT_CREDIT', 'WITHDRAW_FREEZE', 'WITHDRAW_SETTLE',
                                    'WITHDRAW_UNFREEZE', 'ORDER_FREEZE', 'ORDER_UNFREEZE', 'TRADE_SETTLE', 'TRADE_FEE',
                                    'ACCOUNT_TRANSFER', 'FUNDING_PAYMENT', 'REALIZED_PNL', 'LIQUIDATION_SETTLE',
                                    'INSURANCE_CONTRIBUTION', 'ADL_SETTLE', 'INTERNAL_TRANSFER', 'MANUAL_ADJUSTMENT')),
    source_event_id uuid,
    trace_id        text        NOT NULL DEFAULT '',
    memo            text        NOT NULL DEFAULT '',
    posted_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE journal_lines (
    id              bigint         GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    journal_id      uuid           NOT NULL REFERENCES journals (id),
    account_id      uuid           NOT NULL REFERENCES accounts (id),
    asset           text           NOT NULL,
    amount          numeric(38,18) NOT NULL CHECK (amount <> 0),
    balance_kind    text           NOT NULL CHECK (balance_kind IN ('AVAILABLE', 'FROZEN')),
    -- The account's balances right after this line.
    available_after numeric(38,18) NOT NULL,
    frozen_after    numeric(38,18) NOT NULL,
    account_version bigint         NOT NULL,
    UNIQUE (account_id, account_version)
);
CREATE INDEX journal_lines_journal ON journal_lines (journal_id);

-- Spot <-> futures transfers of a user (§7.2 POST /v1/account/transfers).
CREATE TABLE transfers (
    id                uuid           PRIMARY KEY,
    user_id           uuid           NOT NULL,
    idem_key          text           NOT NULL,
    request_hash      bytea          NOT NULL,
    asset             text           NOT NULL,
    amount            numeric(38,18) NOT NULL CHECK (amount > 0),
    from_account_type text           NOT NULL CHECK (from_account_type IN ('SPOT', 'FUTURES')),
    to_account_type   text           NOT NULL CHECK (to_account_type IN ('SPOT', 'FUTURES')),
    status            text           NOT NULL CHECK (status IN ('REQUESTED', 'COMPLETED', 'FAILED')),
    failure_reason    text           NOT NULL DEFAULT '',
    journal_id        uuid           REFERENCES journals (id),
    created_at        timestamptz    NOT NULL DEFAULT now(),
    UNIQUE (user_id, idem_key),
    CHECK (from_account_type <> to_account_type)
);
CREATE INDEX transfers_user ON transfers (user_id, id DESC);

-- Results of the reconciliation runs (§5.9: 差异报告持久化).
CREATE TABLE reconciliation_runs (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    started_at  timestamptz NOT NULL,
    finished_at timestamptz NOT NULL DEFAULT now(),
    -- JOURNAL_BALANCED, ACCOUNT_MATCHES_LINES, SNAPSHOT_MATCHES_ACCOUNT
    check_name  text        NOT NULL,
    mismatches  int         NOT NULL,
    details     jsonb       NOT NULL DEFAULT '[]'
);
CREATE INDEX reconciliation_runs_started ON reconciliation_runs (started_at);

-- +goose StatementBegin
CREATE FUNCTION ledger_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'ledger entries are append-only (ADR-0001)';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER journals_append_only BEFORE UPDATE OR DELETE ON journals
    FOR EACH ROW EXECUTE FUNCTION ledger_append_only();
CREATE TRIGGER journal_lines_append_only BEFORE UPDATE OR DELETE ON journal_lines
    FOR EACH ROW EXECUTE FUNCTION ledger_append_only();
CREATE TRIGGER journals_no_truncate BEFORE TRUNCATE ON journals
    FOR EACH STATEMENT EXECUTE FUNCTION ledger_append_only();
CREATE TRIGGER journal_lines_no_truncate BEFORE TRUNCATE ON journal_lines
    FOR EACH STATEMENT EXECUTE FUNCTION ledger_append_only();

-- Invariant 1 (§11.4), checked when the transaction commits.
-- +goose StatementBegin
CREATE FUNCTION ledger_journal_balanced() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    unbalanced text;
BEGIN
    SELECT asset INTO unbalanced FROM journal_lines WHERE journal_id = NEW.journal_id
        GROUP BY asset HAVING sum(amount) <> 0 LIMIT 1;
    IF unbalanced IS NOT NULL THEN
        RAISE EXCEPTION 'journal % does not balance in %', NEW.journal_id, unbalanced;
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER journal_lines_balanced AFTER INSERT ON journal_lines
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ledger_journal_balanced();

-- +goose Down
DROP TABLE reconciliation_runs;
DROP TABLE transfers;
DROP TRIGGER journal_lines_balanced ON journal_lines;
DROP TRIGGER journal_lines_no_truncate ON journal_lines;
DROP TRIGGER journals_no_truncate ON journals;
DROP TRIGGER journal_lines_append_only ON journal_lines;
DROP TRIGGER journals_append_only ON journals;
DROP TABLE journal_lines;
DROP TABLE journals;
DROP TABLE accounts;
DROP FUNCTION ledger_journal_balanced();
DROP FUNCTION ledger_append_only();
