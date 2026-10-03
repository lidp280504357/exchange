-- The C4b review (C5.5 ⑫): a broadcast whose round fails waits longer
-- each time and, after ten failures in a row, is FAILED until an operator
-- resumes it, while the others go on; a broadcast's mails wait in
-- deliveries (next_attempt_at) for a worker that sends them at the
-- providers' pace and tries a failed one again later; old notices,
-- broadcasts and delivery records are deleted after their keep.

-- +goose Up
ALTER TABLE broadcasts
    ADD COLUMN failures integer NOT NULL DEFAULT 0 CHECK (failures >= 0),
    ADD COLUMN last_error text NOT NULL DEFAULT '',
    ADD COLUMN retry_at timestamptz,
    DROP CONSTRAINT broadcasts_status_check,
    ADD CONSTRAINT broadcasts_status_check CHECK (status IN ('SENDING', 'SENT', 'FAILED'));
CREATE INDEX broadcasts_created ON broadcasts (created_at);
-- Set while a queued delivery waits for its next attempt; NULL for the
-- deliveries sent at once and for those done.
ALTER TABLE deliveries ADD COLUMN next_attempt_at timestamptz;
CREATE INDEX deliveries_due ON deliveries (next_attempt_at) WHERE next_attempt_at IS NOT NULL;
CREATE INDEX notifications_created ON notifications (created_at);

-- +goose Down
DROP INDEX notifications_created;
DROP INDEX deliveries_due;
ALTER TABLE deliveries DROP COLUMN next_attempt_at;
DROP INDEX broadcasts_created;
UPDATE broadcasts SET status = 'SENDING' WHERE status = 'FAILED';
ALTER TABLE broadcasts
    DROP CONSTRAINT broadcasts_status_check,
    ADD CONSTRAINT broadcasts_status_check CHECK (status IN ('SENDING', 'SENT')),
    DROP COLUMN retry_at,
    DROP COLUMN last_error,
    DROP COLUMN failures;
