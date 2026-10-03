-- Fund operations that may have booked (admin console C5.5 ⑥): attempted_at
-- is when an attempt to carry one out began. Until its outcome is recorded
-- the ledger may have booked it, so a pending operation with it set is
-- finished (its idempotency key approval:<id> makes that safe), never
-- rejected. The administrators' Idempotency-Keys of the requests that move
-- money live in the platform's idempotency_keys table.

-- +goose Up
ALTER TABLE approvals ADD COLUMN attempted_at timestamptz;
-- A single-person operation still pending was carried out when requested
-- and did not finish: attempted then.
UPDATE approvals SET attempted_at = created_at WHERE status = 'PENDING' AND mode = 'SINGLE';

-- +goose Down
ALTER TABLE approvals DROP COLUMN attempted_at;
