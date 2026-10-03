-- A custodian's deposit to an address no user has (B7a of the real
-- gateway's integration, 2026-10-03): booked to UNCLAIMED_DEPOSIT as a
-- deposit of nobody (user_id the nil UUID, wallet's domain.NoOwner),
-- reason UNKNOWN_ADDRESS, until an administrator credits it to a user or
-- dismisses it.

-- +goose Up
ALTER TABLE deposits DROP CONSTRAINT deposits_reason_check;
ALTER TABLE deposits ADD CONSTRAINT deposits_reason_check
    CHECK (reason IN ('BELOW_MINIMUM', 'ACCOUNT_CLOSED', 'NOT_ELIGIBLE', 'UNSUPPORTED_TOKEN', 'UNKNOWN_ADDRESS'));

-- +goose Down
-- Deposits of nobody stay; the old check is not applied to them.
ALTER TABLE deposits DROP CONSTRAINT deposits_reason_check;
ALTER TABLE deposits ADD CONSTRAINT deposits_reason_check
    CHECK (reason IN ('BELOW_MINIMUM', 'ACCOUNT_CLOSED', 'NOT_ELIGIBLE', 'UNSUPPORTED_TOKEN')) NOT VALID;
