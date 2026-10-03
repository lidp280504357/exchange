-- Where a custodian's callbacks come from (2026-10-03): the real gateway
-- publishes no addresses, so UDUN_CALLBACK_ALLOWED_IPS may be empty and
-- callbacks are taken on their signature alone; every address a
-- callback's deliveries came from is kept (at most 8), refused ones too,
-- for the allow list to be drawn from once real callbacks have come.

-- +goose Up
ALTER TABLE custody_callbacks ADD COLUMN remote_ips text[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE custody_callbacks DROP COLUMN remote_ips;
