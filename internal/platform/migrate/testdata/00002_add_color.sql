-- +goose Up
ALTER TABLE widgets ADD COLUMN color text NOT NULL DEFAULT 'blue';

-- +goose Down
ALTER TABLE widgets DROP COLUMN color;
