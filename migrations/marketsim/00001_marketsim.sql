-- market-sim: the simulated market of the platform coin ASTRA (ASTRA
-- design §4, §5.1): the bots, the settings of the price model and the
-- cluster, and the model's state, so that a restart goes on from where it
-- stopped. Orders, trades and balances stay with the services that own
-- them; the bots are ordinary users.

-- +goose Up
-- The bot accounts: users registered like anyone (scripts/ops/astra.sh
-- seed) and handed a role.
CREATE TABLE bots (
    user_id    uuid        PRIMARY KEY,
    role       text        NOT NULL CHECK (role IN ('MAKER', 'TAKER', 'TREND', 'EXECUTOR')),
    label      text        NOT NULL UNIQUE,
    enabled    boolean     NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- The settings (one row): the price model's and the cluster's, as JSON
-- read by domain.Params; version counts the changes.
CREATE TABLE settings (
    id         smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    params     jsonb       NOT NULL,
    version    bigint      NOT NULL DEFAULT 1,
    updated_by text        NOT NULL,
    updated_at timestamptz NOT NULL
);

-- The model's state (one row), saved every few seconds: the anchor, the
-- own deviation and event factor, and the random source.
CREATE TABLE state (
    id         smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    state      jsonb       NOT NULL,
    saved_at   timestamptz NOT NULL
);

-- +goose Down
DROP TABLE state;
DROP TABLE settings;
DROP TABLE bots;
