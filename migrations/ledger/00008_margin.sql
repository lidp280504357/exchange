-- Margin trading (margin design 2026-10-06 §3; E0 contract §7): a user's
-- margin account holds three rows per asset, the assets (MARGIN_CROSS,
-- MARGIN_ISOLATED: available and frozen, never below zero), the principal
-- owed (..._DEBT) and the interest owed (..._INTEREST), the debts as
-- negative balances. A loan balances inside the user's own accounts
-- (MARGIN_BORROW: assets +a, debt -a): HOUSE lends, and what it has lent
-- is minus the debt rows' sum, not an account of its own. Interest is
-- income when charged (MARGIN_INTEREST: interest rows -i, HOUSE's
-- MARGIN_INTEREST_INCOME +i); a repayment balances inside the user's
-- accounts again (MARGIN_REPAY). scope is an isolated account's pair, ''
-- for every other account. margin_postings keeps margin-service's
-- requests, one journal per move, so that a repeated key replays them.
-- Administrators' holds stay on SPOT: a margin account is frozen as a
-- whole by margin-service (status FROZEN), and a liquidation must be able
-- to sell everything the account holds.

-- +goose Up
ALTER TABLE accounts ADD COLUMN scope text NOT NULL DEFAULT '';
ALTER TABLE accounts DROP CONSTRAINT accounts_owner_type_owner_id_account_type_asset_key;
ALTER TABLE accounts ADD CONSTRAINT accounts_owner_scope_asset_key UNIQUE (owner_type, owner_id, account_type, scope, asset);
ALTER TABLE accounts ADD CONSTRAINT accounts_scope_check
    CHECK ((scope <> '') = (account_type IN ('MARGIN_ISOLATED', 'MARGIN_ISOLATED_DEBT', 'MARGIN_ISOLATED_INTEREST')));
ALTER TABLE accounts DROP CONSTRAINT accounts_account_type_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_account_type_check CHECK (account_type IN ('SPOT', 'FUTURES', 'FEE_REVENUE',
    'INSURANCE_FUND', 'DEPOSIT_PENDING', 'WITHDRAWAL_PENDING', 'UNCLAIMED_DEPOSIT', 'FUNDING_CLEARING', 'MARKET_MAKER',
    'GAS_SUPPLY', 'ADJUSTMENT', 'PNL_CLEARING', 'MARGIN_CROSS', 'MARGIN_CROSS_DEBT', 'MARGIN_CROSS_INTEREST',
    'MARGIN_ISOLATED', 'MARGIN_ISOLATED_DEBT', 'MARGIN_ISOLATED_INTEREST', 'MARGIN_INTEREST_INCOME'));
ALTER TABLE accounts DROP CONSTRAINT accounts_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_check CHECK ((owner_type = 'USER') = (account_type IN ('SPOT', 'FUTURES',
    'MARGIN_CROSS', 'MARGIN_CROSS_DEBT', 'MARGIN_CROSS_INTEREST', 'MARGIN_ISOLATED', 'MARGIN_ISOLATED_DEBT',
    'MARGIN_ISOLATED_INTEREST')));
-- Invariants 3 and 9: balances never below zero, but for the
-- counterparties allowed to; debt and interest rows never above zero,
-- with nothing frozen.
ALTER TABLE accounts DROP CONSTRAINT accounts_non_negative;
ALTER TABLE accounts ADD CONSTRAINT accounts_non_negative CHECK (
    account_type IN ('DEPOSIT_PENDING', 'ADJUSTMENT', 'PNL_CLEARING', 'MARKET_MAKER')
    OR (account_type IN ('MARGIN_CROSS_DEBT', 'MARGIN_CROSS_INTEREST', 'MARGIN_ISOLATED_DEBT', 'MARGIN_ISOLATED_INTEREST')
        AND available <= 0 AND frozen = 0)
    OR (account_type NOT IN ('MARGIN_CROSS_DEBT', 'MARGIN_CROSS_INTEREST', 'MARGIN_ISOLATED_DEBT', 'MARGIN_ISOLATED_INTEREST')
        AND available >= 0 AND frozen >= 0));
