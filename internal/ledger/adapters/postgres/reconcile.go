package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lidp280504357/exchange/internal/ledger/domain"
)

// Check names of the reconciliation (§5.9, §11.4 invariants 1, 2 and 5;
// invariant 6 of the contracts needs the positions and is
// derivatives-service's).
const (
	CheckJournalBalanced      = "JOURNAL_BALANCED"
	CheckAccountMatchesLines  = "ACCOUNT_MATCHES_LINES"
	CheckSnapshotMatchesAccnt = "SNAPSHOT_MATCHES_ACCOUNT"
	// Every recorded trade is settled (none parked as FAILED).
	CheckTradesSettled = "TRADES_SETTLED"
	// Per symbol, the recorded trades are numbered 1..n without gaps or
	// repeats (trades from before numbering count first, as 0).
	CheckTradesNumbered = "TRADES_NUMBERED"
	// Invariant 5: per asset, the TRADE_SETTLE and HOUSE_TRADE_SETTLE
	// (ADR-0015) credits equal the traded amounts of the engine's spot
	// trades, and the fees charged equal theirs (contract fees are
	// TRADE_FEE journals too, keyed futures:..., and belong to
	// derivatives-service's trades, not these).
	CheckTradeSettleMatches = "TRADE_SETTLE_MATCHES_TRADES"
	CheckTradeFeeMatches    = "TRADE_FEE_MATCHES_TRADES"
	// Per funding settlement (contract and funding time), payers paid at
	// least what receivers got: FUNDING_CLEARING keeps only the rounding.
	// derivatives-service keys the requests funding:<symbol>:<time>:...
	CheckFundingBatches = "FUNDING_BATCHES_BALANCED"
	// PNL_CLEARING moves only with realized profit and loss.
	CheckPnLClearingEntries = "PNL_CLEARING_ONLY_PNL"
	// HOUSE's inventory of a backed asset (domain.HouseBackedAssets) is not below
	// zero (ADR-0013); only internal assets may go short.
	CheckHouseBackedNonNegative = "HOUSE_BACKED_NON_NEGATIVE"
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
			WHERE j.entry_type IN ('TRADE_SETTLE', 'HOUSE_TRADE_SETTLE') AND l.amount > 0 GROUP BY l.asset)
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
			WHERE j.entry_type = 'TRADE_FEE' AND a.account_type = 'FEE_REVENUE' AND j.idem_key NOT LIKE 'futures:%'
			GROUP BY l.asset)
		SELECT COALESCE(e.asset, b.asset), format('trades %s, TRADE_FEE %s', COALESCE(e.amount, 0), COALESCE(b.amount, 0))
		FROM expected e FULL JOIN booked b ON b.asset = e.asset
		WHERE COALESCE(e.amount, 0) <> COALESCE(b.amount, 0) LIMIT 100`},
	{CheckFundingBatches, `SELECT batch, format('FUNDING_CLEARING net %s', net) FROM (
			SELECT split_part(j.idem_key, ':', 3) || ' ' || split_part(j.idem_key, ':', 4) || ' ' || l.asset AS batch,
				sum(l.amount) AS net
			FROM journals j JOIN journal_lines l ON l.journal_id = j.id JOIN accounts a ON a.id = l.account_id
			WHERE j.entry_type = 'FUNDING_PAYMENT' AND a.account_type = 'FUNDING_CLEARING' AND j.idem_key LIKE 'futures:funding:%'
			GROUP BY 1) b
		WHERE net < 0 LIMIT 100`},
	{CheckPnLClearingEntries, `SELECT j.id::text, 'PNL_CLEARING in a ' || j.entry_type || ' journal'
		FROM accounts a JOIN journal_lines l ON l.account_id = a.id JOIN journals j ON j.id = l.journal_id
		WHERE a.account_type = 'PNL_CLEARING' AND j.entry_type NOT IN ('REALIZED_PNL', 'LIQUIDATION_SETTLE', 'ADL_SETTLE')
		LIMIT 100`},
	{CheckHouseBackedNonNegative, `SELECT asset, format('MARKET_MAKER %s available %s, frozen %s', asset, available, frozen)
		FROM accounts WHERE account_type = 'MARKET_MAKER' AND asset = ANY($1) AND (available < 0 OR frozen < 0)
		ORDER BY asset LIMIT 100`},
}

// checkArgs are the query arguments of the checks that take some.
var checkArgs = map[string]func() []any{CheckHouseBackedNonNegative: houseBacked}

// houseBacked lists the backed assets for the query: none until they are
// read (every asset counts as backed then, the internal ones below zero
// included, which is no mismatch).
func houseBacked() []any {
	assets, _ := domain.HouseBackedAssets()
	if assets == nil {
		assets = []string{}
	}
	return []any{assets}
}

// Reconcile runs every check over the whole ledger and records each result
// in reconciliation_runs.
func (s *Store) Reconcile(ctx context.Context, now func() time.Time) ([]CheckResult, error) {
	out := make([]CheckResult, 0, len(checks))
	for _, c := range checks {
		started := now()
		res := CheckResult{Check: c.name, Mismatches: []Mismatch{}}
		var args []any
		if f := checkArgs[c.name]; f != nil {
			args = f()
		}
		rows, err := s.db.Query(ctx, c.sql, args...)
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

// ReconciliationRuns returns the latest run of each check and up to
// failures runs that found mismatches, newest first; details keep the
// first ten mismatches.
func (s *Store) ReconciliationRuns(ctx context.Context, failures int) (latest, failing []domain.ReconciliationRun, err error) {
	const cols = `check_name, started_at, mismatches, jsonb_path_query_array(details, '$[0 to 9]')`
	scan := func(sql string, args ...any) ([]domain.ReconciliationRun, error) {
		rows, err := s.db.Query(ctx, sql, args...)
		if err != nil {
			return nil, fmt.Errorf("reconciliation runs: %w", err)
		}
		defer rows.Close()
		out := []domain.ReconciliationRun{}
		for rows.Next() {
			var r domain.ReconciliationRun
			if err := rows.Scan(&r.Check, &r.StartedAt, &r.Mismatches, &r.Details); err != nil {
				return nil, fmt.Errorf("reconciliation runs: %w", err)
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}
	if latest, err = scan(`SELECT DISTINCT ON (check_name) ` + cols + ` FROM reconciliation_runs ORDER BY check_name, started_at DESC`); err != nil {
		return nil, nil, err
	}
	if failing, err = scan(`SELECT `+cols+` FROM reconciliation_runs WHERE mismatches > 0 ORDER BY started_at DESC LIMIT $1`, failures); err != nil {
		return nil, nil, err
	}
	return latest, failing, nil
}
