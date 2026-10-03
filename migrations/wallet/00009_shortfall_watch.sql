-- What the custody checks keep of an asset between checks (review of
-- ebb8aaa, H2): when one first found funds missing beyond its threshold,
-- kept across restarts and cleared by a check that finds none, by the
-- suspension it led to or by a person lifting one; and a difference a
-- person accepted when lifting a suspension, until a time, which the
-- checks do not count as missing.

-- +goose Up
CREATE TABLE shortfall_watch (
    asset          text           PRIMARY KEY,
    suspect_since  timestamptz,
    accepted       numeric(38,18) NOT NULL DEFAULT 0 CHECK (accepted >= 0),
    accepted_until timestamptz,
    accepted_by    text           NOT NULL DEFAULT '',
    CHECK (accepted = 0 OR accepted_until IS NOT NULL)
);

-- +goose Down
DROP TABLE shortfall_watch;
