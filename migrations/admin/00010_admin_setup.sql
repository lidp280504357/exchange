-- Administrators set their own credentials (C5.5 ⑪): the console's
-- create and resets give a one-time setup token (a day; only its hash is
-- kept) instead of a password or an authenticator secret, and the
-- administrator sets the password and binds the authenticator; one whose
-- password was generated for them (exchangectl admin create) changes it
-- before anything else. An administrator's own new authenticator waits
-- in the same columns (SELF_TOTP, no token).

-- +goose Up
ALTER TABLE admins
    ADD COLUMN setup_kind text CHECK (setup_kind IN ('CREATE', 'PASSWORD', 'TOTP', 'SELF_TOTP')),
    ADD COLUMN setup_hash bytea,
    ADD COLUMN setup_totp_sealed bytea,
    ADD COLUMN setup_expires_at timestamptz,
    ADD COLUMN must_change_password boolean NOT NULL DEFAULT false,
    -- No setup, or one with its expiry, a token unless it is one's own
    -- authenticator, and an authenticator to bind unless it sets a
    -- password alone.
    ADD CONSTRAINT admins_setup_check CHECK (
        (setup_kind IS NULL AND setup_hash IS NULL AND setup_totp_sealed IS NULL AND setup_expires_at IS NULL)
        OR (setup_kind IS NOT NULL AND setup_expires_at IS NOT NULL
            AND (setup_hash IS NOT NULL) = (setup_kind <> 'SELF_TOTP')
            AND (setup_totp_sealed IS NOT NULL) = (setup_kind <> 'PASSWORD')));
CREATE UNIQUE INDEX admins_setup_idx ON admins (setup_hash) WHERE setup_hash IS NOT NULL;

-- +goose Down
DROP INDEX admins_setup_idx;
ALTER TABLE admins DROP CONSTRAINT admins_setup_check, DROP COLUMN must_change_password, DROP COLUMN setup_expires_at,
    DROP COLUMN setup_totp_sealed, DROP COLUMN setup_hash, DROP COLUMN setup_kind;
