-- Margin trading's two-person requests (margin design 2026-10-06 §8, E5):
-- MARGIN_PARAMS changes margin-service's terms as of the version read,
-- MARGIN_LIQUIDATE liquidates an account by hand.

-- +goose Up
ALTER TABLE approvals DROP CONSTRAINT approvals_kind_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_kind_check
    CHECK (kind IN ('LEDGER_ADJUSTMENT', 'INSURANCE_FUND', 'DEPOSIT_BACKFILL', 'SIM_EVENT', 'SIM_PARAMS', 'SIM_MINT', 'DEPOSIT_ASSIGN',
                    'WELCOME_CREDIT', 'MARGIN_PARAMS', 'MARGIN_LIQUIDATE'));

-- +goose Down
-- Loses data: the margin requests go (their audit events stay).
DELETE FROM approvals WHERE kind IN ('MARGIN_PARAMS', 'MARGIN_LIQUIDATE');
ALTER TABLE approvals DROP CONSTRAINT approvals_kind_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_kind_check
    CHECK (kind IN ('LEDGER_ADJUSTMENT', 'INSURANCE_FUND', 'DEPOSIT_BACKFILL', 'SIM_EVENT', 'SIM_PARAMS', 'SIM_MINT', 'DEPOSIT_ASSIGN',
                    'WELCOME_CREDIT'));
