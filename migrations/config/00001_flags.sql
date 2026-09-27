-- Feature flags (ADR-0005, requirements §5.14) in the shared config
-- schema. Services only read them, through internal/platform/flags;
-- changes go through exchangectl, which writes flag_changes in the same
-- transaction. A missing flag is off.

-- +goose Up
CREATE TABLE flags (
    key         text        PRIMARY KEY,
    -- Master switch: false turns the flag off everywhere.
    enabled     boolean     NOT NULL DEFAULT false,
    -- Dimension constraints, e.g. {"regions":{"deny":["KP"]},"users":{"allow":["..."]}}.
    rules       jsonb       NOT NULL DEFAULT '{}',
    description text        NOT NULL DEFAULT '',
    version     bigint      NOT NULL DEFAULT 1,
    updated_by  text        NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Append-only history of every change.
CREATE TABLE flag_changes (
    id         bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    key        text        NOT NULL,
    old_value  jsonb,
    new_value  jsonb       NOT NULL,
    changed_by text        NOT NULL,
    reason     text        NOT NULL,
    changed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX flag_changes_key ON flag_changes (key, changed_at);

-- +goose Down
DROP TABLE flag_changes;
DROP TABLE flags;
