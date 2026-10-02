-- Where each change of the reference data came from (admin console design
-- 2026-10-02 C3): FILE for exchangectl instruments apply (the deploy's sync
-- of deploy/instruments/<env>.json), CONSOLE for the admin console's
-- edits, STATUS for status changes and PROFILE for asset profiles; '' for
-- the rows written before, read as FILE. A file apply keeps an item the
-- console changed last (unless forced), so console edits survive deploys.

-- +goose Up
ALTER TABLE config_history ADD COLUMN source text NOT NULL DEFAULT ''
    CHECK (source IN ('', 'FILE', 'CONSOLE', 'STATUS', 'PROFILE'));
-- The last edit of an item (status and profile changes left out).
CREATE INDEX config_history_edits_idx ON config_history (entity, key, version DESC) WHERE source IN ('', 'FILE', 'CONSOLE');

-- +goose Down
DROP INDEX config_history_edits_idx;
ALTER TABLE config_history DROP COLUMN source;
