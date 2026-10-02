-- The target and the last price every 10 seconds, a day of them, for the
-- operators' chart (ASTRA design §6.1): kept here so that a restart (every
-- deploy) does not wipe the chart.

-- +goose Up
CREATE TABLE samples (
    at     timestamptz PRIMARY KEY,
    target double precision NOT NULL,
    last   numeric(38,18) NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE samples;
