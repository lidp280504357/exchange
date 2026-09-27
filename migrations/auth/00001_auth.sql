-- auth-service: identities, passwords, OTP, sessions and login history
-- (requirements §5.2, §5.3, §6; ADR-0009). Secrets are stored only as
-- hashes: OTP codes as HMAC-SHA256, tickets and refresh tokens as SHA-256,
-- passwords as Argon2id PHC strings.

-- +goose Up

-- An email or phone number bound to a user; each user keeps at least one.
CREATE TABLE identities (
    id          uuid        PRIMARY KEY,
    user_id     uuid        NOT NULL,
    kind        text        NOT NULL CHECK (kind IN ('EMAIL', 'PHONE')),
    -- Normalized: trimmed lower-case email, or E.164 phone number.
    value       text        NOT NULL,
    verified_at timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT identities_kind_value_key UNIQUE (kind, value),
    CONSTRAINT identities_user_kind_key UNIQUE (user_id, kind)
);
CREATE INDEX identities_user ON identities (user_id);

CREATE TABLE credentials (
    user_id             uuid        PRIMARY KEY,
    password_hash       text        NOT NULL,
    failed_attempts     int         NOT NULL DEFAULT 0,
    locked_until        timestamptz,
    -- Last successful login; the 7-day challenge rule reads it.
    last_login_at       timestamptz,
    password_changed_at timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

-- One OTP send-and-verify session.
CREATE TABLE otp_challenges (
    id                 uuid        PRIMARY KEY,
    scene              text        NOT NULL CHECK (scene IN ('REGISTER', 'LOGIN', 'LOGIN_CHALLENGE', 'PASSWORD_RESET',
                                       'BIND_IDENTITY', 'REBIND_IDENTITY', 'STEP_UP', 'WITHDRAW_CONFIRM')),
    channel            text        NOT NULL CHECK (channel IN ('EMAIL', 'SMS')),
    -- Normalized destination; kept so the code can be delivered and the
    -- verified identity bound. Logs and events only see it masked.
    target             text        NOT NULL,
    user_id            uuid,
    login_challenge_id uuid,
    device_id          text        NOT NULL,
    -- HMAC-SHA256(key, challenge id ":" code). A decoy challenge (unknown
    -- account, blocked scene) gets a random hash that no code matches.
    code_hash          bytea       NOT NULL,
    attempts           int         NOT NULL DEFAULT 0,
    status             text        NOT NULL DEFAULT 'PENDING'
                                   CHECK (status IN ('PENDING', 'VERIFIED', 'LOCKED')),
    expires_at         timestamptz NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    verified_at        timestamptz
);
CREATE INDEX otp_challenges_created_at ON otp_challenges (created_at);

-- One-time proof of a verified challenge, redeemed by */complete endpoints.
CREATE TABLE otp_tickets (
    ticket_hash  bytea       PRIMARY KEY,
    challenge_id uuid        NOT NULL REFERENCES otp_challenges (id),
    scene        text        NOT NULL,
    device_id    text        NOT NULL,
    expires_at   timestamptz NOT NULL,
    consumed_at  timestamptz
);

-- Password accepted but the account had no login for 7 days: finish with
-- an OTP to a bound identity.
CREATE TABLE login_challenges (
    id          uuid        PRIMARY KEY,
    user_id     uuid        NOT NULL,
    device_id   text        NOT NULL,
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- A device session; its refresh tokens rotate on every use.
CREATE TABLE sessions (
    id            uuid        PRIMARY KEY,
    user_id       uuid        NOT NULL,
    device_id     text        NOT NULL,
    client_type   text        NOT NULL CHECK (client_type IN ('WEB', 'APP')),
    user_agent    text        NOT NULL DEFAULT '',
    ip            text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    revoked_at    timestamptz,
    revoke_reason text
);
CREATE INDEX sessions_user_active ON sessions (user_id, last_seen_at) WHERE revoked_at IS NULL;

-- A refresh token is used once; presenting a rotated token again is a
-- replay and revokes the whole session.
CREATE TABLE refresh_tokens (
    token_hash bytea       PRIMARY KEY,
    session_id uuid        NOT NULL REFERENCES sessions (id),
    generation int         NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    rotated_at timestamptz
);
CREATE INDEX refresh_tokens_session ON refresh_tokens (session_id);

-- Short-lived proof of re-authentication for a sensitive action.
CREATE TABLE step_up_tokens (
    token_hash  bytea       PRIMARY KEY,
    user_id     uuid        NOT NULL,
    session_id  uuid        NOT NULL,
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Devices a user has logged in from; a new one triggers a notification.
CREATE TABLE known_devices (
    user_id       uuid        NOT NULL,
    device_id     text        NOT NULL,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, device_id)
);

CREATE TABLE login_history (
    id             bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id        uuid,
    method         text        NOT NULL CHECK (method IN ('PASSWORD', 'OTP', 'LOGIN_CHALLENGE', 'REGISTER')),
    result         text        NOT NULL,
    identity_mask  text        NOT NULL DEFAULT '',
    device_id      text        NOT NULL DEFAULT '',
    user_agent     text        NOT NULL DEFAULT '',
    ip             text        NOT NULL DEFAULT '',
    new_device     boolean     NOT NULL DEFAULT false,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX login_history_user ON login_history (user_id, id DESC);

-- Single-identity users cannot rebind on their own (§6.4): the request
-- waits for two-person review in the admin console (phase 2).
CREATE TABLE identity_rebind_requests (
    id          uuid        PRIMARY KEY,
    user_id     uuid        NOT NULL,
    kind        text        NOT NULL CHECK (kind IN ('EMAIL', 'PHONE')),
    new_value   text        NOT NULL,
    status      text        NOT NULL DEFAULT 'PENDING_REVIEW'
                            CHECK (status IN ('PENDING_REVIEW', 'APPROVED', 'REJECTED')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    decided_at  timestamptz
);

-- +goose Down
DROP TABLE identity_rebind_requests;
DROP TABLE login_history;
DROP TABLE known_devices;
DROP TABLE step_up_tokens;
DROP TABLE refresh_tokens;
DROP TABLE sessions;
DROP TABLE login_challenges;
DROP TABLE otp_tickets;
DROP TABLE otp_challenges;
DROP TABLE credentials;
DROP TABLE identities;
