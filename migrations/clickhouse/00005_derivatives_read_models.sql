-- Read models of perpetual contracts (implementation plan §7.3 task 10),
-- projected by analytics-consumer from derivatives.position.events and
-- derivatives.liquidation.events. Contract orders and trades
-- (derivatives.order.events, derivatives.trade.events) go into orders,
-- order_updates and trades with the spot ones; the symbol tells them
-- apart. All tables are ReplacingMergeTree: query with FINAL.

-- +goose NO TRANSACTION
-- +goose Up

-- Every position in its latest state (the Position snapshot of any
-- position event; version grows with each change). A position keeps its
-- ID when it closes and opens again.
CREATE TABLE IF NOT EXISTS derivatives_positions
(
    position_id   UUID,
    user_id       UUID,
    symbol        LowCardinality(String),
    position_side LowCardinality(String),
    quantity      Decimal128(18),
    entry_price   Nullable(Decimal128(18)),
    entry_cost    Decimal128(18),
    margin        Decimal128(18),
    margin_mode   LowCardinality(String),
    leverage      Int32,
    realized_pnl  Decimal128(18),
    funding       Decimal128(18),
    updated_at    DateTime64(3, 'UTC'),
    version       UInt64
)
ENGINE = ReplacingMergeTree(version)
ORDER BY position_id;

-- One row per settled side of a contract trade (FillSettled); notional is
-- price × quantity.
CREATE TABLE IF NOT EXISTS derivatives_fills
(
    trade_id        UUID,
    order_id        UUID,
    user_id         UUID,
    symbol          LowCardinality(String),
    side            LowCardinality(String),
    position_side   LowCardinality(String),
    maker           Bool,
    price           Decimal128(18),
    quantity        Decimal128(18),
    notional        Decimal128(18),
    closed_quantity Decimal128(18),
    fee             Decimal128(18),
    realized_pnl    Decimal128(18),
    liquidation     Bool,
    executed_at     DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(executed_at)
ORDER BY (symbol, executed_at, trade_id, order_id);

-- Funding per position and settlement (FundingPaid): amount is what the
-- position received (positive) or paid.
CREATE TABLE IF NOT EXISTS derivatives_funding
(
    position_id   UUID,
    user_id       UUID,
    symbol        LowCardinality(String),
    position_side LowCardinality(String),
    margin_mode   LowCardinality(String),
    funding_time  DateTime('UTC'),
    funding_rate  Decimal128(18),
    mark_price    Decimal128(18),
    amount        Decimal128(18),
    settled_at    DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(funding_time)
ORDER BY (symbol, funding_time, position_id);

-- Liquidation steps: WARNING (margin balance at most 1.2 × maintenance),
-- STARTED (taken over), FILLED (a liquidation order or an
-- auto-deleveraging closed part of a taken-over position; adl tells which)
-- and ADL (a counterparty's position closed by auto-deleveraging). symbol
-- is empty for a warning of a cross account; the amounts a step does not
-- report are 0.
CREATE TABLE IF NOT EXISTS derivatives_liquidations
(
    event_id           UUID,
    kind               LowCardinality(String),
    user_id            UUID,
    symbol             LowCardinality(String),
    position_side      LowCardinality(String),
    cross_margin       Bool,
    adl                Bool,
    trade_id           String,
    price              Decimal128(18),
    quantity           Decimal128(18),
    realized_pnl       Decimal128(18),
    insurance_paid     Decimal128(18),
    mark_price         Decimal128(18),
    bankruptcy_price   Decimal128(18),
    margin_balance     Decimal128(18),
    maintenance_margin Decimal128(18),
    occurred_at        DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(occurred_at)
ORDER BY (occurred_at, event_id);

-- +goose Down
DROP TABLE IF EXISTS derivatives_liquidations;
DROP TABLE IF EXISTS derivatives_funding;
DROP TABLE IF EXISTS derivatives_fills;
DROP TABLE IF EXISTS derivatives_positions;
