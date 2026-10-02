-- The admin console's handling of deposits and withdrawals (design
-- 2026-10-02 §4.2, §4.3):
-- * a deposit an administrator backfilled because the custodian's callback
--   was lost (source MANUAL, who entered it), when the custodian's own
--   callback later matched it, or what in it disagreed (a discrepancy);
-- * the decision on a deposit that needs one (unclaimed funds released to
--   the user: CREDITED; or dismissed), with who, when, why and the
--   release's journal; a released unclaimed deposit is CREDITED;
-- * a withdrawal in review put on hold with a note;
-- * the callback result DISCREPANCY.

-- +goose Up
ALTER TABLE deposits
    ADD COLUMN source             text NOT NULL DEFAULT '' CHECK (source IN ('', 'MANUAL')),
    ADD COLUMN entered_by         text NOT NULL DEFAULT '',
    ADD COLUMN callback_at        timestamptz,
    ADD COLUMN discrepancy        text NOT NULL DEFAULT '',
    ADD COLUMN resolution         text NOT NULL DEFAULT '' CHECK (resolution IN ('', 'CREDITED', 'DISMISSED')),
    ADD COLUMN resolved_by        text NOT NULL DEFAULT '',
    ADD COLUMN resolved_at        timestamptz,
    ADD COLUMN resolution_note    text NOT NULL DEFAULT '',
    ADD COLUMN release_journal_id uuid,
    ADD CONSTRAINT deposits_manual_check CHECK (source <> 'MANUAL' OR (provider_tx_id IS NOT NULL AND entered_by <> '')),
    ADD CONSTRAINT deposits_release_check CHECK ((resolution = 'CREDITED') = (release_journal_id IS NOT NULL)),
    DROP CONSTRAINT deposits_check2,
    ADD CONSTRAINT deposits_check2 CHECK (status <> 'CREDITED' OR (journal_id IS NOT NULL AND (NOT unclaimed OR resolution = 'CREDITED')));
CREATE INDEX deposits_attention_idx ON deposits (detected_at) WHERE resolution = '' AND (status = 'REJECTED' OR discrepancy <> '');
CREATE INDEX deposits_manual_idx ON deposits (detected_at) WHERE source = 'MANUAL';

ALTER TABLE withdrawals
    ADD COLUMN held_at   timestamptz,
    ADD COLUMN held_by   text NOT NULL DEFAULT '',
    ADD COLUMN hold_note text NOT NULL DEFAULT '';

ALTER TABLE custody_callbacks
    DROP CONSTRAINT custody_callbacks_result_check,
    ADD CONSTRAINT custody_callbacks_result_check
        CHECK (result IN ('RECEIVED', 'APPLIED', 'IGNORED', 'UNMATCHED', 'REJECTED', 'FAILED', 'DISCREPANCY'));

-- +goose Down
ALTER TABLE custody_callbacks
    DROP CONSTRAINT custody_callbacks_result_check,
    ADD CONSTRAINT custody_callbacks_result_check
        CHECK (result IN ('RECEIVED', 'APPLIED', 'IGNORED', 'UNMATCHED', 'REJECTED', 'FAILED'));
ALTER TABLE withdrawals DROP COLUMN hold_note, DROP COLUMN held_by, DROP COLUMN held_at;
DROP INDEX deposits_manual_idx;
DROP INDEX deposits_attention_idx;
ALTER TABLE deposits
    DROP CONSTRAINT deposits_check2,
    ADD CONSTRAINT deposits_check2 CHECK (status <> 'CREDITED' OR (journal_id IS NOT NULL AND NOT unclaimed)),
    DROP CONSTRAINT deposits_release_check,
    DROP CONSTRAINT deposits_manual_check,
    DROP COLUMN release_journal_id,
    DROP COLUMN resolution_note,
    DROP COLUMN resolved_at,
    DROP COLUMN resolved_by,
    DROP COLUMN resolution,
    DROP COLUMN discrepancy,
    DROP COLUMN callback_at,
    DROP COLUMN entered_by,
    DROP COLUMN source;
