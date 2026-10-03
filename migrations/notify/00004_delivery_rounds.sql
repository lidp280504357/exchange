-- The review 23 of C5.5 ⑫: a queued mail's retries follow its rounds,
-- counted here (each failed round adds one), not its attempts, which
-- count every provider of the channel, nor the time since it was queued,
-- which a backed-up queue would spend before the first try.

-- +goose Up
ALTER TABLE deliveries ADD COLUMN rounds integer NOT NULL DEFAULT 0 CHECK (rounds >= 0);

-- +goose Down
ALTER TABLE deliveries DROP COLUMN rounds;
