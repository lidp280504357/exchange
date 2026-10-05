-- What the admin console reads of margin-service (coordination decision
-- of 2026-10-06 03:24 ⑤, review C5): who froze an account and when, when a
-- loan opened, the ledger journal of each hourly charge, and one view of
-- an account's borrows, repayments and interest charges.

-- +goose Up
ALTER TABLE accounts
    -- The administrator who froze the account (FROZEN); '' otherwise.
    ADD COLUMN frozen_by text NOT NULL DEFAULT '',
    ADD COLUMN frozen_at timestamptz;

-- When the loan last went from owing nothing to owing something.
ALTER TABLE loans ADD COLUMN opened_at timestamptz;
UPDATE loans l SET opened_at = coalesce(
    (SELECT min(b.created_at) FROM borrows b WHERE b.user_id = l.user_id AND b.account_type = l.account_type
        AND b.symbol = l.symbol AND b.asset = l.asset AND b.status = 'DONE'),
    l.updated_at)
WHERE l.principal > 0 OR l.interest > 0;

-- The ledger journal that booked an hourly charge (margin-interest:
-- <asset>:<hour unix>, then :<n> for the n-th journal of the hour); a
-- borrow's first hour is the borrow's second journal.
ALTER TABLE interest_charges ADD COLUMN journal_key text NOT NULL DEFAULT '';
UPDATE interest_charges SET journal_key = 'margin-interest:' || asset || ':' || extract(epoch FROM hour)::bigint
WHERE borrow_id IS NULL AND status = 'DONE';

-- A digest of what ReserveOrder was asked: a repeat for the order with
-- other content is refused (review CR); NULL on the older rows.
ALTER TABLE order_reservations ADD COLUMN request_hash bytea;

-- An account's borrows, repayments and interest charges in one list,
-- with the ledger journal of each (its idempotency key).
CREATE VIEW loan_changes AS
SELECT borrow_id AS id, user_id, account_type, symbol, asset, 'BORROW' AS kind, status, amount,
       amount AS principal_part, 0::numeric AS interest_part,
       CASE WHEN order_id IS NULL THEN 'USER' ELSE 'AUTO_BORROW' END AS reason, order_id, NULL::uuid AS liquidation_id,
       'margin:margin-borrow:' || borrow_id || ':0' AS journal_key, created_at
FROM borrows
UNION ALL
SELECT repay_id, user_id, account_type, symbol, asset, 'REPAY', status, interest_repaid + principal_repaid,
       principal_repaid, interest_repaid, reason, order_id, liquidation_id,
       CASE WHEN idem_key LIKE 'trade-repay:%' THEN idem_key ELSE 'margin:margin-repay:' || repay_id || ':0' END, created_at
FROM repays
UNION ALL
SELECT interest_id, user_id, account_type, symbol, asset, 'INTEREST', status, interest, 0, interest, NULL, NULL, NULL,
       CASE WHEN borrow_id IS NOT NULL THEN 'margin:margin-borrow:' || borrow_id || ':1' ELSE journal_key END, created_at
FROM interest_charges;

-- +goose Down
DROP VIEW loan_changes;
ALTER TABLE order_reservations DROP COLUMN request_hash;
ALTER TABLE interest_charges DROP COLUMN journal_key;
ALTER TABLE loans DROP COLUMN opened_at;
ALTER TABLE accounts DROP COLUMN frozen_at, DROP COLUMN frozen_by;
