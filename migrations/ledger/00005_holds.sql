-- Administrators' holds on part of a user's SPOT balance (design
-- 2026-10-02 §4.1, risk control): ADMIN_FREEZE moves the amount from
-- available to frozen, the release moves it back with ADMIN_UNFREEZE.

-- +goose Up
ALTER TABLE journals DROP CONSTRAINT journals_entry_type_check;
ALTER TABLE journals ADD CONSTRAINT journals_entry_type_check CHECK (entry_type IN ('DEPOSIT_CREDIT', 'WITHDRAW_FREEZE',
    'WITHDRAW_SETTLE', 'WITHDRAW_UNFREEZE', 'ORDER_FREEZE', 'ORDER_UNFREEZE', 'TRADE_SETTLE', 'TRADE_FEE', 'ACCOUNT_TRANSFER',
    'FUNDING_PAYMENT', 'REALIZED_PNL', 'LIQUIDATION_SETTLE', 'INSURANCE_CONTRIBUTION', 'ADL_SETTLE', 'INTERNAL_TRANSFER',
    'MANUAL_ADJUSTMENT', 'HOUSE_TRADE_SETTLE', 'ADMIN_FREEZE', 'ADMIN_UNFREEZE'));

CREATE TABLE holds (
    id                 uuid           PRIMARY KEY,
    user_id            uuid           NOT NULL,
    account_type       text           NOT NULL DEFAULT 'SPOT' CHECK (account_type = 'SPOT'),
    asset              text           NOT NULL,
    amount             numeric(38,18) NOT NULL CHECK (amount > 0),
    reason             text           NOT NULL,
    -- The administrator's email.
    actor              text           NOT NULL,
    journal_id         uuid           NOT NULL REFERENCES journals (id),
    created_at         timestamptz    NOT NULL,
    released_at        timestamptz,
    released_by        text,
    release_reason     text,
    release_journal_id uuid           REFERENCES journals (id),
    CHECK ((released_at IS NULL) = (release_journal_id IS NULL))
);
CREATE INDEX holds_user ON holds (user_id, created_at DESC);

-- +goose Down
DROP TABLE holds;
ALTER TABLE journals DROP CONSTRAINT journals_entry_type_check;
ALTER TABLE journals ADD CONSTRAINT journals_entry_type_check CHECK (entry_type IN ('DEPOSIT_CREDIT', 'WITHDRAW_FREEZE',
    'WITHDRAW_SETTLE', 'WITHDRAW_UNFREEZE', 'ORDER_FREEZE', 'ORDER_UNFREEZE', 'TRADE_SETTLE', 'TRADE_FEE', 'ACCOUNT_TRANSFER',
    'FUNDING_PAYMENT', 'REALIZED_PNL', 'LIQUIDATION_SETTLE', 'INSURANCE_CONTRIBUTION', 'ADL_SETTLE', 'INTERNAL_TRANSFER',
    'MANUAL_ADJUSTMENT', 'HOUSE_TRADE_SETTLE'));
