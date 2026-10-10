-- M1, review LI ②: the retention deletes the settled fills by their
-- execution time, a batch at a time; with this index a batch does not
-- scan the table (orders_finished in 00009 does the same for the orders).

-- +goose Up
CREATE INDEX fills_settled ON fills (executed_at) WHERE settled;

-- +goose Down
DROP INDEX fills_settled;
