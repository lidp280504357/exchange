-- The platform's profile (design 2026-10-04 §4.1): the exchange's name,
-- domain, colours, footer, contact, social links, default language, the
-- learning-mode banner and whether sign-ups are open, which operators
-- change in the admin console and the sites read at run time. One row,
-- seeded with what the sites show without it. The texts are by language
-- ({"zh-CN": ..., "en": ...}); instrument-service checks the rest.
-- version goes up with every change, the images' included: their URLs
-- carry it, so a new image is never served from an old cache.

-- +goose Up
CREATE TABLE platform_profile (
    id                  smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    name                text        NOT NULL CHECK (char_length(name) BETWEEN 2 AND 32),
    short_name          text        NOT NULL CHECK (char_length(short_name) BETWEEN 2 AND 12),
    domain              text        NOT NULL DEFAULT '' CHECK (char_length(domain) <= 253),
    theme_color         text        NOT NULL CHECK (theme_color ~ '^#[0-9a-f]{6}$'),
    brand_color         text        NOT NULL CHECK (brand_color ~ '^#[0-9a-f]{6}$'),
    default_locale      text        NOT NULL CHECK (default_locale IN ('zh-CN', 'en')),
    learning_enabled    boolean     NOT NULL,
    registration_status text        NOT NULL CHECK (registration_status IN ('OPEN', 'CLOSED')),
    -- footer {copyright, compliance}, contact {email, support_url},
    -- social [{kind, url}], learning_text, registration_closed_text.
    texts               jsonb       NOT NULL CHECK (jsonb_typeof(texts) = 'object'),
    version             bigint      NOT NULL CHECK (version >= 1),
    updated_by          text        NOT NULL,
    updated_at          timestamptz NOT NULL
);

INSERT INTO platform_profile (name, short_name, theme_color, brand_color, default_locale, learning_enabled, registration_status,
                              texts, version, updated_by, updated_at)
VALUES ('Astras', 'Astras', '#0b0e11', '#f0b90b', 'zh-CN', true, 'OPEN', '{
  "footer": {
    "copyright": {"zh-CN": "© 2026 Astras · 学习项目，资金为模拟", "en": "© 2026 Astras · a learning project with simulated funds"},
    "compliance": {"zh-CN": "", "en": ""}
  },
  "contact": {"email": "", "support_url": ""},
  "social": [],
  "learning_text": {"zh-CN": "本站为学习/模拟环境，资产无真实价值。", "en": "A learning environment: balances are simulated and have no real value."},
  "registration_closed_text": {"zh-CN": "暂未开放注册。", "en": "Sign-ups are closed for now."}
}', 1, 'system:migration', now());

-- The uploaded images; none until an operator uploads one, and the sites
-- show their own meanwhile.
CREATE TABLE platform_images (
    kind       text        PRIMARY KEY CHECK (kind IN ('logo_light', 'logo_dark', 'favicon', 'apple_touch_icon')),
    data       bytea       NOT NULL CHECK (octet_length(data) BETWEEN 1 AND 204800),
    mime       text        NOT NULL CHECK (mime IN ('image/png', 'image/svg+xml', 'image/webp')),
    width      integer     NOT NULL CHECK (width > 0),
    updated_at timestamptz NOT NULL
);

-- Profile changes are kept in the history with the profile before and after.
ALTER TABLE config_history DROP CONSTRAINT config_history_entity_check;
ALTER TABLE config_history ADD CONSTRAINT config_history_entity_check
    CHECK (entity IN ('FEE_SCHEDULE', 'ASSET', 'NETWORK', 'TRADING_PAIR', 'CONTRACT', 'ASSET_PROFILE', 'PLATFORM_PROFILE'));

-- +goose Down
DELETE FROM config_history WHERE entity = 'PLATFORM_PROFILE';
ALTER TABLE config_history DROP CONSTRAINT config_history_entity_check;
ALTER TABLE config_history ADD CONSTRAINT config_history_entity_check
    CHECK (entity IN ('FEE_SCHEDULE', 'ASSET', 'NETWORK', 'TRADING_PAIR', 'CONTRACT', 'ASSET_PROFILE'));
DROP TABLE platform_images;
DROP TABLE platform_profile;
