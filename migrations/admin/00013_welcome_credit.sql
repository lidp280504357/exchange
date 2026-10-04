-- Raising the welcome credits (what a new account gets, the ledger's
-- setting; design 2026-10-04 §4.2, D2) waits for a second ADMIN: a
-- request of kind WELCOME_CREDIT in the approvals, executed by setting the
-- ledger's credits when approved.

-- +goose Up
ALTER TABLE approvals DROP CONSTRAINT approvals_kind_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_kind_check
    CHECK (kind IN ('LEDGER_ADJUSTMENT', 'INSURANCE_FUND', 'DEPOSIT_BACKFILL', 'SIM_EVENT', 'SIM_PARAMS', 'SIM_MINT', 'DEPOSIT_ASSIGN',
                    'WELCOME_CREDIT'));

-- +goose Down
-- Loses data: the WELCOME_CREDIT requests go (their audit events stay).
DELETE FROM approvals WHERE kind = 'WELCOME_CREDIT';
ALTER TABLE approvals DROP CONSTRAINT approvals_kind_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_kind_check
    CHECK (kind IN ('LEDGER_ADJUSTMENT', 'INSURANCE_FUND', 'DEPOSIT_BACKFILL', 'SIM_EVENT', 'SIM_PARAMS', 'SIM_MINT', 'DEPOSIT_ASSIGN'));
