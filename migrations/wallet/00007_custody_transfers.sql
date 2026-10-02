-- A custodian's deposit is one transfer (network, hash, address) whatever
-- trade ID reports it: a backfill entered with a wrong trade ID and the
-- custodian's late callback under the right one must never both be booked
-- (admin console C5.5 ①; the callback path matches the transfer first).
-- The platform's own chain deposits keep their key (network, tx_hash,
-- log_index): one transaction may pay an address in several logs.

-- +goose Up
CREATE UNIQUE INDEX deposits_custody_transfer_idx ON deposits (network, lower(tx_hash), lower(address))
    WHERE provider_tx_id IS NOT NULL AND tx_hash <> '';

-- +goose Down
DROP INDEX deposits_custody_transfer_idx;
