-- The stand-in custodian and the hidden test asset (ADR-0017): the
-- stand-in gateway's second merchant UDUNMOCK serves networks too, only of
-- hidden assets, and an asset may be hidden: in no public list, without
-- pairs or contracts, its deposits and withdrawals open only to users
-- eligible for TEST_ASSETS (the end-to-end tests' accounts).

-- +goose Up
ALTER TABLE networks
    DROP CONSTRAINT networks_provider_check,
    ADD CONSTRAINT networks_provider_check CHECK (provider IN ('', 'UDUN', 'UDUNMOCK'));
ALTER TABLE assets ADD COLUMN hidden boolean NOT NULL DEFAULT false;

-- +goose Down
-- Fails while a network of UDUNMOCK is left (TUSD's TRON-TEST on the test
-- server, review AQ): delete it first (DELETE FROM networks WHERE provider
-- = 'UDUNMOCK') and take TUSD out of deploy/instruments/test.json.
ALTER TABLE assets DROP COLUMN hidden;
ALTER TABLE networks
    DROP CONSTRAINT networks_provider_check,
    ADD CONSTRAINT networks_provider_check CHECK (provider IN ('', 'UDUN'));
