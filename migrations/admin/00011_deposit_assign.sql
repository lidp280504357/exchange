-- A deposit of nobody (B7a: a custodian's deposit to an address no user
-- has, booked to UNCLAIMED_DEPOSIT) is credited to the user an
-- administrator names: a fund operation of kind DEPOSIT_ASSIGN, alone
-- within the single-person limits, else with a second administrator
-- (C5.5 ㉑).

-- +goose Up
ALTER TABLE approvals DROP CONSTRAINT approvals_kind_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_kind_check
    CHECK (kind IN ('LEDGER_ADJUSTMENT', 'INSURANCE_FUND', 'DEPOSIT_BACKFILL', 'SIM_EVENT', 'SIM_PARAMS', 'SIM_MINT', 'DEPOSIT_ASSIGN'));

-- +goose Down
-- Loses data: the DEPOSIT_ASSIGN requests go (their audit events stay).
DELETE FROM approvals WHERE kind = 'DEPOSIT_ASSIGN';
ALTER TABLE approvals DROP CONSTRAINT approvals_kind_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_kind_check
    CHECK (kind IN ('LEDGER_ADJUSTMENT', 'INSURANCE_FUND', 'DEPOSIT_BACKFILL', 'SIM_EVENT', 'SIM_PARAMS', 'SIM_MINT'));
