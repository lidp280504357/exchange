-- Two more content sections (design 2026-10-04 §4.4): LEGAL, the fixed
-- legal and information pages (terms, privacy, risk, fees, about,
-- contact), and HOME, the home page's blocks (home-hero). The sites
-- bundle a draft of each; an operator's published article replaces it,
-- an archived one hides it. notification-service keeps each section to
-- its slugs.

-- +goose Up
ALTER TABLE articles
    DROP CONSTRAINT articles_section_check,
    ADD CONSTRAINT articles_section_check CHECK (section IN ('ANNOUNCEMENT', 'HELP', 'LEGAL', 'HOME'));

-- +goose Down
DELETE FROM articles WHERE section IN ('LEGAL', 'HOME');
ALTER TABLE articles
    DROP CONSTRAINT articles_section_check,
    ADD CONSTRAINT articles_section_check CHECK (section IN ('ANNOUNCEMENT', 'HELP'));
