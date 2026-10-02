-- Changes of trading parameters wait (design 2026-10-02 §2 item 6): a
-- pair's or contract's status other than a halt, fee rates, risk ladders
-- and reference symbols take effect change_delay_seconds after an ADMIN
-- confirmed what the preview showed, and while admin.two_person_approval
-- is on only once a second ADMIN approved them; any ADMIN may cancel one
-- before. admin-service applies the due ones.

-- +goose Up
ALTER TABLE settings ADD COLUMN change_delay_seconds integer NOT NULL DEFAULT 300 CHECK (change_delay_seconds BETWEEN 60 AND 86400);

CREATE TABLE instrument_changes (
    id           uuid        PRIMARY KEY,
    kind         text        NOT NULL CHECK (kind IN ('CONFIG', 'PAIR_STATUS', 'CONTRACT_STATUS')),
    target       text        NOT NULL,
    payload      jsonb       NOT NULL,
    summary      jsonb       NOT NULL,
    reason       text        NOT NULL,
    status       text        NOT NULL CHECK (status IN ('PENDING_APPROVAL', 'SCHEDULED', 'APPLIED', 'CANCELED', 'REJECTED', 'FAILED')),
    requested_by uuid        NOT NULL REFERENCES admins (id),
    approved_by  uuid        REFERENCES admins (id),
    approved_at  timestamptz,
    closed_by    uuid        REFERENCES admins (id),
    closed_at    timestamptz,
    effective_at timestamptz,
    applied_at   timestamptz,
    result       text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL,
    CHECK (approved_by IS NULL OR approved_by <> requested_by),
    CHECK (status NOT IN ('SCHEDULED', 'APPLIED', 'FAILED') OR effective_at IS NOT NULL),
    CHECK (status NOT IN ('CANCELED', 'REJECTED') OR closed_by IS NOT NULL)
);
CREATE INDEX instrument_changes_due_idx ON instrument_changes (effective_at) WHERE status = 'SCHEDULED';
CREATE INDEX instrument_changes_list_idx ON instrument_changes (created_at DESC, id DESC);

-- +goose Down
DROP TABLE instrument_changes;
ALTER TABLE settings DROP COLUMN change_delay_seconds;
