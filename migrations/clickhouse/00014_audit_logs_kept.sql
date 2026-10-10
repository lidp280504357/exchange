-- The audit log is the console's only record of who did what and is
-- small: it is kept whatever its age (the coordinator, review LK; 00013
-- had given it 15 days).

-- +goose NO TRANSACTION
-- +goose Up
ALTER TABLE audit_logs REMOVE TTL;

-- +goose Down
ALTER TABLE audit_logs MODIFY TTL toDateTime(occurred_at) + INTERVAL 15 DAY;
