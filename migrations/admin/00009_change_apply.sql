-- Applying a change of trading parameters happens outside the console's
-- transaction (C5.5 ⑩): applying_at marks one claimed by an apply round
-- until it is recorded, so a crash or a failed record between the apply and
-- its record is found again and checked (in effect already, or still to
-- do), and nobody cancels it meanwhile. A preview's confirmation confirms
-- one change: its hash is kept with it, and a second use of the same token
-- answers with that change.

-- +goose Up
ALTER TABLE instrument_changes ADD COLUMN applying_at timestamptz, ADD COLUMN confirmation_hash text;
CREATE UNIQUE INDEX instrument_changes_confirmation_idx ON instrument_changes (confirmation_hash) WHERE confirmation_hash IS NOT NULL;

-- +goose Down
DROP INDEX instrument_changes_confirmation_idx;
ALTER TABLE instrument_changes DROP COLUMN confirmation_hash, DROP COLUMN applying_at;
