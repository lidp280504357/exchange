-- Price events on followed pairs (design 2026-10-07, general price control,
-- J0): an OVERLAY event multiplies one pair's reference data on the
-- platform by a factor that ramps from 1 to its target, holds, and ramps
-- back to 1. symbol is the pair (empty: the simulated market's own, as for
-- every event before); target_factor the target price over the reference
-- price when it started (base_price); the four samples tell how it went.

-- +goose Up
ALTER TABLE events
    ADD COLUMN symbol              text             NOT NULL DEFAULT '',
    ADD COLUMN target_factor       double precision NOT NULL DEFAULT 0,
    ADD COLUMN ramp_up_s           integer          NOT NULL DEFAULT 0 CHECK (ramp_up_s >= 0),
    ADD COLUMN ramp_down_s         integer          NOT NULL DEFAULT 0 CHECK (ramp_down_s >= 0),
    ADD COLUMN risk                boolean          NOT NULL DEFAULT true,
    ADD COLUMN base_price          numeric(38,18),
    ADD COLUMN peak_price          numeric(38,18),
    ADD COLUMN end_reference_price numeric(38,18),
    ADD COLUMN end_platform_price  numeric(38,18);
ALTER TABLE events DROP CONSTRAINT events_type_check;
ALTER TABLE events ADD CONSTRAINT events_type_check
    CHECK (type IN ('JUMP', 'TARGET', 'SPIKE', 'TREND', 'VOLATILITY', 'PAUSE', 'HALT', 'REANCHOR', 'OVERLAY'));
ALTER TABLE events ADD CONSTRAINT events_overlay_check
    CHECK (type <> 'OVERLAY' OR (symbol <> '' AND target_factor BETWEEN 0.1 AND 1.9 AND ramp_up_s >= 1 AND ramp_down_s >= 3
        AND ramp_up_s + hold_s + ramp_down_s <= 600));
-- One open overlay a pair.
CREATE UNIQUE INDEX events_overlay_open_idx ON events (symbol) WHERE type = 'OVERLAY' AND status IN ('SCHEDULED', 'RUNNING');

-- +goose Down
DROP INDEX events_overlay_open_idx;
ALTER TABLE events DROP CONSTRAINT events_overlay_check;
DELETE FROM events WHERE type = 'OVERLAY';
ALTER TABLE events DROP CONSTRAINT events_type_check;
ALTER TABLE events ADD CONSTRAINT events_type_check
    CHECK (type IN ('JUMP', 'TARGET', 'SPIKE', 'TREND', 'VOLATILITY', 'PAUSE', 'HALT', 'REANCHOR'));
ALTER TABLE events DROP COLUMN end_platform_price, DROP COLUMN end_reference_price, DROP COLUMN peak_price, DROP COLUMN base_price,
    DROP COLUMN risk, DROP COLUMN ramp_down_s, DROP COLUMN ramp_up_s, DROP COLUMN target_factor, DROP COLUMN symbol;
