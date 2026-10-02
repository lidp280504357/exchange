-- The operators' price events (ASTRA design §6.2): scheduled or at once,
-- each with its course, so that a restart picks a running one up where it
-- was.

-- +goose Up
CREATE TABLE events (
    id          uuid        PRIMARY KEY,
    type        text        NOT NULL CHECK (type IN ('JUMP', 'TARGET', 'TREND', 'VOLATILITY', 'PAUSE', 'HALT', 'REANCHOR')),
    -- What it does: a jump's size (0.1 is +10%), a target price, a trend
    -- per day, a volatility factor; how long it takes and, for a target,
    -- how long it holds there (seconds; 0 as the type says).
    size        double precision NOT NULL DEFAULT 0,
    price       numeric(38,18) NOT NULL DEFAULT 0,
    mu          double precision NOT NULL DEFAULT 0,
    factor      double precision NOT NULL DEFAULT 0,
    duration_s  integer     NOT NULL DEFAULT 0 CHECK (duration_s >= 0),
    hold_s      integer     NOT NULL DEFAULT 0 CHECK (hold_s >= 0),
    starts_at   timestamptz NOT NULL,
    status      text        NOT NULL CHECK (status IN ('SCHEDULED', 'RUNNING', 'DONE', 'CANCELED')),
    created_by  text        NOT NULL,
    approved_by text        NOT NULL DEFAULT '',
    reason      text        NOT NULL,
    created_at  timestamptz NOT NULL,
    -- Its course: when it started and ended, the event factor (log) and
    -- the target when it started, who ended it early.
    started_at  timestamptz,
    ended_at    timestamptz,
    from_log_e  double precision NOT NULL DEFAULT 0,
    from_p      numeric(38,18) NOT NULL DEFAULT 0,
    ended_by    text        NOT NULL DEFAULT ''
);
CREATE INDEX events_open_idx ON events (starts_at) WHERE status IN ('SCHEDULED', 'RUNNING');
CREATE INDEX events_recent_idx ON events (created_at DESC);

-- +goose Down
DROP TABLE events;
