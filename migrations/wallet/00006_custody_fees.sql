-- The custodian's fees on the withdrawals it sends (review ④, 2026-10-03):
-- * a fee a person must look at before it is booked is HELD with why
--   (above five times the network's withdrawal fee or the amount sent, or
--   a token whose fee unit no person has confirmed), then booked as they
--   decide (BOOKABLE, possibly in another asset or amount) or WRITTEN_OFF
--   (not charged in the coin balances), with who, when and why;
-- * how the custodian counts its fee on a network (custody_fee_units), as
--   a person confirmed it against a real withdrawal: in the withdrawal's
--   asset (SELF), in the chain's own coin (MAIN) or outside the coin
--   balances (OUTSIDE).

-- +goose Up
ALTER TABLE chain_fees
    ADD COLUMN status      text NOT NULL DEFAULT 'BOOKABLE' CHECK (status IN ('BOOKABLE', 'HELD', 'WRITTEN_OFF')),
    ADD COLUMN hold_reason text NOT NULL DEFAULT '',
    ADD COLUMN resolved_by text NOT NULL DEFAULT '',
    ADD COLUMN resolution  text NOT NULL DEFAULT '',
    ADD COLUMN resolved_at timestamptz,
    ADD CONSTRAINT chain_fees_booked_status_check CHECK (status = 'BOOKABLE' OR booked_at IS NULL),
    ADD CONSTRAINT chain_fees_held_check CHECK (status = 'BOOKABLE' AND resolved_at IS NULL OR hold_reason <> ''),
    ADD CONSTRAINT chain_fees_resolved_check CHECK (status <> 'WRITTEN_OFF' OR resolved_at IS NOT NULL);
CREATE INDEX chain_fees_held_idx ON chain_fees (created_at) WHERE status = 'HELD';
CREATE INDEX chain_fees_reference_idx ON chain_fees (reference);

CREATE TABLE custody_fee_units (
    provider     text        NOT NULL,
    asset        text        NOT NULL,
    network      text        NOT NULL,
    unit         text        NOT NULL CHECK (unit IN ('SELF', 'MAIN', 'OUTSIDE')),
    confirmed_by text        NOT NULL,
    reason       text        NOT NULL,
    confirmed_at timestamptz NOT NULL,
    PRIMARY KEY (provider, asset, network)
);

-- +goose Down
DROP TABLE custody_fee_units;
DROP INDEX chain_fees_reference_idx;
DROP INDEX chain_fees_held_idx;
ALTER TABLE chain_fees
    DROP CONSTRAINT chain_fees_resolved_check,
    DROP CONSTRAINT chain_fees_held_check,
    DROP CONSTRAINT chain_fees_booked_status_check,
    DROP COLUMN resolved_at,
    DROP COLUMN resolution,
    DROP COLUMN resolved_by,
    DROP COLUMN hold_reason,
    DROP COLUMN status;
