-- derivatives-service: perpetual contract orders, positions and margin
-- (requirements §5.8, §11.7; plan §7.3 task 5). The money is in the
-- ledger, in each user's FUTURES account (USDT): its frozen balance is the
-- reservations of the open orders here plus the margin of the positions
-- here. Positions are booked at their entry cost, so realized profit is
-- exact (invariant 6).

-- +goose Up
-- A user's choices per contract (defaults until the first change).
CREATE TABLE settings (
    user_id       uuid        NOT NULL,
    symbol        text        NOT NULL,
    position_mode text        NOT NULL CHECK (position_mode IN ('ONE_WAY', 'HEDGE')),
    margin_mode   text        NOT NULL CHECK (margin_mode IN ('CROSS', 'ISOLATED')),
    leverage      int         NOT NULL CHECK (leverage BETWEEN 1 AND 125),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, symbol)
);

CREATE TABLE orders (
    order_id          uuid           PRIMARY KEY,
    client_order_id   text           NOT NULL,
    user_id           uuid           NOT NULL,
    symbol            text           NOT NULL,
    side              text           NOT NULL CHECK (side IN ('BUY', 'SELL')),
    position_side     text           NOT NULL CHECK (position_side IN ('BOTH', 'LONG', 'SHORT')),
    type              text           NOT NULL CHECK (type IN ('LIMIT', 'MARKET')),
    time_in_force     text           NOT NULL CHECK (time_in_force IN ('GTC', 'IOC', 'FOK', 'POST_ONLY')),
    -- The limit price; for a market order its protection price (it goes
    -- to the engine as an IOC or FOK limit order at that price).
    price             numeric(38,18) NOT NULL CHECK (price > 0),
    quantity          numeric(38,18) NOT NULL CHECK (quantity > 0),
    reduce_only       boolean        NOT NULL,
    -- USER, or LIQUIDATION for the liquidation engine's orders (task 7).
    kind              text           NOT NULL DEFAULT 'USER',
    leverage          int            NOT NULL,
    margin_mode       text           NOT NULL CHECK (margin_mode IN ('CROSS', 'ISOLATED')),
    maker_fee_rate    numeric(38,18) NOT NULL,
    taker_fee_rate    numeric(38,18) NOT NULL,
    lot_size          numeric(38,18) NOT NULL,
    -- What an opening order froze per lot (0 for closing orders): initial
    -- margin and the taker fee at its price, rounded up.
    margin_per_lot    numeric(38,18) NOT NULL CHECK (margin_per_lot >= 0),
    fee_per_lot       numeric(38,18) NOT NULL CHECK (fee_per_lot >= 0),
    -- The quantity whose reservation fills used; the unfilled rest is
    -- unfrozen once the order is finished (released).
    consumed_quantity numeric(38,18) NOT NULL DEFAULT 0,
    released          boolean        NOT NULL DEFAULT false,
    status            text           NOT NULL CHECK (status IN ('NEW', 'OPEN', 'PARTIALLY_FILLED', 'FILLED', 'CANCELED',
                                         'REJECTED', 'EXPIRED')),
    freeze_state      text           NOT NULL CHECK (freeze_state IN ('PENDING', 'FROZEN', 'NONE')),
    cancel_requested  boolean        NOT NULL DEFAULT false,
    cancel_reason     text           NOT NULL DEFAULT '',
    reject_reason     text           NOT NULL DEFAULT '',
    filled_quantity   numeric(38,18) NOT NULL DEFAULT 0,
    filled_quote      numeric(38,18) NOT NULL DEFAULT 0,
    fee               numeric(38,18) NOT NULL DEFAULT 0,
    realized_pnl      numeric(38,18) NOT NULL DEFAULT 0,
    -- The engine sequence of the last update applied.
    sequence          bigint         NOT NULL DEFAULT 0,
    created_at        timestamptz    NOT NULL,
    updated_at        timestamptz    NOT NULL,
    UNIQUE (user_id, client_order_id),
    CHECK (consumed_quantity >= 0 AND consumed_quantity <= quantity)
);
CREATE INDEX orders_user ON orders (user_id, order_id DESC);
CREATE INDEX orders_active ON orders (user_id, symbol) WHERE status IN ('NEW', 'OPEN', 'PARTIALLY_FILLED');
CREATE INDEX orders_pending ON orders (created_at) WHERE freeze_state = 'PENDING';
CREATE INDEX orders_unreleased ON orders (updated_at)
    WHERE NOT released AND freeze_state = 'FROZEN' AND status IN ('FILLED', 'CANCELED', 'REJECTED', 'EXPIRED');

