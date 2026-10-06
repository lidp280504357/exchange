-- The reference market's statistics of the perpetual contracts it trades
-- (design 2026-10-06 §3.3): open interest, the long/short ratios, taker
-- buy and sell volumes and the basis at each period, and the settled
-- funding rates, for the contracts' data panel. Binance's figures as
-- they are, display only; a series keeps the 500 points a chart may ask
-- for and nothing older than 30 days (Binance keeps 30 days).

-- +goose Up
CREATE TABLE futures_stats (
    symbol text        NOT NULL,
    metric text        NOT NULL CHECK (metric IN ('open_interest', 'long_short_account', 'top_long_short_account',
                           'top_long_short_position', 'taker_ratio', 'basis', 'funding')),
    -- A funding rate is one point a settlement, with no period.
    period text        NOT NULL CHECK (period IN ('5m', '15m', '1h', '4h', '1d', '')),
    -- The source's time of the point: a snapshot's, or the start of the
    -- period a volume was traded in.
    ts     timestamptz NOT NULL,
    -- The point's values by name, decimal strings (the API's "values").
    data   jsonb       NOT NULL CHECK (jsonb_typeof(data) = 'object'),
    PRIMARY KEY (symbol, metric, period, ts),
    CHECK ((metric = 'funding') = (period = ''))
);
CREATE INDEX futures_stats_ts ON futures_stats (ts);

-- +goose Down
DROP TABLE futures_stats;
