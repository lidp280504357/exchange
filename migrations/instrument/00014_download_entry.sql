-- The switch for the sites' download entries (design 2026-10-07, App
-- download page §1.2 #8, user 19:3x, batch H5): on by default, the entries
-- showing whether or not a platform is offered (the download page then
-- says none is offered yet); an operator turns it off in the console. One
-- row; version goes up with every switch, and the public answer's ETag
-- carries it.

-- +goose Up
CREATE TABLE platform_download_entry (
    id         smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    visible    boolean     NOT NULL,
    version    bigint      NOT NULL CHECK (version >= 1),
    updated_by text        NOT NULL,
    updated_at timestamptz NOT NULL
);

INSERT INTO platform_download_entry (visible, version, updated_by, updated_at)
VALUES (true, 1, 'system:migration', now());

-- Its switches are kept in the history with the state before and after.
ALTER TABLE config_history DROP CONSTRAINT config_history_entity_check;
ALTER TABLE config_history ADD CONSTRAINT config_history_entity_check
    CHECK (entity IN ('FEE_SCHEDULE', 'ASSET', 'NETWORK', 'TRADING_PAIR', 'CONTRACT', 'ASSET_PROFILE', 'PLATFORM_PROFILE', 'PLATFORM_APP',
                      'DOWNLOAD_ENTRY'));

-- +goose Down
DELETE FROM config_history WHERE entity = 'DOWNLOAD_ENTRY';
ALTER TABLE config_history DROP CONSTRAINT config_history_entity_check;
ALTER TABLE config_history ADD CONSTRAINT config_history_entity_check
    CHECK (entity IN ('FEE_SCHEDULE', 'ASSET', 'NETWORK', 'TRADING_PAIR', 'CONTRACT', 'ASSET_PROFILE', 'PLATFORM_PROFILE', 'PLATFORM_APP'));
DROP TABLE platform_download_entry;
