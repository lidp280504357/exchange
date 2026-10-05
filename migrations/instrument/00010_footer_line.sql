-- The seeded footer line called the exchange a learning project with
-- simulated funds. That is the test mode's to say (user decision 2026-10-04
-- 18:45: the learning wording goes, copy follows the mode), and the footer
-- does not follow the mode, so a site gone live kept saying it (review BK).
-- The seeded line gives way to the plain copyright; an operator's own line
-- stays. version goes up so the sites read the profile again (its ETag).

-- +goose Up
UPDATE platform_profile
SET texts = jsonb_set(texts, '{footer,copyright}', jsonb_build_object('zh-CN', '© 2026 Astras', 'en', '© 2026 Astras')),
    version = version + 1,
    updated_by = 'system:migration',
    updated_at = now()
WHERE texts #> '{footer,copyright}' = jsonb_build_object(
    'zh-CN', '© 2026 Astras · 学习项目，资金为模拟',
    'en', '© 2026 Astras · a learning project with simulated funds');

-- +goose Down
UPDATE platform_profile
SET texts = jsonb_set(texts, '{footer,copyright}', jsonb_build_object(
        'zh-CN', '© 2026 Astras · 学习项目，资金为模拟',
        'en', '© 2026 Astras · a learning project with simulated funds')),
    version = version + 1,
    updated_by = 'system:migration',
    updated_at = now()
WHERE texts #> '{footer,copyright}' = jsonb_build_object('zh-CN', '© 2026 Astras', 'en', '© 2026 Astras');
