-- The apps to download (design 2026-10-07, App download page §2.1, batch
-- H0): one row per platform, ANDROID and IOS, OFF until an operator sets
-- it. mode LINK sends the sites to link_url (an app store, TestFlight,
-- another page), FILE to the current uploaded app. The files themselves
-- are on the server's disk (admin-service writes them, nginx serves
-- /downloads/); the rows keep what was read of them:
--   current, mobileconfig: {file_id, kind, name, size, sha256, stored_as,
--     manifest (an .ipa's manifest.plist), package, version, build,
--     min_os, uploaded_at, uploaded_by}, null for none (mobileconfig on
--     iOS only);
--   files: every file kept for the platform, newest first, the same
--     objects (the current app and configuration profile among them);
--   notes: the version notes by language ({"zh-CN", "zh-TW", "en"}).
-- version goes up with every change (a setting, an upload, a deletion);
-- the public answer's ETag carries the larger of the two rows'.

-- +goose Up
CREATE TABLE platform_apps (
    platform     text        PRIMARY KEY CHECK (platform IN ('ANDROID', 'IOS')),
    mode         text        NOT NULL CHECK (mode IN ('OFF', 'LINK', 'FILE')),
    link_url     text        NOT NULL CHECK (link_url = '' OR (link_url LIKE 'https://%' AND char_length(link_url) <= 500)),
    enabled      boolean     NOT NULL,
    current      jsonb       CHECK (current IS NULL OR jsonb_typeof(current) = 'object'),
    mobileconfig jsonb       CHECK (mobileconfig IS NULL OR jsonb_typeof(mobileconfig) = 'object'),
    files        jsonb       NOT NULL CHECK (jsonb_typeof(files) = 'array'),
    notes        jsonb       NOT NULL CHECK (jsonb_typeof(notes) = 'object'),
    version      bigint      NOT NULL CHECK (version >= 1),
    updated_by   text        NOT NULL,
    updated_at   timestamptz NOT NULL,
    CHECK (mode <> 'LINK' OR link_url <> ''),
    CHECK (mode <> 'FILE' OR current IS NOT NULL),
    CHECK (platform = 'IOS' OR mobileconfig IS NULL)
);

INSERT INTO platform_apps (platform, mode, link_url, enabled, files, notes, version, updated_by, updated_at)
VALUES ('ANDROID', 'OFF', '', false, '[]', '{"zh-CN": "", "zh-TW": "", "en": ""}', 1, 'system:migration', now()),
       ('IOS', 'OFF', '', false, '[]', '{"zh-CN": "", "zh-TW": "", "en": ""}', 1, 'system:migration', now());

-- Their changes are kept in the history with the row before and after.
ALTER TABLE config_history DROP CONSTRAINT config_history_entity_check;
ALTER TABLE config_history ADD CONSTRAINT config_history_entity_check
    CHECK (entity IN ('FEE_SCHEDULE', 'ASSET', 'NETWORK', 'TRADING_PAIR', 'CONTRACT', 'ASSET_PROFILE', 'PLATFORM_PROFILE', 'PLATFORM_APP'));

-- +goose Down
DELETE FROM config_history WHERE entity = 'PLATFORM_APP';
ALTER TABLE config_history DROP CONSTRAINT config_history_entity_check;
ALTER TABLE config_history ADD CONSTRAINT config_history_entity_check
    CHECK (entity IN ('FEE_SCHEDULE', 'ASSET', 'NETWORK', 'TRADING_PAIR', 'CONTRACT', 'ASSET_PROFILE', 'PLATFORM_PROFILE'));
DROP TABLE platform_apps;
