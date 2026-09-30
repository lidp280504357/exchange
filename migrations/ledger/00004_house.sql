-- HOUSE's virtual liquidity (ADR-0013, ADR-0015): trades against HOUSE
-- settle as HOUSE_TRADE_SETTLE on the system account MARKET_MAKER, which
-- may now go below zero like the other counterparties (an internal asset
-- HOUSE sold; a backed asset only by an incident, which alerts). The
-- trade records keep the side HOUSE took.

-- +goose Up
ALTER TABLE journals DROP CONSTRAINT journals_entry_type_check;
ALTER TABLE journals ADD CONSTRAINT journals_entry_type_check CHECK (entry_type IN ('DEPOSIT_CREDIT', 'WITHDRAW_FREEZE',
    'WITHDRAW_SETTLE', 'WITHDRAW_UNFREEZE', 'ORDER_FREEZE', 'ORDER_UNFREEZE', 'TRADE_SETTLE', 'TRADE_FEE', 'ACCOUNT_TRANSFER',
    'FUNDING_PAYMENT', 'REALIZED_PNL', 'LIQUIDATION_SETTLE', 'INSURANCE_CONTRIBUTION', 'ADL_SETTLE', 'INTERNAL_TRANSFER',
    'MANUAL_ADJUSTMENT', 'HOUSE_TRADE_SETTLE'));
ALTER TABLE accounts DROP CONSTRAINT accounts_non_negative;
ALTER TABLE accounts ADD CONSTRAINT accounts_non_negative
    CHECK (account_type IN ('DEPOSIT_PENDING', 'ADJUSTMENT', 'PNL_CLEARING', 'MARKET_MAKER') OR (available >= 0 AND frozen >= 0));
-- BUY or SELL when HOUSE was the counterparty, '' between users.
ALTER TABLE trades ADD COLUMN house_side text NOT NULL DEFAULT '' CHECK (house_side IN ('', 'BUY', 'SELL'));

-- +goose Down
ALTER TABLE trades DROP COLUMN house_side;
ALTER TABLE accounts DROP CONSTRAINT accounts_non_negative;
ALTER TABLE accounts ADD CONSTRAINT accounts_non_negative
    CHECK (account_type IN ('DEPOSIT_PENDING', 'ADJUSTMENT', 'PNL_CLEARING') OR (available >= 0 AND frozen >= 0));
ALTER TABLE journals DROP CONSTRAINT journals_entry_type_check;
ALTER TABLE journals ADD CONSTRAINT journals_entry_type_check CHECK (entry_type IN ('DEPOSIT_CREDIT', 'WITHDRAW_FREEZE',
    'WITHDRAW_SETTLE', 'WITHDRAW_UNFREEZE', 'ORDER_FREEZE', 'ORDER_UNFREEZE', 'TRADE_SETTLE', 'TRADE_FEE', 'ACCOUNT_TRANSFER',
    'FUNDING_PAYMENT', 'REALIZED_PNL', 'LIQUIDATION_SETTLE', 'INSURANCE_CONTRIBUTION', 'ADL_SETTLE', 'INTERNAL_TRANSFER',
    'MANUAL_ADJUSTMENT'));
