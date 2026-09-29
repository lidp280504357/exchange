-- Contributions to the contracts' insurance fund take two administrators
-- too (implementation plan §7.3 task 10): approvals of kind
-- INSURANCE_FUND, booked with ledger FundInsurance.

-- +goose Up
ALTER TABLE approvals DROP CONSTRAINT approvals_kind_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_kind_check CHECK (kind IN ('LEDGER_ADJUSTMENT', 'INSURANCE_FUND'));

-- +goose Down
DELETE FROM approvals WHERE kind = 'INSURANCE_FUND';
ALTER TABLE approvals DROP CONSTRAINT approvals_kind_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_kind_check CHECK (kind IN ('LEDGER_ADJUSTMENT'));
