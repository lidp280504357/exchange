-- Each account's balances after the latest of its lines the retention run
-- deleted (B199, ADR-0022): SNAPSHOT_MATCHES_ACCOUNT compares an account
-- with its latest line, and once the run has deleted an idle account's
-- newer lines the latest one left can be an older journal kept for good
-- (a funding payment, a custody reset) - the check takes whichever of the
-- two is the newer. And the journals a transfer or a hold points at by
-- their ID, which the run's batches ask about for every journal they
-- take (B199).

-- +goose Up
CREATE TABLE account_snapshots (
    account_id      uuid           PRIMARY KEY,
    available       numeric(38,18) NOT NULL,
    frozen          numeric(38,18) NOT NULL,
    account_version bigint         NOT NULL,
    updated_at      timestamptz    NOT NULL DEFAULT now()
);
CREATE INDEX transfers_journal ON transfers (journal_id);
CREATE INDEX holds_journal ON holds (journal_id);
CREATE INDEX holds_release_journal ON holds (release_journal_id);

-- +goose Down
DROP INDEX holds_release_journal;
DROP INDEX holds_journal;
DROP INDEX transfers_journal;
DROP TABLE account_snapshots;
