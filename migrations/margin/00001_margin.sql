-- margin-service (margin design 2026-10-06): the margin terms of assets and
-- pairs, users' margin accounts, and the record of what they borrowed,
-- repaid, were charged and moved. Balances and debts live in the ledger
-- (ADR-0001); the loans here mirror the debts for the hourly interest, the
-- pools' use and the limits, and reconciliation compares the two.

-- +goose Up

-- An asset's terms (design §4.1); operators change them (the console's
-- two-person approval for rates), each change bumps version.
CREATE TABLE asset_terms (
    asset           text           PRIMARY KEY,
    borrowable      boolean        NOT NULL,
    -- Counts in a margin account's total assets, at its haircut.
    collateral      boolean        NOT NULL,
    haircut         numeric(10,4)  NOT NULL CHECK (haircut > 0 AND haircut <= 1),
    -- What the platform lends at most, and what one user may owe.
    pool_cap        numeric(38,18) NOT NULL CHECK (pool_cap >= 0),
    user_cap        numeric(38,18) NOT NULL CHECK (user_cap >= 0 AND user_cap <= pool_cap),
    interest_model  text           NOT NULL CHECK (interest_model IN ('FIXED', 'FLOATING')),
    -- Hourly rates: the fixed model's, and the floating curve (base at an
    -- idle pool, kink_rate at the kink's use, max_rate when all is lent).
    fixed_rate      numeric(20,12) NOT NULL CHECK (fixed_rate >= 0),
    float_base      numeric(20,12) NOT NULL CHECK (float_base >= 0),
    float_kink      numeric(10,4)  NOT NULL CHECK (float_kink > 0 AND float_kink < 1),
    float_kink_rate numeric(20,12) NOT NULL CHECK (float_kink_rate >= float_base),
    float_max_rate  numeric(20,12) NOT NULL CHECK (float_max_rate >= float_kink_rate),
    version         bigint         NOT NULL DEFAULT 1,
    updated_at      timestamptz    NOT NULL DEFAULT now(),
    updated_by      text           NOT NULL DEFAULT ''
);

-- A pair's isolated terms (design §4.2, §4.4).
CREATE TABLE pair_terms (
    symbol            text          PRIMARY KEY,
    base_asset        text          NOT NULL,
    quote_asset       text          NOT NULL,
    -- Takes isolated accounts.
    isolated          boolean       NOT NULL,
    leverage          int           NOT NULL CHECK (leverage BETWEEN 2 AND 10),
    warn_level        numeric(10,4) NOT NULL,
    liquidation_level numeric(10,4) NOT NULL CHECK (liquidation_level > 1 AND liquidation_level < warn_level),
    liquidation_fee   numeric(10,6) NOT NULL CHECK (liquidation_fee >= 0 AND liquidation_fee <= 0.1),
    version           bigint        NOT NULL DEFAULT 1,
    updated_at        timestamptz   NOT NULL DEFAULT now(),
    updated_by        text          NOT NULL DEFAULT '',
    CHECK (base_asset <> quote_asset)
);

-- The cross account's terms: one row.
CREATE TABLE cross_terms (
    id                boolean       PRIMARY KEY DEFAULT true CHECK (id),
    leverage          int           NOT NULL CHECK (leverage BETWEEN 2 AND 10),
    warn_level        numeric(10,4) NOT NULL,
    liquidation_level numeric(10,4) NOT NULL CHECK (liquidation_level > 1 AND liquidation_level < warn_level),
    liquidation_fee   numeric(10,6) NOT NULL CHECK (liquidation_fee >= 0 AND liquidation_fee <= 0.1),
    version           bigint        NOT NULL DEFAULT 1,
    updated_at        timestamptz   NOT NULL DEFAULT now(),
    updated_by        text          NOT NULL DEFAULT ''
);

-- What each pool has lent: the principal owed, with borrows in flight.
-- Borrows lock the asset's row, so its cap holds across users.
CREATE TABLE pools (
    asset text           PRIMARY KEY,
    lent  numeric(38,18) NOT NULL DEFAULT 0 CHECK (lent >= 0)
);

