-- The ledger's settings an operator changes in the admin console (design
-- 2026-10-04 §4.2). Today one: welcome_credits, what a new account gets
-- while the flag ledger.welcome_credit is on, as a list of
-- {"asset", "amount"} with decimal-string amounts; an empty list grants
-- nothing (a live exchange sets it so). The first value comes from the
-- environment (WELCOME_FUNDS) when ledger-service first starts with this
-- table; from then on the table rules. version goes up with every change,
-- so two operators cannot overwrite each other unseen.

-- +goose Up
CREATE TABLE settings (
    key        text        PRIMARY KEY CHECK (key IN ('welcome_credits')),
    value      jsonb       NOT NULL,
    version    bigint      NOT NULL CHECK (version >= 1),
    updated_by text        NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (key <> 'welcome_credits' OR jsonb_typeof(value) = 'array')
);

-- +goose Down
DROP TABLE settings;
