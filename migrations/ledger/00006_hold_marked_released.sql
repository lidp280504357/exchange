-- C5.5 ㉒: when everything a hold froze went elsewhere, the operators'
-- forced release (exchangectl ledger release-hold) releases nothing and
-- only marks the hold released, without a journal; a release journal
-- still belongs to a released hold.

-- +goose Up
ALTER TABLE holds
    DROP CONSTRAINT holds_check,
    ADD CONSTRAINT holds_check CHECK (released_at IS NOT NULL OR release_journal_id IS NULL);

-- +goose Down
-- Fails while a hold is marked released without a journal.
ALTER TABLE holds
    DROP CONSTRAINT holds_check,
    ADD CONSTRAINT holds_check CHECK ((released_at IS NULL) = (release_journal_id IS NULL));
