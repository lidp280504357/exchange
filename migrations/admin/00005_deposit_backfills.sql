-- A backfill of a custodian deposit whose callback was lost goes through
-- the fund operations' approvals too (design 2026-10-02 §4.3): kind
-- DEPOSIT_BACKFILL.

-- +goose Up
ALTER TABLE approvals DROP CONSTRAINT approvals_kind_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_kind_check CHECK (kind IN ('LEDGER_ADJUSTMENT', 'INSURANCE_FUND', 'DEPOSIT_BACKFILL'));

-- +goose Down
DELETE FROM approvals WHERE kind = 'DEPOSIT_BACKFILL';
ALTER TABLE approvals DROP CONSTRAINT approvals_kind_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_kind_check CHECK (kind IN ('LEDGER_ADJUSTMENT', 'INSURANCE_FUND'));
