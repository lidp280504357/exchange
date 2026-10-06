-- The uploads of the apps to download in progress (design 2026-10-07, App
-- download page §7 #12, batch H1): the console sends a file in parts of
-- 10 MiB (Cloudflare takes at most 100 MB a request); the parts are on the
-- server's disk (APP_UPLOADS_DIR/<upload_id>/<n>.part, nginx does not
-- serve them), a row says what the upload is, which parts came and whether
-- a completion holds it. Completed, dropped or expired (24 hours) it goes
-- with its parts; admin-service survives a restart in between.

-- +goose Up
CREATE TABLE app_uploads (
    upload_id        uuid        PRIMARY KEY,
    platform         text        NOT NULL CHECK (platform IN ('ANDROID', 'IOS')),
    kind             text        NOT NULL CHECK (kind IN ('APP', 'MOBILECONFIG')),
    name             text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
    size             bigint      NOT NULL CHECK (size BETWEEN 1 AND 524288000),
    sha256           text        NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    part_size        integer     NOT NULL CHECK (part_size > 0),
    received         integer[]   NOT NULL DEFAULT '{}',
    started_by       text        NOT NULL,
    started_at       timestamptz NOT NULL,
    expires_at       timestamptz NOT NULL,
    completing_until timestamptz,
    CHECK (platform = 'IOS' OR kind = 'APP'),
    CHECK (kind = 'APP' OR size <= 1048576)
);

CREATE INDEX app_uploads_expires_at ON app_uploads (expires_at);

-- +goose Down
DROP TABLE app_uploads;
