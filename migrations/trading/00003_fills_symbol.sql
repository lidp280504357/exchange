-- The latest trade of a symbol anchors the price band and the protection
-- price of market orders (requirements §11.2; plan §6.3 task 5).

-- +goose Up
CREATE INDEX fills_symbol_idx ON fills (symbol, sequence DESC);

-- +goose Down
DROP INDEX fills_symbol_idx;
