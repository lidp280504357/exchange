-- When each simulated market last reported its target (its heartbeat,
-- ASTRA design §9), so that a restarted market-data-service still
-- watches a market that went silent before it: the guard halts it a
-- minute after this time while sim.halt_on_loss is on. Delete a row to
-- forget a simulated market for good.

-- +goose Up
CREATE TABLE sim_heartbeats (
    symbol  text        PRIMARY KEY,
    last_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE sim_heartbeats;
