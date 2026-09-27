-- notification-service: deliveries through mail/SMS providers and the
-- in-app inbox (requirements §5.3, §6.2).

-- +goose Up

-- One message handed to a provider, with the outcome (§5.3: 发送结果落库).
-- OTP codes never reach this table.
CREATE TABLE deliveries (
    id                  uuid        PRIMARY KEY,
    kind                text        NOT NULL CHECK (kind IN ('OTP', 'NOTICE')),
    channel             text        NOT NULL CHECK (channel IN ('EMAIL', 'SMS')),
    template            text        NOT NULL,
    target_mask         text        NOT NULL,
    user_id             uuid,
    provider            text        NOT NULL DEFAULT '',
    status              text        NOT NULL DEFAULT 'QUEUED'
                                    CHECK (status IN ('QUEUED', 'SENT', 'FAILED_RETRYING', 'FAILED')),
    attempts            int         NOT NULL DEFAULT 0,
    -- TIMEOUT, REJECTED, INVALID_TARGET, INSUFFICIENT_BALANCE, CIRCUIT_OPEN, UNKNOWN
    failure_class       text        NOT NULL DEFAULT '',
    provider_message_id text        NOT NULL DEFAULT '',
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX deliveries_created_at ON deliveries (created_at);
CREATE INDEX deliveries_failed ON deliveries (updated_at) WHERE status = 'FAILED';

-- In-app notifications (GET /v1/notifications).
CREATE TABLE notifications (
    id         uuid        PRIMARY KEY,
    user_id    uuid        NOT NULL,
    type       text        NOT NULL,
    title      text        NOT NULL,
    body       text        NOT NULL,
    data       jsonb       NOT NULL DEFAULT '{}',
    read_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notifications_user ON notifications (user_id, id DESC);

-- Messages "sent" by the mock providers (SMS in development, test mail
-- domains), readable through the dev inbox outside production so that
-- people and end-to-end tests can see the code (decision #8). Purged
-- after a day.
CREATE TABLE mock_messages (
    id         bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    channel    text        NOT NULL,
    target     text        NOT NULL,
    subject    text        NOT NULL DEFAULT '',
    body       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX mock_messages_target ON mock_messages (target, id DESC);

-- +goose Down
DROP TABLE mock_messages;
DROP TABLE notifications;
DROP TABLE deliveries;
