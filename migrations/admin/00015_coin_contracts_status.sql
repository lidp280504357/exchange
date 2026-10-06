-- A coin's contracts closed or reopened at once (coin-margined design
-- 2026-10-06 §3.5, A63): one change of kind COIN_CONTRACTS_STATUS (target
-- coin:<coin>) moves the coin's USDⓈ-M and COIN-M perpetuals together.

-- +goose Up
ALTER TABLE instrument_changes DROP CONSTRAINT instrument_changes_kind_check;
ALTER TABLE instrument_changes ADD CONSTRAINT instrument_changes_kind_check
    CHECK (kind IN ('CONFIG', 'PAIR_STATUS', 'CONTRACT_STATUS', 'COIN_CONTRACTS_STATUS'));

-- +goose Down
-- Loses data: the coins' changes go (their audit events stay).
DELETE FROM instrument_changes WHERE kind = 'COIN_CONTRACTS_STATUS';
ALTER TABLE instrument_changes DROP CONSTRAINT instrument_changes_kind_check;
ALTER TABLE instrument_changes ADD CONSTRAINT instrument_changes_kind_check
    CHECK (kind IN ('CONFIG', 'PAIR_STATUS', 'CONTRACT_STATUS'));
