-- Operations content (design 2026-10-02 §4.5): announcements and help
-- articles written in the admin console, in Chinese and English, published
-- at once or at a time and read by the sites through GET /v1/announcements
-- and /v1/help; and in-app messages an operator sends to some users or all
-- of them, with how many arrived and were read.

-- +goose Up
CREATE TABLE articles (
    id           uuid        PRIMARY KEY,
    section      text        NOT NULL CHECK (section IN ('ANNOUNCEMENT', 'HELP')),
    slug         text        NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,63}$'),
    category     text        NOT NULL DEFAULT '' CHECK (category ~ '^[a-z0-9_-]{0,32}$'),
    pinned       boolean     NOT NULL DEFAULT false,
    sort_order   integer     NOT NULL DEFAULT 0,
    status       text        NOT NULL CHECK (status IN ('DRAFT', 'PUBLISHED', 'ARCHIVED')),
    -- A published article shows from publish_at on (scheduled publishing).
    publish_at   timestamptz,
    version      integer     NOT NULL DEFAULT 1,
    updated_by   text        NOT NULL,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL,
    UNIQUE (section, slug),
    CHECK (status <> 'PUBLISHED' OR publish_at IS NOT NULL)
);
CREATE INDEX articles_published ON articles (section, publish_at DESC) WHERE status = 'PUBLISHED';

CREATE TABLE article_texts (
    article_id uuid NOT NULL REFERENCES articles (id) ON DELETE CASCADE,
    locale     text NOT NULL CHECK (locale IN ('zh-CN', 'en')),
    title      text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    summary    text NOT NULL DEFAULT '' CHECK (length(summary) <= 500),
    body       text NOT NULL CHECK (length(body) <= 100000),
    PRIMARY KEY (article_id, locale)
);

-- An in-app message to some users (their IDs) or to everyone; the sender
-- goes through the recipients in rounds, cursor holding where it is.
CREATE TABLE broadcasts (
    id          uuid        PRIMARY KEY,
    audience    text        NOT NULL CHECK (audience IN ('ALL', 'USERS')),
    user_ids    uuid[]      NOT NULL DEFAULT '{}',
    title       jsonb       NOT NULL,
    body        jsonb       NOT NULL,
    link        text        NOT NULL DEFAULT '',
    email       boolean     NOT NULL DEFAULT false,
    status      text        NOT NULL CHECK (status IN ('SENDING', 'SENT')),
    cursor      text        NOT NULL DEFAULT '',
    recipients  integer     NOT NULL DEFAULT 0,
    created_by  text        NOT NULL,
    created_at  timestamptz NOT NULL,
    finished_at timestamptz,
    CHECK (audience = 'ALL' OR cardinality(user_ids) BETWEEN 1 AND 10000)
);
CREATE INDEX broadcasts_sending ON broadcasts (created_at) WHERE status = 'SENDING';
CREATE INDEX notifications_broadcast ON notifications ((data ->> 'broadcast_id')) WHERE data ? 'broadcast_id';

-- +goose Down
DROP INDEX notifications_broadcast;
DROP TABLE broadcasts;
DROP TABLE article_texts;
DROP TABLE articles;
