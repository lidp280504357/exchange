-- A simulated market's price event or settings beyond one operator's share
-- (ASTRA design §6.2) wait in approvals for a second administrator, whose
-- approval admin-service passes to market-sim as approved_by: kinds
-- SIM_EVENT and SIM_PARAMS. More coin or USDT for its bots (§4: the pool
-- grows by manual adjustments) is a fund operation of kind SIM_MINT.

-- +goose Up
ALTER TABLE approvals DROP CONSTRAINT approvals_kind_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_kind_check
    CHECK (kind IN ('LEDGER_ADJUSTMENT', 'INSURANCE_FUND', 'DEPOSIT_BACKFILL', 'SIM_EVENT', 'SIM_PARAMS', 'SIM_MINT'));

-- +goose Down
DELETE FROM approvals WHERE kind IN ('SIM_EVENT', 'SIM_PARAMS', 'SIM_MINT');
ALTER TABLE approvals DROP CONSTRAINT approvals_kind_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_kind_check CHECK (kind IN ('LEDGER_ADJUSTMENT', 'INSURANCE_FUND', 'DEPOSIT_BACKFILL'));
