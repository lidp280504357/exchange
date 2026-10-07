-- The reference minutes a price event touched (design 2026-10-07, general
-- price control; review GD ③): stored as the platform showed them, the
-- charts lay them over the reference market's own candles, so a spike
-- stays in the history. Upsert does not replace them and the purge keeps
-- them.

-- +goose Up
ALTER TABLE reference_candles ADD COLUMN overlay boolean NOT NULL DEFAULT false;
CREATE INDEX reference_candles_overlay_idx ON reference_candles (symbol, open_time) WHERE overlay;

-- +goose Down
DROP INDEX reference_candles_overlay_idx;
ALTER TABLE reference_candles DROP COLUMN overlay;
