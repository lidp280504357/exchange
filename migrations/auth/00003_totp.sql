-- Authenticator apps (TOTP, requirements §6.5; plan §6.3 task 8): one per
-- user, the secret sealed with TOTP_SECRET_KEY (AES-256-GCM, bound to the
-- user ID); last_step keeps each code to one use. Step-ups can be proven
-- by TOTP, which takes precedence over codes once bound.

-- +goose Up
CREATE TABLE totp_credentials (
    user_id      uuid        PRIMARY KEY,
    secret_enc   bytea       NOT NULL,
    status       text        NOT NULL CHECK (status IN ('PENDING', 'ACTIVE')),
    last_step    bigint      NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL,
    activated_at timestamptz
);
ALTER TABLE step_up_tokens DROP CONSTRAINT step_up_tokens_channel_check,
    ADD CONSTRAINT step_up_tokens_channel_check CHECK (channel IN ('EMAIL', 'SMS', 'TOTP'));

-- +goose Down
DELETE FROM step_up_tokens WHERE channel = 'TOTP';
ALTER TABLE step_up_tokens DROP CONSTRAINT step_up_tokens_channel_check,
    ADD CONSTRAINT step_up_tokens_channel_check CHECK (channel IN ('EMAIL', 'SMS'));
DROP TABLE totp_credentials;