CREATE TABLE positions (
    position_id   uuid           PRIMARY KEY,
    user_id       uuid           NOT NULL,
    symbol        text           NOT NULL,
    position_side text           NOT NULL CHECK (position_side IN ('BOTH', 'LONG', 'SHORT')),
    -- Signed: positive long, negative short.
    quantity      numeric(38,18) NOT NULL,
    entry_cost    numeric(38,18) NOT NULL CHECK (entry_cost >= 0),
    margin        numeric(38,18) NOT NULL CHECK (margin >= 0),
    margin_mode   text           NOT NULL CHECK (margin_mode IN ('CROSS', 'ISOLATED')),
    leverage      int            NOT NULL,
    realized_pnl  numeric(38,18) NOT NULL DEFAULT 0,
    funding       numeric(38,18) NOT NULL DEFAULT 0,
    fees          numeric(38,18) NOT NULL DEFAULT 0,
    version       bigint         NOT NULL DEFAULT 0,
    opened_at     timestamptz,
    updated_at    timestamptz    NOT NULL,
    UNIQUE (user_id, symbol, position_side),
    CHECK (position_side <> 'LONG' OR quantity >= 0),
    CHECK (position_side <> 'SHORT' OR quantity <= 0),
    CHECK (quantity <> 0 OR entry_cost = 0)
);
CREATE INDEX positions_open ON positions (symbol) WHERE quantity <> 0;

-- Each side of a contract trade, once applied.
CREATE TABLE fills (
    trade_id        uuid           NOT NULL,
    side            text           NOT NULL CHECK (side IN ('BUY', 'SELL')),
    order_id        uuid           NOT NULL,
    user_id         uuid           NOT NULL,
    symbol          text           NOT NULL,
    position_side   text           NOT NULL,
    maker           boolean        NOT NULL,
    price           numeric(38,18) NOT NULL,
    quantity        numeric(38,18) NOT NULL,
    closed_quantity numeric(38,18) NOT NULL,
    fee             numeric(38,18) NOT NULL,
    fee_waived      numeric(38,18) NOT NULL DEFAULT 0,
    realized_pnl    numeric(38,18) NOT NULL,
    -- What the insurance fund paid of the loss.
    insurance       numeric(38,18) NOT NULL DEFAULT 0,
    liquidation     boolean        NOT NULL DEFAULT false,
    sequence        bigint         NOT NULL,
    executed_at     timestamptz    NOT NULL,
    -- False while the ledger refused the settlement (pending_settlements).
    settled         boolean        NOT NULL,
    PRIMARY KEY (trade_id, side)
);
CREATE INDEX fills_user ON fills (user_id, executed_at DESC, trade_id);
CREATE INDEX fills_order ON fills (order_id);

-- Settlements the ledger refused (an insurance fund too small for a
-- shortfall): the position moved on, the money waits. Retried until the
-- ledger books them.
CREATE TABLE pending_settlements (
    idem_key    text        PRIMARY KEY,
    user_id     uuid        NOT NULL,
    request     jsonb       NOT NULL,
    -- The position a partial FREEZE (move freeze_move, -1 for none) in the
    -- request gives margin to.
    position_id uuid,
    freeze_move int         NOT NULL DEFAULT -1,
    -- The fill it settles, if any.
    trade_id    uuid,
    side        text,
    attempts    int         NOT NULL DEFAULT 1,
    last_error  text        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Contracts under reduce-only (§11.7): a degradation sets it, a person
-- lifts it.
CREATE TABLE contract_states (
    symbol      text        PRIMARY KEY,
    reduce_only boolean     NOT NULL,
    reason      text        NOT NULL,
    since       timestamptz NOT NULL,
    lifted_by   text        NOT NULL DEFAULT '',
    lifted_at   timestamptz,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Results of the reconciliation runs (invariant 6).
CREATE TABLE reconciliation_runs (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    started_at  timestamptz NOT NULL,
    finished_at timestamptz NOT NULL DEFAULT now(),
    check_name  text        NOT NULL,
    mismatches  int         NOT NULL,
    details     jsonb       NOT NULL DEFAULT '[]'
);
CREATE INDEX reconciliation_runs_started ON reconciliation_runs (started_at);

-- +goose Down
DROP TABLE reconciliation_runs;
DROP TABLE contract_states;
DROP TABLE pending_settlements;
DROP TABLE fills;
DROP TABLE positions;
DROP TABLE orders;
DROP TABLE settings;
