-- When the user's authenticator app was last unbound or reset by an
-- administrator (admin console C5.5 ⑤): withdrawals within a day of it
-- wait for review like those after a password or identity change; the
-- console shows it on the account's security.

-- +goose Up
ALTER TABLE credentials ADD COLUMN totp_changed_at timestamptz;

-- +goose Down
ALTER TABLE credentials DROP COLUMN totp_changed_at;
