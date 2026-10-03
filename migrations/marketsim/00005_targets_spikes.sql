-- Threshold targets and spikes (ASTRA design §3, §6.2, 2026-10-04): a
-- target takes the price above or below a level by the end of its window
-- (its direction, what follows its crossing, when it crossed, how it
-- ended); a spike moves the printed price for a few seconds, alone or as
-- one of a target's (its width, its target).

-- +goose Up
ALTER TABLE events
    DROP CONSTRAINT events_type_check,
    ADD CONSTRAINT events_type_check CHECK (type IN ('JUMP', 'TARGET', 'SPIKE', 'TREND', 'VOLATILITY', 'PAUSE', 'HALT', 'REANCHOR')),
    ADD COLUMN direction  text        NOT NULL DEFAULT '' CHECK (direction IN ('', 'ABOVE', 'BELOW')),
    ADD COLUMN then_mode  text        NOT NULL DEFAULT '' CHECK (then_mode IN ('', 'FOLLOW', 'HOLD')),
    ADD COLUMN width_s    integer     NOT NULL DEFAULT 0 CHECK (width_s BETWEEN 0 AND 60),
    ADD COLUMN parent_id  uuid,
    ADD COLUMN crossed_at timestamptz,
    ADD COLUMN result     text        NOT NULL DEFAULT '' CHECK (result IN ('', 'HIT', 'MISSED', 'CANCELED'));
CREATE INDEX events_parent_idx ON events (parent_id) WHERE parent_id IS NOT NULL;

-- +goose Down
-- Fails while a SPIKE is stored: delete them first.
DROP INDEX events_parent_idx;
ALTER TABLE events
    DROP COLUMN result,
    DROP COLUMN crossed_at,
    DROP COLUMN parent_id,
    DROP COLUMN width_s,
    DROP COLUMN then_mode,
    DROP COLUMN direction,
    DROP CONSTRAINT events_type_check,
    ADD CONSTRAINT events_type_check CHECK (type IN ('JUMP', 'TARGET', 'TREND', 'VOLATILITY', 'PAUSE', 'HALT', 'REANCHOR'));
