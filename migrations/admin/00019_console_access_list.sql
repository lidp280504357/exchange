-- The console's access restriction (design 2026-10-02, N1): while on
-- (admin.access_restriction), admin-service answers its API only to the
-- addresses of the list - IPv4 or IPv6 addresses or CIDR prefixes, as
-- admin-service normalized them, at most 50 - and refuses the others
-- (ADMIN_ACCESS_DENIED). Off keeps the list.

-- +goose Up
ALTER TABLE console_access
    ADD COLUMN access_restriction boolean NOT NULL DEFAULT false,
    ADD COLUMN access_allowlist   text[]  NOT NULL DEFAULT '{}' CHECK (cardinality(access_allowlist) <= 50),
    ADD CHECK (NOT access_restriction OR cardinality(access_allowlist) > 0);

-- +goose Down
ALTER TABLE console_access DROP COLUMN access_allowlist, DROP COLUMN access_restriction;
