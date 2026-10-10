-- M1, review LI ②: the retention deletes the platform's candles of less
-- than a day once they closed before its cutoff, a batch at a time; it
-- bounds their open time by the cutoff first, which this index serves
-- (the primary key starts with the symbol).

-- +goose Up
CREATE INDEX candles_intraday_open ON candles (open_time)
    WHERE interval IN ('1m', '3m', '5m', '15m', '30m', '1h', '2h', '4h', '6h', '12h');

-- +goose Down
DROP INDEX candles_intraday_open;
