-- Perpetual contracts (requirements §11.7; plan §7.3 task 4): the system
-- account PNL_CLEARING, counterparty of realized profit and loss, which
-- may go negative like DEPOSIT_PENDING and ADJUSTMENT; and the settlement
-- steps derivatives-service asked for, so a repeated request returns the
-- first outcome (the capped moves depend on the balance at the time).

-- +goose Up
ALTER TABLE accounts DROP CONSTRAINT accounts_account_type_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_account_type_check CHECK (account_type IN ('SPOT', 'FUTURES', 'FEE_REVENUE',
    'INSURANCE_FUND', 'DEPOSIT_PENDING', 'WITHDRAWAL_PENDING', 'UNCLAIMED_DEPOSIT', 'FUNDING_CLEARING', 'MARKET_MAKER',
    'GAS_SUPPLY', 'ADJUSTMENT', 'PNL_CLEARING'));
ALTER TABLE accounts DROP CONSTRAINT accounts_non_negative;
ALTER TABLE accounts ADD CONSTRAINT accounts_non_negative
    CHECK (account_type IN ('DEPOSIT_PENDING', 'ADJUSTMENT', 'PNL_CLEARING') OR (available >= 0 AND frozen >= 0));

CREATE TABLE futures_settlements (
    -- The request's key; its journals are futures:<key>:<move>.
    idem_key     text        PRIMARY KEY,
    user_id      uuid        NOT NULL,
    request_hash bytea       NOT NULL,
    reference    text        NOT NULL,
    -- One outcome per move: journal_id, user, insurance, waived.
    outcomes     jsonb       NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX futures_settlements_user ON futures_settlements (user_id, created_at DESC);

-- +goose Down
DROP TABLE futures_settlements;
ALTER TABLE accounts DROP CONSTRAINT accounts_non_negative;
ALTER TABLE accounts ADD CONSTRAINT accounts_non_negative
    CHECK (account_type IN ('DEPOSIT_PENDING', 'ADJUSTMENT') OR (available >= 0 AND frozen >= 0));
ALTER TABLE accounts DROP CONSTRAINT accounts_account_type_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_account_type_check CHECK (account_type IN ('SPOT', 'FUTURES', 'FEE_REVENUE',
    'INSURANCE_FUND', 'DEPOSIT_PENDING', 'WITHDRAWAL_PENDING', 'UNCLAIMED_DEPOSIT', 'FUNDING_CLEARING', 'MARKET_MAKER',
    'GAS_SUPPLY', 'ADJUSTMENT'));
