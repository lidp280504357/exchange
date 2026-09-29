-- Liquidation (requirements §11.7; plan §7.3 task 7): a position below its
-- maintenance margin is taken over and closed by the liquidation engine
-- (orders of kind LIQUIDATION), or against its counterparties (ADL) when
-- the book cannot take it.

-- +goose Up
ALTER TABLE positions
    ADD COLUMN liquidating         boolean     NOT NULL DEFAULT false,
    -- Liquidation orders tried since it was taken over.
    ADD COLUMN liquidation_attempts int        NOT NULL DEFAULT 0,
    ADD COLUMN liquidation_at      timestamptz,
    -- Set while the margin is at most 1.2 x the maintenance margin; the
    -- warning goes out once per episode.
    ADD COLUMN warned_at           timestamptz;
ALTER TABLE orders ADD CONSTRAINT orders_kind_check CHECK (kind IN ('USER', 'LIQUIDATION', 'ADL', 'TAKE_PROFIT', 'STOP_LOSS'));

-- Cross accounts (per user) warned that their margin is close to the
-- maintenance margin; taken-over cross positions carry liquidating.
CREATE TABLE cross_accounts (
    user_id    uuid        PRIMARY KEY,
    warned_at  timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE cross_accounts;
ALTER TABLE orders DROP CONSTRAINT orders_kind_check;
ALTER TABLE positions DROP COLUMN warned_at, DROP COLUMN liquidation_at, DROP COLUMN liquidation_attempts,
    DROP COLUMN liquidating;
