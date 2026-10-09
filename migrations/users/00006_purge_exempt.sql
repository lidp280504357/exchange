-- Accounts the test-account purge leaves alone (L4, the coordinator's
-- contract 2026-10-10 04:25): the end-to-end scripts' standing accounts,
-- funding.sh's hedges, which hold positions from run to run. Set and
-- lifted with exchangectl users exempt (audited user.purge_exempt).

-- +goose Up
ALTER TABLE users ADD COLUMN purge_exempt boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE users DROP COLUMN purge_exempt;
