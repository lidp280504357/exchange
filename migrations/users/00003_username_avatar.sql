-- Usernames and avatars (design 2026-10-07, avatars and usernames §1, §2):
-- every user has a username, unique whatever the case, user_ and 8
-- lowercase letters or digits until the user changes it (once in 7 days:
-- username_changed_at); the avatar is the uploaded files' description,
-- null for the sites' default.

-- +goose Up
ALTER TABLE users
    ADD COLUMN username            text,
    ADD COLUMN username_changed_at timestamptz,
    -- {"path", "thumb_path", "uploaded_at", "size", "sha256"}: the files
    -- under the avatars directory (paths relative to it), the upload's
    -- time, its size in bytes and the 256 x 256 file's digest.
    ADD COLUMN avatar              jsonb;

CREATE UNIQUE INDEX users_username_lower ON users (lower(username));

-- The users there are get one now, as a sign-up would (hex digits are
-- lowercase letters or digits), drawn again on the rare clash.
-- +goose StatementBegin
DO $$
DECLARE
    r         record;
    candidate text;
BEGIN
    FOR r IN SELECT id FROM users WHERE username IS NULL LOOP
        LOOP
            candidate := 'user_' || substr(md5(random()::text || clock_timestamp()::text || r.id::text), 1, 8);
            EXIT WHEN NOT EXISTS (SELECT 1 FROM users WHERE lower(username) = candidate);
        END LOOP;
        UPDATE users SET username = candidate WHERE id = r.id;
    END LOOP;
END $$;
-- +goose StatementEnd

-- The default is for a sign-up by code that does not give one yet (the
-- migration runs before the new code is in): the service draws its own
-- and retries on a clash.
ALTER TABLE users
    ALTER COLUMN username SET DEFAULT ('user_' || substr(md5(random()::text || clock_timestamp()::text), 1, 8)),
    ALTER COLUMN username SET NOT NULL,
    ADD CONSTRAINT users_username_format CHECK (username ~ '^[A-Za-z0-9][A-Za-z0-9_]{2,19}$'),
    ADD CONSTRAINT users_avatar_object CHECK (avatar IS NULL OR jsonb_typeof(avatar) = 'object');

-- +goose Down
ALTER TABLE users DROP CONSTRAINT users_avatar_object, DROP CONSTRAINT users_username_format;
DROP INDEX users_username_lower;
ALTER TABLE users DROP COLUMN avatar, DROP COLUMN username_changed_at, DROP COLUMN username;
