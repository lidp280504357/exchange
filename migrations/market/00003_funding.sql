-- Funding of perpetual contracts (requirements §11.7; plan §7.3 task 3):
-- each funding period's premium index samples, summed while it runs, and
-- its rate once it has ended. derivatives-service settles the payments
-- from the settled rows; the estimate of a running period is computed
-- from its samples.

-- +goose Up
CREATE TABLE funding_periods (
    symbol        text           NOT NULL,
    -- The end of the period, when it settles.
    funding_time  timestamptz    NOT NULL,
    premium_sum   numeric(38,18) NOT NULL,
    samples       bigint         NOT NULL CHECK (samples >= 0),
    -- Set together when the period settles.
    funding_rate  numeric(38,18),
    premium       numeric(38,18),
    interest_rate numeric(38,18),
    mark_price    numeric(38,18),
    index_price   numeric(38,18),
    settled_at    timestamptz,
    updated_at    timestamptz    NOT NULL DEFAULT now(),
    PRIMARY KEY (symbol, funding_time),
    CHECK ((settled_at IS NULL) = (funding_rate IS NULL)),
    CHECK (settled_at IS NULL OR (premium IS NOT NULL AND interest_rate IS NOT NULL AND mark_price > 0 AND index_price > 0))
);
CREATE INDEX funding_periods_unsettled ON funding_periods (funding_time) WHERE settled_at IS NULL;

-- +goose Down
DROP TABLE funding_periods;
