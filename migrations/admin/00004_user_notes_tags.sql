-- Administrators' notes on an account (a timeline, never edited) and its
-- tags (VIP, SUSPICIOUS, TEST and the like), for the user's page of the
-- admin console (design 2026-10-02 §4.1). Both belong to the console, not
-- to the account: user-service never sees them.

-- +goose Up
CREATE TABLE user_notes (
    id         uuid        PRIMARY KEY,
    user_id    uuid        NOT NULL,
    admin_id   uuid        NOT NULL REFERENCES admins (id),
    body       text        NOT NULL CHECK (length(body) BETWEEN 1 AND 2000),
    created_at timestamptz NOT NULL
);
CREATE INDEX user_notes_user_idx ON user_notes (user_id, created_at DESC, id DESC);

CREATE TABLE user_tags (
    user_id  uuid        NOT NULL,
    tag      text        NOT NULL CHECK (tag ~ '^[A-Z][A-Z0-9_]{0,31}$'),
    added_by uuid        NOT NULL REFERENCES admins (id),
    added_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, tag)
);
CREATE INDEX user_tags_tag_idx ON user_tags (tag);

-- +goose Down
DROP TABLE user_tags;
DROP TABLE user_notes;
