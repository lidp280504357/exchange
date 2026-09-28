package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Check names of the reconciliation (§5.9, §11.4 invariants 1, 2 and 5).
const (
	CheckJournalBalanced      = "JOURNAL_BALANCED"
	CheckAccountMatchesLines  = "ACCOUNT_MATCHES_LINES"
	CheckSnapshotMatchesAccnt = "SNAPSHOT_MATCHES_ACCOUNT"
	// Every recorded trade is settled (none parked as FAILED).
	CheckTradesSettled = "TRADES_SETTLED"
	// Per symbol, the recorded trades are numbered 1..n without gaps or
	// repeats (trades from before numbering count first, as 0).
	CheckTradesNumbered = "TRADES_NUMBERED"
	// Invariant 5: per asset, the TRADE_SETTLE credits equal the traded
	// amounts of the engine's trades, and the fees charged equal theirs.
	CheckTradeSettleMatches = "TRADE_SETTLE_MATCHES_TRADES"
	CheckTradeFeeMatches    = "TRADE_FEE_MATCHES_TRADES"
)

// Mismatch is one finding of a check.
type Mismatch struct {
	Key    string `json:"key"`
	Detail string `json:"detail"`
}

// CheckResult is the outcome of one check.
type CheckResult struct {
	Check      string
	Mismatches []Mismatch
}

var checks = []struct {
	name string
	sql  string
}{
	{CheckJournalBalanced, `SELECT journal_id::text || '/' || asset, 'sum ' || sum(amount)::text FROM journal_lines
		GROUP BY journal_id, asset HAVING sum(amount) <> 0 LIMIT 100`},
	{CheckAccountMatchesLines, `SELECT a.id::text, format('available %s vs lines %s, frozen %s vs lines %s',
		a.available, COALESCE(s.available, 0), a.frozen, COALESCE(s.frozen, 0))
		FROM accounts a LEFT JOIN (
			SELECT account_id, sum(amount) FILTER (WHERE balance_kind = 'AVAILABLE') AS available,
				sum(amount) FILTER (WHERE balance_kind = 'FROZEN') AS frozen
			FROM journal_lines GROUP BY account_id) s ON s.account_id = a.id
		WHERE a.available <> COALESCE(s.available, 0) OR a.frozen <> COALESCE(s.frozen, 0) LIMIT 100`},
	{CheckSnapshotMatchesAccnt, `SELECT a.id::text, format('account %s/%s v%s, last line %s/%s v%s',
		a.available, a.frozen, a.version, l.available_after, l.frozen_after, l.account_version)
		FROM accounts a JOIN LATERAL (
			SELECT available_after, frozen_after, account_version FROM journal_lines
			WHERE account_id = a.id ORDER BY account_version DESC LIMIT 1) l ON true
		WHERE a.available <> l.available_after OR a.frozen <> l.frozen_after OR a.version <> l.account_version LIMIT 100`},
	{CheckTradesSettled, `SELECT trade_id::text, error_code || ': ' || error FROM trades WHERE status = 'FAILED'
		ORDER BY recorded_at LIMIT 100`},
	{CheckTradesNumbered, `SELECT symbol, format('%s trades recorded, numbered up to %s', count(*), max(trade_number))
		FROM trades GROUP BY symbol HAVING max(trade_number) > 0 AND count(*) <> max(trade_number) LIMIT 100`},
	{CheckTradeSettleMatches, `WITH expected AS (
			SELECT asset, sum(amount) AS amount FROM (
				SELECT base_asset AS asset, quantity AS amount FROM trades WHERE status = 'SETTLED'
				UNION ALL
				SELECT quote_asset, quote_quantity FROM trades WHERE status = 'SETTLED') t
			GROUP BY asset),
		booked AS (
			SELECT l.asset, sum(l.amount) AS amount FROM journal_lines l JOIN journals j ON j.id = l.journal_id
			WHERE j.entry_type = 'TRADE_SETTLE' AND l.amount > 0 GROUP BY l.asset)
		SELECT COALESCE(e.asset, b.asset), format('trades %s, TRADE_SETTLE %s', COALESCE(e.amount, 0), COALESCE(b.amount, 0))
		FROM expected e FULL JOIN booked b ON b.asset = e.asset
		WHERE COALESCE(e.amount, 0) <> COALESCE(b.amount, 0) LIMIT 100`},
	{CheckTradeFeeMatches, `WITH expected AS (
			SELECT asset, sum(amount) AS amount FROM (
				SELECT base_asset AS asset, buyer_fee AS amount FROM trades WHERE status = 'SETTLED'
				UNION ALL
				SELECT quote_asset, seller_fee FROM trades WHERE status = 'SETTLED') t
			GROUP BY asset),
		booked AS (
			SELECT l.asset, sum(l.amount) AS amount FROM journal_lines l JOIN journals j ON j.id = l.journal_id
			JOIN accounts a ON a.id = l.account_id
			WHERE j.entry_type = 'TRADE_FEE' AND a.account_type = 'FEE_REVENUE' GROUP BY l.asset)
		SELECT COALESCE(e.asset, b.asset), format('trades %s, TRADE_FEE %s', COALESCE(e.amount, 0), COALESCE(b.amount, 0))
		FROM expected e FULL JOIN booked b ON b.asset = e.asset
		WHERE COALESCE(e.amount, 0) <> COALESCE(b.amount, 0) LIMIT 100`},
}

// Reconcile runs every check over the whole ledger and records each result
// in reconciliation_runs.
func (s *Store) Reconcile(ctx context.Context, now func() time.Time) ([]CheckResult, error) {
	out := make([]CheckResult, 0, len(checks))
	for _, c := range checks {
		started := now()
		res := CheckResult{Check: c.name, Mismatches: []Mismatch{}}
		rows, err := s.db.Query(ctx, c.sql)
		if err != nil {
			return nil, fmt.Errorf("reconcile %s: %w", c.name, err)
		}
		for rows.Next() {
			var m Mismatch
			if err := rows.Scan(&m.Key, &m.Detail); err != nil {
				rows.Close()
				return nil, fmt.Errorf("reconcile %s: %w", c.name, err)
			}
			res.Mismatches = append(res.Mismatches, m)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("reconcile %s: %w", c.name, err)
		}
		details, _ := json.Marshal(res.Mismatches)
		if _, err := s.db.Exec(ctx, `INSERT INTO reconciliation_runs (started_at, check_name, mismatches, details) VALUES ($1, $2, $3, $4)`,
			started, c.name, len(res.Mismatches), details); err != nil {
			return nil, fmt.Errorf("record reconciliation: %w", err)
		}
		out = append(out, res)
	}
	return out, nil
}
