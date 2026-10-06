-- HOUSE's caps changed from the console (user 2026-10-07, A69): a change
-- of market-maker's runtime caps (review C45) waits in approvals for a
-- second administrator, kind HOUSE_CAPS.

-- +goose Up
ALTER TABLE approvals DROP CONSTRAINT approvals_kind_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_kind_check
    CHECK (kind IN ('LEDGER_ADJUSTMENT', 'INSURANCE_FUND', 'DEPOSIT_BACKFILL', 'SIM_EVENT', 'SIM_PARAMS', 'SIM_MINT', 'DEPOSIT_ASSIGN',
                    'WELCOME_CREDIT', 'MARGIN_PARAMS', 'MARGIN_LIQUIDATE', 'HOUSE_CAPS'));

-- +goose Down
-- Loses data: the caps requests go (their audit events stay).
DELETE FROM approvals WHERE kind = 'HOUSE_CAPS';
ALTER TABLE approvals DROP CONSTRAINT approvals_kind_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_kind_check
    CHECK (kind IN ('LEDGER_ADJUSTMENT', 'INSURANCE_FUND', 'DEPOSIT_BACKFILL', 'SIM_EVENT', 'SIM_PARAMS', 'SIM_MINT', 'DEPOSIT_ASSIGN',
                    'WELCOME_CREDIT', 'MARGIN_PARAMS', 'MARGIN_LIQUIDATE'));
