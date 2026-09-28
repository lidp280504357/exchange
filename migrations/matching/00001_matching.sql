-- matching-engine (requirements §5.7, ADR-0002): the write-ahead log of
-- applied commands and snapshots of the books. Its events leave through
-- the platform outbox of this schema.

-- +goose Up
-- One row per command applied, keyed by its place in order.commands.
CREATE TABLE wal (
    partition  INT         NOT NULL,
    "offset"   BIGINT      NOT NULL,
    symbol     TEXT        NOT NULL,
    command    BYTEA       NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (partition, "offset")
);
CREATE INDEX wal_applied_at_idx ON wal (applied_at);

-- The books of a partition after the command at "offset".
CREATE TABLE snapshots (
    partition INT         PRIMARY KEY,
    "offset"  BIGINT      NOT NULL,
    books     JSONB       NOT NULL,
    taken_at  TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE snapshots;
DROP TABLE wal;
