-- risk-service (requirements §5.13): the devices each user has used, the
-- events velocity rules count, and every assessment with at least one hit.

-- +goose Up
CREATE TABLE user_devices (
    user_id       UUID        NOT NULL,
    device_id     TEXT        NOT NULL CHECK (device_id <> ''),
    first_seen_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, device_id)
);

-- One row per event and rule; the key is the rule's value (a device, a
-- masked network or a user). Kept for the longest rule window.
CREATE TABLE velocity_events (
    rule     TEXT        NOT NULL,
    key      TEXT        NOT NULL CHECK (key <> ''),
    event_id UUID        NOT NULL,
    at       TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (rule, key, event_id)
);
CREATE INDEX velocity_events_window_idx ON velocity_events (rule, key, at);
CREATE INDEX velocity_events_at_idx ON velocity_events (at);

CREATE TABLE assessments (
    id                UUID        PRIMARY KEY,
    user_id           UUID        NOT NULL,
    source_event_id   UUID        NOT NULL UNIQUE,
    source_event_type TEXT        NOT NULL,
    score             INT         NOT NULL CHECK (score BETWEEN 0 AND 100),
    action            TEXT        NOT NULL CHECK (action IN ('NONE', 'STEP_UP', 'REVIEW', 'REJECT')),
    hits              JSONB       NOT NULL,
    enforced          BOOLEAN     NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL
);
CREATE INDEX assessments_user_idx ON assessments (user_id, created_at DESC);

-- +goose Down
DROP TABLE assessments;
DROP TABLE velocity_events;
DROP TABLE user_devices;
