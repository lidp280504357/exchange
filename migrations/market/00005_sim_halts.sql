-- The pairs halted because their simulated market went silent (ASTRA
-- design §9: market-sim's heartbeat lost for 60 seconds while
-- sim.halt_on_loss is on), recorded before the halt so that a restart
-- resumes them too; apart from feed_halts, which the reference feed's
-- guard resumes.

-- +goose Up
CREATE TABLE sim_halts (
    symbol    text        PRIMARY KEY,
    halted_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE sim_halts;
