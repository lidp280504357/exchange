-- HOUSE's virtual liquidity (ADR-0015): the engine applies reference books
-- from order.references besides the commands of order.commands. WAL
-- entries are numbered per partition in the order they were applied
-- (seq) and keep the topic and offset they came from; snapshots cover a
-- WAL position and record how far each topic was applied.

-- +goose Up
ALTER TABLE wal ADD COLUMN seq BIGINT;
ALTER TABLE wal ADD COLUMN source TEXT NOT NULL DEFAULT 'commands' CHECK (source IN ('commands', 'references'));
-- Until now the offsets of order.commands numbered the WAL.
UPDATE wal SET seq = "offset";
ALTER TABLE wal ALTER COLUMN seq SET NOT NULL;
ALTER TABLE wal DROP CONSTRAINT wal_pkey;
ALTER TABLE wal ADD PRIMARY KEY (partition, seq);
-- A redelivered command or book is never applied twice.
CREATE UNIQUE INDEX wal_source_offset ON wal (partition, source, "offset");

ALTER TABLE snapshots ADD COLUMN seq BIGINT;
UPDATE snapshots SET seq = "offset";
ALTER TABLE snapshots ALTER COLUMN seq SET NOT NULL;
-- The last reference book the snapshot covers; -1 for none.
ALTER TABLE snapshots ADD COLUMN ref_offset BIGINT NOT NULL DEFAULT -1;

-- +goose Down
ALTER TABLE snapshots DROP COLUMN ref_offset;
ALTER TABLE snapshots DROP COLUMN seq;
DELETE FROM wal WHERE source <> 'commands';
DROP INDEX wal_source_offset;
ALTER TABLE wal DROP CONSTRAINT wal_pkey;
ALTER TABLE wal ADD PRIMARY KEY (partition, "offset");
ALTER TABLE wal DROP COLUMN source;
ALTER TABLE wal DROP COLUMN seq;
