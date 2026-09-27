-- Record how a step-up was proven: rebinding an identity requires a
-- step-up through the other identity (§6.4).

-- +goose Up
ALTER TABLE step_up_tokens ADD COLUMN channel text NOT NULL DEFAULT 'EMAIL' CHECK (channel IN ('EMAIL', 'SMS'));

-- +goose Down
ALTER TABLE step_up_tokens DROP COLUMN channel;
