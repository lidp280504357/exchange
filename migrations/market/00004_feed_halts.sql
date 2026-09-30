-- Pairs market-data-service halted because the reference feed was lost
-- (flag market.halt_on_feed_loss, ADR-0010): it resumes exactly these when
-- the feed is back, never a pair an operator halted.

-- +goose Up
CREATE TABLE feed_halts (
    symbol    text        PRIMARY KEY,
    halted_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE feed_halts;