ALTER TABLE journals DROP CONSTRAINT journals_entry_type_check;
ALTER TABLE journals ADD CONSTRAINT journals_entry_type_check CHECK (entry_type IN ('DEPOSIT_CREDIT', 'WITHDRAW_FREEZE',
    'WITHDRAW_SETTLE', 'WITHDRAW_UNFREEZE', 'ORDER_FREEZE', 'ORDER_UNFREEZE', 'TRADE_SETTLE', 'TRADE_FEE', 'ACCOUNT_TRANSFER',
    'FUNDING_PAYMENT', 'REALIZED_PNL', 'LIQUIDATION_SETTLE', 'INSURANCE_CONTRIBUTION', 'ADL_SETTLE', 'INTERNAL_TRANSFER',
    'MANUAL_ADJUSTMENT', 'HOUSE_TRADE_SETTLE', 'ADMIN_FREEZE', 'ADMIN_UNFREEZE', 'MARGIN_TRANSFER_IN', 'MARGIN_TRANSFER_OUT',
    'MARGIN_BORROW', 'MARGIN_INTEREST', 'MARGIN_REPAY', 'MARGIN_TRADE_SETTLE', 'MARGIN_LIQUIDATE'));

CREATE TABLE margin_postings (
    -- The request's key; its journals are margin:<key>:<move>, or
    -- margin-interest:<key> for an hour's interest.
    idem_key     text        PRIMARY KEY,
    -- Empty for an hour's interest, which spans users.
    user_id      text        NOT NULL,
    request_hash bytea       NOT NULL,
    reference    text        NOT NULL,
    -- One journal ID per move ('' for none).
    journals     jsonb       NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX margin_postings_user ON margin_postings (user_id, created_at DESC);

-- +goose Down
DROP TABLE margin_postings;
ALTER TABLE journals DROP CONSTRAINT journals_entry_type_check;
ALTER TABLE journals ADD CONSTRAINT journals_entry_type_check CHECK (entry_type IN ('DEPOSIT_CREDIT', 'WITHDRAW_FREEZE',
    'WITHDRAW_SETTLE', 'WITHDRAW_UNFREEZE', 'ORDER_FREEZE', 'ORDER_UNFREEZE', 'TRADE_SETTLE', 'TRADE_FEE', 'ACCOUNT_TRANSFER',
    'FUNDING_PAYMENT', 'REALIZED_PNL', 'LIQUIDATION_SETTLE', 'INSURANCE_CONTRIBUTION', 'ADL_SETTLE', 'INTERNAL_TRANSFER',
    'MANUAL_ADJUSTMENT', 'HOUSE_TRADE_SETTLE', 'ADMIN_FREEZE', 'ADMIN_UNFREEZE'));
ALTER TABLE accounts DROP CONSTRAINT accounts_non_negative;
ALTER TABLE accounts ADD CONSTRAINT accounts_non_negative
    CHECK (account_type IN ('DEPOSIT_PENDING', 'ADJUSTMENT', 'PNL_CLEARING', 'MARKET_MAKER') OR (available >= 0 AND frozen >= 0));
ALTER TABLE accounts DROP CONSTRAINT accounts_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_check CHECK ((owner_type = 'USER') = (account_type IN ('SPOT', 'FUTURES')));
ALTER TABLE accounts DROP CONSTRAINT accounts_account_type_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_account_type_check CHECK (account_type IN ('SPOT', 'FUTURES', 'FEE_REVENUE',
    'INSURANCE_FUND', 'DEPOSIT_PENDING', 'WITHDRAWAL_PENDING', 'UNCLAIMED_DEPOSIT', 'FUNDING_CLEARING', 'MARKET_MAKER',
    'GAS_SUPPLY', 'ADJUSTMENT', 'PNL_CLEARING'));
ALTER TABLE accounts DROP CONSTRAINT accounts_scope_check;
ALTER TABLE accounts DROP CONSTRAINT accounts_owner_scope_asset_key;
ALTER TABLE accounts ADD CONSTRAINT accounts_owner_type_owner_id_account_type_asset_key UNIQUE (owner_type, owner_id, account_type, asset);
ALTER TABLE accounts DROP COLUMN scope;
