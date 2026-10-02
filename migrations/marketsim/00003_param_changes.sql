-- The changes of the settings as the guards count them (ASTRA design §6.2,
-- clarified after the A3 review): a change of P0, of the floor and ceiling,
-- of max_minute_move or of the day's turnover takes from the same hourly
-- budget as the operators' events, and beyond one operator's share needs a
-- second one, recorded here with the change.

-- +goose Up
CREATE TABLE param_changes (
    version     bigint      PRIMARY KEY,
    at          timestamptz NOT NULL,
    actor       text        NOT NULL,
    approved_by text        NOT NULL DEFAULT '',
    -- How far the change moves the price (a share) and the day's taker
    -- turnover (a logarithm).
    move        double precision NOT NULL DEFAULT 0,
    volume      double precision NOT NULL DEFAULT 0
);
CREATE INDEX param_changes_at_idx ON param_changes (at);
CREATE INDEX events_starts_idx ON events (starts_at);

-- +goose Down
DROP INDEX events_starts_idx;
DROP TABLE param_changes;
