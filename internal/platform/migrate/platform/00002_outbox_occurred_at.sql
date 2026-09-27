-- occurred_at of the envelope, truncated to milliseconds like ClickHouse's
-- DateTime64(3), so that daily reconciliation compares exact windows.

-- +goose Up
ALTER TABLE outbox ADD COLUMN occurred_at timestamptz;
CREATE INDEX outbox_occurred_at ON outbox (occurred_at);

-- +goose Down
DROP INDEX outbox_occurred_at;
ALTER TABLE outbox DROP COLUMN occurred_at;
