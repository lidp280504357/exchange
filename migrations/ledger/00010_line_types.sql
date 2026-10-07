-- A user's ledger filtered by type (GET /v1/account/ledger?type=, B141):
-- journal_line_types lists each line under its account and its journal's
-- entry type, so a filtered page reads only that type's lines of each
-- account, newest first, on its key. Before, a type the account seldom
-- has walked every line of it (4.5 s for a busy market-sim bot on the
-- test server; 30 ms this way). A trigger adds a line's row in the same
-- transaction, the type read from its journal, so the two never differ;
-- like the lines, the rows are append-only. A side table rather than a
-- column on journal_lines: filling a column would rewrite every line
-- (measured at about 5 minutes for the test server's 3.5 million, with the
-- append-only trigger off meanwhile); this fills in about 30 s (16 s to
-- copy, 14 s for the key, measured on those 3.5 million lines).

-- +goose Up
-- No line is posted while the lines before are copied.
LOCK TABLE journal_lines IN SHARE MODE;

CREATE TABLE journal_line_types (
    account_id uuid   NOT NULL,
    entry_type text   NOT NULL,
    line_id    bigint NOT NULL
);
INSERT INTO journal_line_types (account_id, entry_type, line_id)
SELECT l.account_id, j.entry_type, l.id FROM journal_lines l JOIN journals j ON j.id = l.journal_id;
ALTER TABLE journal_line_types ADD PRIMARY KEY (account_id, entry_type, line_id);

-- +goose StatementBegin
CREATE FUNCTION ledger_line_type() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO journal_line_types (account_id, entry_type, line_id)
    SELECT NEW.account_id, j.entry_type, NEW.id FROM journals j WHERE j.id = NEW.journal_id;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER journal_lines_type AFTER INSERT ON journal_lines
    FOR EACH ROW EXECUTE FUNCTION ledger_line_type();
CREATE TRIGGER journal_line_types_append_only BEFORE UPDATE OR DELETE ON journal_line_types
    FOR EACH ROW EXECUTE FUNCTION ledger_append_only();
CREATE TRIGGER journal_line_types_no_truncate BEFORE TRUNCATE ON journal_line_types
    FOR EACH STATEMENT EXECUTE FUNCTION ledger_append_only();

-- +goose Down
DROP TRIGGER journal_lines_type ON journal_lines;
DROP FUNCTION ledger_line_type();
DROP TABLE journal_line_types;
