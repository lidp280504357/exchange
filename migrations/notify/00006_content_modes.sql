-- Content by mode (user decision 2026-10-04 18:45, design 2026-10-04
-- §4.4): an article shows in test mode (TEST), when live (FORMAL) or in
-- both (BOTH, as every article so far). The sites show only what fits the
-- platform profile's test mode, so switching it swaps the content. A slug
-- may have one article per mode: a test and a formal page of the same
-- slug, or one for both.

-- +goose Up
ALTER TABLE articles ADD COLUMN modes text NOT NULL DEFAULT 'BOTH' CHECK (modes IN ('TEST', 'FORMAL', 'BOTH'));
ALTER TABLE articles DROP CONSTRAINT articles_section_slug_key;
CREATE UNIQUE INDEX articles_slug_test ON articles (section, slug) WHERE modes IN ('TEST', 'BOTH');
CREATE UNIQUE INDEX articles_slug_formal ON articles (section, slug) WHERE modes IN ('FORMAL', 'BOTH');

-- +goose Down
-- A slug's test page goes, so that one article per slug is left.
DELETE FROM articles WHERE modes = 'TEST' AND (section, slug) IN (SELECT section, slug FROM articles WHERE modes = 'FORMAL');
DROP INDEX articles_slug_formal;
DROP INDEX articles_slug_test;
ALTER TABLE articles ADD CONSTRAINT articles_section_slug_key UNIQUE (section, slug);
ALTER TABLE articles DROP COLUMN modes;
