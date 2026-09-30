-- A user's favorite markets (design 2026-09-30 §6.2): the list the market
-- pages star, in the user's order; one row per user.

-- +goose Up
CREATE TABLE favorites (
    user_id    uuid        PRIMARY KEY REFERENCES users (id),
    symbols    text[]      NOT NULL DEFAULT '{}' CHECK (cardinality(symbols) <= 100),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE favorites;
