-- A test account cleared out (L4, the user 2026-10-10: "直接把测试用户清理了吧，
-- 需要测试再创建"): its orders, positions and loans ended, its balances
-- moved to the ADJUSTMENT account, the account CLOSED - and then purged_at
-- set. The row stays (the ledger only appends, and every entry names its
-- account); the console's lists and counts leave purged accounts out unless
-- asked for them.

-- +goose Up
ALTER TABLE users ADD COLUMN purged_at timestamptz;
CREATE INDEX users_unpurged_created ON users (created_at DESC, id DESC) WHERE purged_at IS NULL;

-- +goose Down
DROP INDEX users_unpurged_created;
ALTER TABLE users DROP COLUMN purged_at;
