-- admin-service (requirements §5.12; plan §6.3 task 11): administrators
-- with a password hash and a sealed authenticator secret (TOTP is
-- mandatory), their sessions (token hashes only), and the requests a
-- second administrator must approve.

-- +goose Up
CREATE TABLE admins (
    id              uuid        PRIMARY KEY,
    email           text        NOT NULL,
    name            text        NOT NULL,
    role            text        NOT NULL CHECK (role IN ('ADMIN', 'OPERATOR', 'FINANCE', 'AUDITOR')),
    password_hash   text        NOT NULL,
    totp_sealed     bytea       NOT NULL,
    totp_last_step  bigint      NOT NULL DEFAULT 0,
    status          text        NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'DISABLED')),
    failed_attempts int         NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0),
    locked_until    timestamptz,
    last_login_at   timestamptz,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX admins_email_idx ON admins (lower(email));

CREATE TABLE admin_sessions (
    token_hash   bytea       PRIMARY KEY,
    admin_id     uuid        NOT NULL REFERENCES admins (id),
    ip           text        NOT NULL DEFAULT '',
    user_agent   text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    expires_at   timestamptz NOT NULL,
    revoked_at   timestamptz
);
CREATE INDEX admin_sessions_admin_idx ON admin_sessions (admin_id) WHERE revoked_at IS NULL;

CREATE TABLE approvals (
    id           uuid        PRIMARY KEY,
    kind         text        NOT NULL CHECK (kind IN ('LEDGER_ADJUSTMENT')),
    payload      jsonb       NOT NULL,
    reason       text        NOT NULL,
    status       text        NOT NULL CHECK (status IN ('PENDING', 'EXECUTED', 'REJECTED', 'FAILED')),
    requested_by uuid        NOT NULL REFERENCES admins (id),
    decided_by   uuid        REFERENCES admins (id),
    result       text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL,
    decided_at   timestamptz,
    CHECK (decided_by IS NULL OR decided_by <> requested_by),
    CHECK ((status = 'PENDING') = (decided_by IS NULL))
);
CREATE INDEX approvals_status_idx ON approvals (status, created_at DESC);

-- +goose Down
DROP TABLE approvals;
DROP TABLE admin_sessions;
DROP TABLE admins;