-- The rate each hour charges per asset: FLOATING from the pool's use on
-- the hour (lent over pool_cap), recorded so that every charge can be
-- worked out again (design §4.3).
CREATE TABLE hourly_rates (
    asset          text           NOT NULL,
    hour           timestamptz    NOT NULL CHECK (date_trunc('hour', hour) = hour),
    interest_model text           NOT NULL CHECK (interest_model IN ('FIXED', 'FLOATING')),
    rate           numeric(20,12) NOT NULL CHECK (rate >= 0),
    lent           numeric(38,18) NOT NULL,
    pool_cap       numeric(38,18) NOT NULL,
    created_at     timestamptz    NOT NULL DEFAULT now(),
    PRIMARY KEY (asset, hour)
);

-- Users' margin accounts: the cross account (symbol '') and an isolated
-- account per pair, created by their first transfer in.
CREATE TABLE accounts (
    user_id       uuid        NOT NULL,
    account_type  text        NOT NULL CHECK (account_type IN ('MARGIN_CROSS', 'MARGIN_ISOLATED')),
    symbol        text        NOT NULL DEFAULT '',
    -- WARNED under the warning level, LIQUIDATING from the trigger to the
    -- end, FROZEN by an operator.
    status        text        NOT NULL DEFAULT 'NORMAL' CHECK (status IN ('NORMAL', 'WARNED', 'LIQUIDATING', 'FROZEN')),
    warned_at     timestamptz,
    frozen_reason text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, account_type, symbol),
    CHECK ((account_type = 'MARGIN_CROSS') = (symbol = ''))
);

-- Loans: per account and asset, the principal and interest owed.
CREATE TABLE loans (
    user_id      uuid           NOT NULL,
    account_type text           NOT NULL,
    symbol       text           NOT NULL DEFAULT '',
    asset        text           NOT NULL,
    principal    numeric(38,18) NOT NULL DEFAULT 0 CHECK (principal >= 0),
    interest     numeric(38,18) NOT NULL DEFAULT 0 CHECK (interest >= 0),
    updated_at   timestamptz    NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, account_type, symbol, asset),
    FOREIGN KEY (user_id, account_type, symbol) REFERENCES accounts (user_id, account_type, symbol)
);
CREATE INDEX loans_open ON loans (asset) WHERE principal > 0 OR interest > 0;

-- Borrows: a user's (or an order's AUTO_BORROW) loan with its first
-- hour's interest. PENDING until the ledger booked it (DONE) or refused
-- it (FAILED); recovery retries the PENDING ones with the same key.
CREATE TABLE borrows (
    borrow_id      uuid           PRIMARY KEY,
    user_id        uuid           NOT NULL,
    account_type   text           NOT NULL,
    symbol         text           NOT NULL DEFAULT '',
    asset          text           NOT NULL,
    amount         numeric(38,18) NOT NULL CHECK (amount > 0),
    interest_model text           NOT NULL,
    hourly_rate    numeric(20,12) NOT NULL,
    first_interest numeric(38,18) NOT NULL CHECK (first_interest >= 0),
    order_id       uuid,
    -- The client's Idempotency-Key and a digest of the request.
    idem_key       text           NOT NULL,
    request_hash   bytea          NOT NULL,
    status         text           NOT NULL CHECK (status IN ('PENDING', 'DONE', 'FAILED')),
    failure        text           NOT NULL DEFAULT '',
    created_at     timestamptz    NOT NULL,
    done_at        timestamptz,
    UNIQUE (user_id, idem_key)
);
CREATE INDEX borrows_pending ON borrows (created_at) WHERE status = 'PENDING';
CREATE INDEX borrows_loan ON borrows (user_id, account_type, symbol, asset, created_at);

-- Repayments, interest first: by the user, an order's AUTO_REPAY or a
-- liquidation.
CREATE TABLE repays (
    repay_id         uuid           PRIMARY KEY,
    user_id          uuid           NOT NULL,
    account_type     text           NOT NULL,
    symbol           text           NOT NULL DEFAULT '',
    asset            text           NOT NULL,
    interest_repaid  numeric(38,18) NOT NULL CHECK (interest_repaid >= 0),
    principal_repaid numeric(38,18) NOT NULL CHECK (principal_repaid >= 0),
    reason           text           NOT NULL CHECK (reason IN ('USER', 'AUTO_REPAY', 'LIQUIDATION')),
    order_id         uuid,
    liquidation_id   uuid,
    idem_key         text           NOT NULL,
    request_hash     bytea          NOT NULL,
    status           text           NOT NULL CHECK (status IN ('PENDING', 'DONE', 'FAILED')),
    failure          text           NOT NULL DEFAULT '',
    created_at       timestamptz    NOT NULL,
    done_at          timestamptz,
    UNIQUE (user_id, idem_key),
    CHECK (interest_repaid + principal_repaid > 0)
);
CREATE INDEX repays_pending ON repays (created_at) WHERE status = 'PENDING';
CREATE INDEX repays_loan ON repays (user_id, account_type, symbol, asset, created_at);

