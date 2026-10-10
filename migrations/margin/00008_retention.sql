-- M1, review LI ②: the retention deletes the booked interest charges by
-- when they were charged and the hourly rates by their hour, a batch at a
-- time; with these indexes a batch does not scan the table.

-- +goose Up
CREATE INDEX interest_charges_done ON interest_charges (created_at) WHERE status = 'DONE';
CREATE INDEX hourly_rates_hour ON hourly_rates (hour);

-- +goose Down
DROP INDEX hourly_rates_hour;
DROP INDEX interest_charges_done;
