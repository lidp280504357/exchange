package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Check names of the reconciliation (§5.9, §11.4 invariants 1 and 2).
const (
	CheckJournalBalanced      = "JOURNAL_BALANCED"
	CheckAccountMatchesLines  = "ACCOUNT_MATCHES_LINES"
	CheckSnapshotMatchesAccnt = "SNAPSHOT_MATCHES_ACCOUNT"
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