-- Interest charges: a borrow's first hour (borrow_id set, hour the time
-- of borrowing) and each hour's charge on the principal owed on the hour.
CREATE TABLE interest_charges (
    interest_id    uuid           PRIMARY KEY,
    user_id        uuid           NOT NULL,
    account_type   text           NOT NULL,
    symbol         text           NOT NULL DEFAULT '',
    asset          text           NOT NULL,
    hour           timestamptz    NOT NULL,
    principal      numeric(38,18) NOT NULL CHECK (principal > 0),
    interest_model text           NOT NULL,
    hourly_rate    numeric(20,12) NOT NULL,
    interest       numeric(38,18) NOT NULL CHECK (interest >= 0),
    borrow_id      uuid,
    status         text           NOT NULL CHECK (status IN ('PENDING', 'DONE')),
    created_at     timestamptz    NOT NULL,
    done_at        timestamptz
);
CREATE UNIQUE INDEX interest_charges_hourly ON interest_charges (user_id, account_type, symbol, asset, hour)
    WHERE borrow_id IS NULL;
CREATE INDEX interest_charges_user ON interest_charges (user_id, created_at DESC, interest_id DESC);
CREATE INDEX interest_charges_pending ON interest_charges (created_at) WHERE status = 'PENDING';

-- The hourly charging runs: an hour is DONE once every loan owed on it
-- was charged.
CREATE TABLE interest_runs (
    hour       timestamptz PRIMARY KEY CHECK (date_trunc('hour', hour) = hour),
    status     text        NOT NULL CHECK (status IN ('RUNNING', 'DONE')),
    loans      int         NOT NULL DEFAULT 0,
    started_at timestamptz NOT NULL DEFAULT now(),
    done_at    timestamptz
);

-- Transfers between SPOT and a margin account.
CREATE TABLE transfers (
    transfer_id  uuid           PRIMARY KEY,
    user_id      uuid           NOT NULL,
    direction    text           NOT NULL CHECK (direction IN ('IN', 'OUT')),
    account_type text           NOT NULL,
    symbol       text           NOT NULL DEFAULT '',
    asset        text           NOT NULL,
    amount       numeric(38,18) NOT NULL CHECK (amount > 0),
    idem_key     text           NOT NULL,
    request_hash bytea          NOT NULL,
    status       text           NOT NULL CHECK (status IN ('PENDING', 'DONE', 'FAILED')),
    failure      text           NOT NULL DEFAULT '',
    created_at   timestamptz    NOT NULL,
    done_at      timestamptz,
    UNIQUE (user_id, idem_key)
);
CREATE INDEX transfers_pending ON transfers (created_at) WHERE status = 'PENDING';
CREATE INDEX transfers_user ON transfers (user_id, created_at DESC);

-- Results of the reconciliation runs (loans against the ledger's debts,
-- pools against the loans).
CREATE TABLE reconciliation_runs (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    started_at  timestamptz NOT NULL,
    finished_at timestamptz NOT NULL DEFAULT now(),
    check_name  text        NOT NULL,
    mismatches  int         NOT NULL,
    details     jsonb       NOT NULL DEFAULT '[]'
);
CREATE INDEX reconciliation_runs_started ON reconciliation_runs (started_at);

-- +goose Down
DROP TABLE reconciliation_runs;
DROP TABLE transfers;
DROP TABLE interest_runs;
DROP TABLE interest_charges;
DROP TABLE repays;
DROP TABLE borrows;
DROP TABLE loans;
DROP TABLE accounts;
DROP TABLE hourly_rates;
DROP TABLE pools;
DROP TABLE cross_terms;
DROP TABLE pair_terms;
DROP TABLE asset_terms;
