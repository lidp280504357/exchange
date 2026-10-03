-- One administrator may carry out fund operations alone while the flag
-- admin.two_person_approval is off (design 2026-10-02 §2). Every
-- operation keeps its row in approvals: its mode, its worth in USDT when
-- requested (the 24-hour limit sums a requester's single-person ones), why
-- a two-person one waits and the journal that booked it. The requester
-- may finish a single-person operation whose outcome was unknown and
-- withdraw (reject) their own request. settings holds the single-person
-- limits (one row).

-- +goose Up
ALTER TABLE approvals
    ADD COLUMN mode       text NOT NULL DEFAULT 'TWO_PERSON' CHECK (mode IN ('TWO_PERSON', 'SINGLE')),
    ADD COLUMN value_usdt numeric,
    ADD COLUMN escalation text NOT NULL DEFAULT '',
    ADD COLUMN journal_id text NOT NULL DEFAULT '';
ALTER TABLE approvals DROP CONSTRAINT approvals_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_decider_check
    CHECK (decided_by IS NULL OR decided_by <> requested_by OR mode = 'SINGLE' OR status = 'REJECTED');
CREATE INDEX approvals_single_idx ON approvals (requested_by, created_at) WHERE mode = 'SINGLE';
-- Every request so far was asked for; the executed ones name their journal.
UPDATE approvals SET escalation = 'REQUESTED';
UPDATE approvals SET journal_id = substr(result, 9) WHERE status = 'EXECUTED' AND result LIKE 'journal %';

CREATE TABLE settings (
    id                  boolean     PRIMARY KEY DEFAULT true CHECK (id),
    single_max_usdt     numeric     NOT NULL CHECK (single_max_usdt > 0),
    daily_max_usdt      numeric     NOT NULL CHECK (daily_max_usdt >= single_max_usdt),
    withdrawal_max_usdt numeric     NOT NULL CHECK (withdrawal_max_usdt > 0),
    updated_by          text        NOT NULL,
    updated_at          timestamptz NOT NULL
);

-- +goose Down
-- Loses data: every single-person operation (and every request its
-- requester withdrew) is deleted, since the old constraint forbids a
-- request decided by its requester; their audit events stay. approvals_check
-- is the name Postgres gave the unnamed constraint of 00001 (the C1 review).
DROP TABLE settings;
DROP INDEX approvals_single_idx;
DELETE FROM approvals WHERE decided_by = requested_by;
ALTER TABLE approvals DROP CONSTRAINT approvals_decider_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_check CHECK (decided_by IS NULL OR decided_by <> requested_by);
ALTER TABLE approvals DROP COLUMN journal_id, DROP COLUMN escalation, DROP COLUMN value_usdt, DROP COLUMN mode;
