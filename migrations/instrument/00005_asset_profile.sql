-- Asset profiles (ASTRA design 2026-10-02 §5.3): what the sites show of an
-- asset beyond its code, changed by operators: a display name, an
-- introduction per language, links and a logo (PNG, SVG cleaned on upload,
-- or WebP, square, at most 200 KB). profile_version goes up with every
-- change; logo URLs carry it, so a new logo is never served from an old
-- cache. Reference data applies (exchangectl instruments apply) never touch
-- these columns.

-- +goose Up
ALTER TABLE assets
    ADD COLUMN display_name    text  NOT NULL DEFAULT '' CHECK (char_length(display_name) <= 32),
    ADD COLUMN description     jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(description) = 'object'),
    ADD COLUMN links           jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(links) = 'object'),
    ADD COLUMN logo            bytea CHECK (octet_length(logo) BETWEEN 1 AND 204800),
    ADD COLUMN logo_mime       text  NOT NULL DEFAULT '' CHECK (logo_mime IN ('', 'image/png', 'image/svg+xml', 'image/webp')),
    ADD COLUMN profile_version bigint NOT NULL DEFAULT 0 CHECK (profile_version >= 0),
    ADD CONSTRAINT assets_logo_typed CHECK ((logo IS NULL) = (logo_mime = ''));

-- Profile changes are kept in the history with the profile before and after.
ALTER TABLE config_history DROP CONSTRAINT config_history_entity_check;
ALTER TABLE config_history ADD CONSTRAINT config_history_entity_check
    CHECK (entity IN ('FEE_SCHEDULE', 'ASSET', 'NETWORK', 'TRADING_PAIR', 'CONTRACT', 'ASSET_PROFILE'));

-- +goose Down
DELETE FROM config_history WHERE entity = 'ASSET_PROFILE';
ALTER TABLE config_history DROP CONSTRAINT config_history_entity_check;
ALTER TABLE config_history ADD CONSTRAINT config_history_entity_check
    CHECK (entity IN ('FEE_SCHEDULE', 'ASSET', 'NETWORK', 'TRADING_PAIR', 'CONTRACT'));

ALTER TABLE assets
    DROP CONSTRAINT assets_logo_typed,
    DROP COLUMN profile_version,
    DROP COLUMN logo_mime,
    DROP COLUMN logo,
    DROP COLUMN links,
    DROP COLUMN description,
    DROP COLUMN display_name;
