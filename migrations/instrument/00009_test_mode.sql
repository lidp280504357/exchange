-- The learning mode becomes the test mode (user decision 2026-10-04 18:45,
-- design 2026-10-04 §4.1): while it is on the sites show the content meant
-- for it and "测试模式" badges, and a banner unless an operator hides it
-- (test_banner). The banner's text defaults to "测试模式": the seeded
-- learning text gives way to it, an operator's own text stays. version
-- goes up so the sites' cached copies are read again.

-- +goose Up
ALTER TABLE platform_profile RENAME COLUMN learning_enabled TO test_mode;
ALTER TABLE platform_profile ADD COLUMN test_banner boolean NOT NULL DEFAULT true;
UPDATE platform_profile
SET texts = (texts - 'learning_text') || jsonb_build_object('test_text',
        CASE
            WHEN texts -> 'learning_text' IS NULL OR texts -> 'learning_text' = jsonb_build_object(
                'zh-CN', '本站为学习/模拟环境，资产无真实价值。',
                'en', 'A learning environment: balances are simulated and have no real value.')
                THEN jsonb_build_object('zh-CN', '测试模式', 'en', 'Test mode')
            ELSE texts -> 'learning_text'
        END),
    version = version + 1,
    updated_by = 'system:migration',
    updated_at = now();

-- +goose Down
UPDATE platform_profile
SET texts = (texts - 'test_text') || jsonb_build_object('learning_text', texts -> 'test_text'),
    version = version + 1,
    updated_by = 'system:migration',
    updated_at = now();
ALTER TABLE platform_profile DROP COLUMN test_banner;
ALTER TABLE platform_profile RENAME COLUMN test_mode TO learning_enabled;
