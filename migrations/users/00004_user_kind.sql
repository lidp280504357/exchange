-- What an account is (L0, the user 2026-10-09/10): HUMAN, a person (the
-- default, every sign-up); BOT, the simulated market's bots; TEST, the
-- end-to-end scripts' accounts; SYSTEM, HOUSE and the like. It is what the
-- console shows and filters by (humans by default), nothing else: no
-- trading, fee, risk, notification or bot code reads it - the platform
-- coin's bots are a production feature and trade as any account - and the
-- public API does not show it. user_kind_changes keeps every change.

-- +goose Up
ALTER TABLE users ADD COLUMN kind text NOT NULL DEFAULT 'HUMAN'
    CHECK (kind IN ('HUMAN', 'BOT', 'TEST', 'SYSTEM'));
CREATE INDEX users_kind ON users (kind);

CREATE TABLE user_kind_changes (
    id         bigserial   PRIMARY KEY,
    user_id    uuid        NOT NULL REFERENCES users (id),
    from_kind  text        NOT NULL,
    to_kind    text        NOT NULL,
    actor      text        NOT NULL,
    reason     text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX user_kind_changes_user ON user_kind_changes (user_id, id DESC);

-- +goose Down
DROP TABLE user_kind_changes;
DROP INDEX users_kind;
ALTER TABLE users DROP COLUMN kind;
