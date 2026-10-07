-- The product lines (design 2026-10-07, product switches, K0): spot
-- trading, the USDT-margined and the coin-margined contracts, open unless an
-- operator closes one; none is ever deleted. Seeded on here, once, so the
-- flags' lists and the console show them open; a flag already stored keeps
-- its state. Services also take a missing one as open
-- (flags.Client.Closed).

-- +goose Up
WITH seeded AS (
    INSERT INTO flags (key, enabled, description, updated_by) VALUES
        ('product.spot', true, 'Spot trading as a product line (design 2026-10-07, product switches)', 'migration:config-00002'),
        ('product.usdt_m', true, 'The USDT-margined contracts as a product line (design 2026-10-07, product switches)', 'migration:config-00002'),
        ('product.coin_m', true, 'The coin-margined contracts as a product line (design 2026-10-07, product switches)', 'migration:config-00002')
    ON CONFLICT (key) DO NOTHING
    RETURNING key, enabled, rules, description, version, updated_by, updated_at
)
INSERT INTO flag_changes (key, old_value, new_value, changed_by, reason)
SELECT key, NULL, to_jsonb(seeded), updated_by, 'seeded open: the product lines are open unless an operator closes one'
FROM seeded;

-- +goose Down
DELETE FROM flags WHERE key IN ('product.spot', 'product.usdt_m', 'product.coin_m');
