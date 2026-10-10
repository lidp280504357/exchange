-- The console's access switches (design 2026-10-02, N1): whether signing
-- in asks for the authenticator code (admin.require_totp). One row,
-- written by admin-service with its guards and by exchangectl in its
-- container (switching off only); admin-service stores it the first time
-- it starts, carried over from the flag admin.login_without_totp it
-- replaces. totp_confirmed_at is when an administrator's current
-- authenticator proved itself with a code (it is "bound"): signing in
-- while codes are asked, the setup link that binds it, binding it on the
-- account page.

-- +goose Up
CREATE TABLE console_access (
    id           boolean     PRIMARY KEY DEFAULT true CHECK (id),
    require_totp boolean     NOT NULL,
    updated_by   text        NOT NULL,
    updated_at   timestamptz NOT NULL
);

ALTER TABLE admins ADD COLUMN totp_confirmed_at timestamptz;

-- +goose Down
ALTER TABLE admins DROP COLUMN totp_confirmed_at;
DROP TABLE console_access;
