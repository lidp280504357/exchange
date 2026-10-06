-- Traditional Chinese (design 2026-10-06 繁体中文 §2.4): an article may
-- carry a zh-TW text beside the Simplified one (required) and the English
-- one; the sites fall back to the Simplified. The broadcasts keep their
-- texts by locale in jsonb and take zh-TW as they are.

-- +goose Up
ALTER TABLE article_texts
    DROP CONSTRAINT article_texts_locale_check,
    ADD CONSTRAINT article_texts_locale_check CHECK (locale IN ('zh-CN', 'zh-TW', 'en'));

-- +goose Down
DELETE FROM article_texts WHERE locale = 'zh-TW';
ALTER TABLE article_texts
    DROP CONSTRAINT article_texts_locale_check,
    ADD CONSTRAINT article_texts_locale_check CHECK (locale IN ('zh-CN', 'en'));
