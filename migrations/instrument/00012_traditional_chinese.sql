-- Traditional Chinese (design 2026-10-06 繁体中文 §2.1): the platform's
-- default language may be zh-TW. Its texts (jsonb by language) and the
-- assets' introductions take zh-TW as they are.

-- +goose Up
ALTER TABLE platform_profile
    DROP CONSTRAINT platform_profile_default_locale_check,
    ADD CONSTRAINT platform_profile_default_locale_check CHECK (default_locale IN ('zh-CN', 'zh-TW', 'en'));

-- +goose Down
UPDATE platform_profile SET default_locale = 'zh-CN' WHERE default_locale = 'zh-TW';
ALTER TABLE platform_profile
    DROP CONSTRAINT platform_profile_default_locale_check,
    ADD CONSTRAINT platform_profile_default_locale_check CHECK (default_locale IN ('zh-CN', 'en'));
