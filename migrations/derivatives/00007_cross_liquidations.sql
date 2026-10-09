-- A cross account's liquidation (coin-margined design 2026-10-06 §0, C68):
-- as Binance does, what it leaves goes to the insurance fund as a
-- liquidation clearance fee once its cross positions are closed and
-- settled, and the account ends at zero. Kept from the take-over until the
-- fee is booked: while it is open nothing takes from the account's
-- available balance (no cross order, no opening order on a contract settled
-- in its asset, no isolated margin added, no transfer out of FUTURES).

-- +goose Up
CREATE TABLE cross_liquidations (
    liquidation_id uuid           PRIMARY KEY,
    user_id        uuid           NOT NULL,
    asset          text           NOT NULL,
    started_at     timestamptz    NOT NULL,
    -- The cross equity at the marks when it was taken over: the most the
    -- fee takes.
    equity         numeric(38,18) NOT NULL,
    -- What the account held then: the available balance, its cross orders'
    -- reservations and its cross positions' margin.
    balance        numeric(38,18) NOT NULL,
    -- What its cross positions' fills and funding added or took since
    -- (realized result, the fund's part of a loss, less fees; funding as
    -- booked): balance + flows is what the liquidation left.
    flows          numeric(38,18) NOT NULL DEFAULT 0,
    status         text           NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN', 'DONE')),
    -- The clearance fee once worked out, booked under the key
    -- cross-liquidation:<liquidation_id>.
    fee            numeric(38,18) CHECK (fee >= 0),
    done_at        timestamptz,
    CHECK (status = 'OPEN' OR (fee IS NOT NULL AND done_at IS NOT NULL))
);
CREATE UNIQUE INDEX cross_liquidations_open ON cross_liquidations (user_id, asset) WHERE status = 'OPEN';
CREATE INDEX cross_liquidations_user ON cross_liquidations (user_id, started_at DESC);

-- +goose Down
DROP TABLE cross_liquidations;
