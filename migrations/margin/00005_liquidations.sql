-- Liquidations (margin design 2026-10-06 §4.5, batch E3): an account at
-- its liquidation level twice in a row (AUTO), or an administrator's
-- approved request (MANUAL), is frozen, its orders canceled, what it holds
-- beyond its debts sold to HOUSE and what it lacks bought, its debts
-- repaid, the rest covered by the insurance fund, the fee charged, and
-- the account freed with what is left. The process moves step by step
-- and resumes where it stopped after a restart.

-- +goose Up
CREATE TABLE liquidations (
    liquidation_id    uuid           PRIMARY KEY,
    user_id           uuid           NOT NULL,
    account_type      text           NOT NULL,
    symbol            text           NOT NULL DEFAULT '',
    trigger           text           NOT NULL CHECK (trigger IN ('AUTO', 'MANUAL')),
    -- The administrators' approval (MANUAL) and who asked.
    approval_id       uuid,
    requested_by      text           NOT NULL DEFAULT '',
    status            text           NOT NULL CHECK (status IN ('STARTED', 'COMPLETED')),
    step              text           NOT NULL CHECK (step IN ('CANCEL', 'SELL', 'BUY', 'REPAY', 'COVER', 'FEE', 'SETTLE', 'DONE')),
    -- The account's status to return to: FROZEN when an administrator had
    -- frozen it, else NORMAL.
    prior_status      text           NOT NULL,
    -- The account when it started, in USDT.
    margin_level      numeric(20,8),
    total_asset       numeric(38,8)  NOT NULL,
    total_liability   numeric(38,8)  NOT NULL,
    fee_rate          numeric(10,6)  NOT NULL,
    -- The quote asset the orders trade against (USDT on the cross
    -- account, the pair's quote on an isolated one), its free balance when
    -- the current trading step began, and what the orders traded of it.
    quote_asset       text           NOT NULL,
    quote_mark        numeric(38,18) NOT NULL DEFAULT 0,
    traded            numeric(38,18) NOT NULL DEFAULT 0,
    -- In the quote asset, set before it is booked; in USDT: what the
    -- insurance fund paid.
    fee               numeric(38,18) NOT NULL DEFAULT 0,
    fee_usdt          numeric(38,8)  NOT NULL DEFAULT 0,
    insurance_covered numeric(38,8)  NOT NULL DEFAULT 0,
    -- [{asset, amount}]: the debts repaid (by the account and the fund),
    -- what stayed in the account.
    repaid            jsonb          NOT NULL DEFAULT '[]',
    remaining         jsonb          NOT NULL DEFAULT '[]',
    -- Why the current step waits, for operators.
    note              text           NOT NULL DEFAULT '',
    started_at        timestamptz    NOT NULL,
    step_at           timestamptz    NOT NULL,
    completed_at      timestamptz,
    FOREIGN KEY (user_id, account_type, symbol) REFERENCES accounts (user_id, account_type, symbol)
);
CREATE UNIQUE INDEX liquidations_running ON liquidations (user_id, account_type, symbol) WHERE status = 'STARTED';
CREATE INDEX liquidations_account ON liquidations (user_id, account_type, symbol, started_at DESC);
CREATE INDEX liquidations_user ON liquidations (user_id, liquidation_id DESC);
CREATE UNIQUE INDEX liquidations_approval ON liquidations (approval_id) WHERE approval_id IS NOT NULL;

-- A liquidation's market orders against HOUSE (spot-trading-service
-- POST /internal/orders/liquidations, idempotent by liquidation, pair and
-- side): stored before they are sent, sent again until accepted.
CREATE TABLE liquidation_orders (
    liquidation_id uuid           NOT NULL REFERENCES liquidations (liquidation_id),
    symbol         text           NOT NULL,
    side           text           NOT NULL CHECK (side IN ('BUY', 'SELL')),
    -- A sell's quantity of the base, a buy's amount of the quote.
    quantity       numeric(38,18) NOT NULL DEFAULT 0,
    quote_amount   numeric(38,18) NOT NULL DEFAULT 0,
    order_id       uuid,
    -- PLANNED until the trading service took it (SENT) or refused it
    -- (REFUSED, with its error).
    status         text           NOT NULL CHECK (status IN ('PLANNED', 'SENT', 'REFUSED')),
    error          text           NOT NULL DEFAULT '',
    created_at     timestamptz    NOT NULL,
    PRIMARY KEY (liquidation_id, symbol, side)
);

-- A repayment the insurance fund made for a liquidation (its debts beyond
-- what the account held).
ALTER TABLE repays ADD COLUMN covered_by text NOT NULL DEFAULT '' CHECK (covered_by IN ('', 'INSURANCE_FUND'));

-- +goose Down
ALTER TABLE repays DROP COLUMN covered_by;
DROP TABLE liquidation_orders;
DROP TABLE liquidations;
