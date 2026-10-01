-- The admin console decides identity rebind requests (design 2026-10-02
-- §4.1): who decided and why are kept with the decision.

-- +goose Up
ALTER TABLE identity_rebind_requests
    ADD COLUMN decided_by      text,
    ADD COLUMN decision_reason text;
CREATE INDEX identity_rebind_requests_pending ON identity_rebind_requests (created_at) WHERE status = 'PENDING_REVIEW';

-- +goose Down
DROP INDEX identity_rebind_requests_pending;
ALTER TABLE identity_rebind_requests DROP COLUMN decision_reason, DROP COLUMN decided_by;
