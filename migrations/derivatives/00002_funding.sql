-- Funding (requirements §11.7; plan §7.3 task 6): at each funding time
-- the open positions are snapshotted; once market-data-service has settled
-- the period's rate, every position pays or receives notional x rate at
-- the settlement mark price, payers first so FUNDING_CLEARING never runs
-- short.

-- +goose Up
CREATE TABLE funding_rounds (
    symbol       text        NOT NULL,
    funding_time timestamptz NOT NULL,
    -- SNAPSHOT: positions taken, waiting for the rate; SETTLED; SKIPPED
    -- (no rate came).
    status       text        NOT NULL CHECK (status IN ('SNAPSHOT', 'SETTLED', 'SKIPPED')),
    funding_rate numeric(38,18),
    mark_price   numeric(38,18),
    positions    int         NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    settled_at   timestamptz,
    PRIMARY KEY (symbol, funding_time)
);

-- The positions at the funding time, and what each paid (negative) or
-- received once settled.
CREATE TABLE funding_payments (
    symbol        text           NOT NULL,
    funding_time  timestamptz    NOT NULL,
    position_id   uuid           NOT NULL,
    user_id       uuid           NOT NULL,
    position_side text           NOT NULL,
    -- Signed, as the position was at the funding time.
    quantity      numeric(38,18) NOT NULL,
    margin_mode   text           NOT NULL,
    amount        numeric(38,18),
    -- What the insurance fund paid of a payment the user could not cover.
    insurance     numeric(38,18),
    settled_at    timestamptz,
    PRIMARY KEY (symbol, funding_time, position_id),
    FOREIGN KEY (symbol, funding_time) REFERENCES funding_rounds (symbol, funding_time)
);
CREATE INDEX funding_payments_user ON funding_payments (user_id, funding_time DESC);

-- +goose Down
DROP TABLE funding_payments;
DROP TABLE funding_rounds;
