package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
)

// ReplayDays outlives the topics a consumer may replay (trade.events and
// order.commands keep 30 days): settled trades are kept this long, or a
// replay would record them again and the reconciliation count them twice;
// so are the keys of order freezes and releases, which only an order's
// own retries post again.
const ReplayDays = 35

// prunable is a journal the run deletes, $1 the cutoff. Kept whatever their
// age: what a check or a rule adds up in full - the funding batches
// (FUNDING_BATCHES_BALANCED), the interest charged (MARGIN_INTEREST_CONSERVED
// against the income account), the custody resets (their reversal's limit),
// the release of an unclaimed deposit (its journal and payee are read
// back) - and the journals a transfer or a hold still points at.
const prunable = `j.posted_at < $1
	AND j.entry_type NOT IN ('FUNDING_PAYMENT', 'MARGIN_INTEREST')
	AND NOT starts_with(j.idem_key, 'custody-reset:') AND NOT starts_with(j.idem_key, 'deposit-release:')
	AND NOT EXISTS (SELECT 1 FROM transfers t WHERE t.journal_id = j.id)
	AND NOT EXISTS (SELECT 1 FROM holds h WHERE h.journal_id = j.id OR h.release_journal_id = j.id)`

// Retention deletes the ledger's history older than the window (M1,
// ADR-0022). Journals go with their lines and line types, what the lines
// added up to kept as checkpoints (each account's balances, the trades'
// settlement and fee journals by asset) and the journals' keys kept, so a
// posting with one is a replay still: an order freeze's or release's key
// for ReplayDays, any other for the keys' window. Settled trades go after
// ReplayDays, their amounts and count kept as checkpoints. Ended
// transfers, released holds and old reconciliation runs go with the
// window; the futures and margin settlement records, which answer a
// replay with its outcome, with the keys' window. Accounts and settings
// are state and stay.
type Retention struct{}

// Schema is the ledger's.
func (Retention) Schema() string { return "ledger" }

// Run applies the rules: the transfers and holds go before the journals
// they point at.
func (Retention) Run(ctx context.Context, db *pg.DB, w retention.Window) ([]retention.Result, error) {
	if err := w.Validate(); err != nil {
		return nil, err
	}
	h, k := w.History(), w.Keys()
	replay := w.Now.AddDate(0, 0, -max(ReplayDays, w.Days))
	out, err := retention.ApplyAll(ctx, db, w, []retention.Rule{
		{Table: "transfers", Name: "transfers ended before the window", Cutoff: h, Where: "status <> 'REQUESTED' AND created_at < $1"},
		{Table: "holds", Name: "holds released before the window", Cutoff: h, Where: "released_at < $1"},
		{Table: "reconciliation_runs", Name: "runs before the window", Cutoff: h, Where: "started_at < $1"},
		{Table: "futures_settlements", Name: "settlement records before the keys' window", Cutoff: k, Where: "created_at < $1"},
		{Table: "margin_postings", Name: "margin posting records before the keys' window", Cutoff: k, Where: "created_at < $1"},
		{
			Table: "journal_keys", Name: "deleted order freezes' and releases' keys past the replays", Cutoff: replay,
			Where: "entry_type IN ('ORDER_FREEZE', 'ORDER_UNFREEZE') AND posted_at < $1",
		},
		{
			Table: "journal_keys", Name: "other deleted journals' keys before the keys' window", Cutoff: k,
			Where: "entry_type NOT IN ('ORDER_FREEZE', 'ORDER_UNFREEZE') AND posted_at < $1",
		},
	})
	if err != nil {
		return out, err
	}
	journals, err := pruneJournals(ctx, db, w, h)
	out = append(out, journals...)
	if err != nil {
		return out, err
	}
	trades, err := pruneTrades(ctx, db, w, replay)
	return append(out, trades), err
}

// pruneJournals deletes the journals before cutoff with their lines and
// line types, a batch per transaction: the batch's keys move to
// journal_keys, its lines' sums to the checkpoints and each account's
// latest of them to account_snapshots in the same transaction, which
// alone may delete them (ledger.retention).
func pruneJournals(ctx context.Context, db *pg.DB, w retention.Window, cutoff time.Time) ([]retention.Result, error) {
	out := []retention.Result{
		{Table: "journals", Rule: "journals before the window, their keys kept"},
		{Table: "journal_lines", Rule: "lines of those journals, their sums kept as checkpoints"},
		{Table: "journal_line_types", Rule: "line types of those lines"},
	}
	for i := range out {
		var err error
		if out[i].Total, out[i].Bytes, err = retention.Stats(ctx, db, out[i].Table); err != nil {
			return out, err
		}
	}
	if w.DryRun {
		err := db.QueryRow(ctx, `WITH j AS (SELECT j.id FROM journals j WHERE `+prunable+`),
			l AS (SELECT l.id, l.account_id FROM journal_lines l JOIN j ON j.id = l.journal_id)
			SELECT (SELECT count(*) FROM j), (SELECT count(*) FROM l),
				(SELECT count(*) FROM journal_line_types t JOIN l ON l.id = t.line_id AND l.account_id = t.account_id)`, cutoff).
			Scan(&out[0].Rows, &out[1].Rows, &out[2].Rows)
		if err != nil {
			return out, fmt.Errorf("retention: count the journals: %w", err)
		}
		return out, nil
	}
	for {
		var n, lines, types int64
		err := db.InTx(ctx, func(tx pgx.Tx) error {
			n, lines, types = 0, 0, 0
			for _, q := range []string{
				`SET LOCAL ledger.retention = 'on'`,
				`CREATE TEMP TABLE pruned (id uuid PRIMARY KEY, idem_key text, request_hash bytea, seq bigint, entry_type text,
					posted_at timestamptz) ON COMMIT DROP`,
			} {
				if _, err := tx.Exec(ctx, q); err != nil {
					return err
				}
			}
			tag, err := tx.Exec(ctx, `INSERT INTO pruned SELECT j.id, j.idem_key, j.request_hash, j.seq, j.entry_type, j.posted_at
				FROM journals j WHERE `+prunable+` ORDER BY j.seq LIMIT $2`, cutoff, w.Batch)
			if err != nil || tag.RowsAffected() == 0 {
				return err
			}
			n = tag.RowsAffected()
			for _, q := range []string{
				// Each account's balances.
				`INSERT INTO checkpoints (name, key, amount, count)
					SELECT 'ACCOUNT_' || l.balance_kind, l.account_id::text, sum(l.amount), count(*)
					FROM journal_lines l JOIN pruned p ON p.id = l.journal_id GROUP BY 1, 2
					ON CONFLICT (name, key) DO UPDATE SET amount = checkpoints.amount + excluded.amount,
						count = checkpoints.count + excluded.count, updated_at = now()`,
				// What the spot trades' settlement credited, by asset (TRADE_SETTLE_MATCHES_TRADES).
				`INSERT INTO checkpoints (name, key, amount, count)
					SELECT 'TRADE_SETTLE_BOOKED', l.asset, sum(l.amount), count(*)
					FROM journal_lines l JOIN pruned p ON p.id = l.journal_id
					WHERE p.entry_type IN ('TRADE_SETTLE', 'HOUSE_TRADE_SETTLE', 'MARGIN_TRADE_SETTLE') AND l.amount > 0 GROUP BY l.asset
					ON CONFLICT (name, key) DO UPDATE SET amount = checkpoints.amount + excluded.amount,
						count = checkpoints.count + excluded.count, updated_at = now()`,
				// What their fees took in, by asset (TRADE_FEE_MATCHES_TRADES; the contracts' are keyed futures:...).
				`INSERT INTO checkpoints (name, key, amount, count)
					SELECT 'TRADE_FEE_BOOKED', l.asset, sum(l.amount), count(*)
					FROM journal_lines l JOIN pruned p ON p.id = l.journal_id JOIN accounts a ON a.id = l.account_id
					WHERE p.entry_type = 'TRADE_FEE' AND a.account_type = 'FEE_REVENUE' AND p.idem_key NOT LIKE 'futures:%' GROUP BY l.asset
					ON CONFLICT (name, key) DO UPDATE SET amount = checkpoints.amount + excluded.amount,
						count = checkpoints.count + excluded.count, updated_at = now()`,
				// Each account's balances after its latest deleted line
				// (SNAPSHOT_MATCHES_ACCOUNT, B199).
				`INSERT INTO account_snapshots (account_id, available, frozen, account_version)
					SELECT DISTINCT ON (l.account_id) l.account_id, l.available_after, l.frozen_after, l.account_version
					FROM journal_lines l JOIN pruned p ON p.id = l.journal_id ORDER BY l.account_id, l.account_version DESC
					ON CONFLICT (account_id) DO UPDATE SET available = excluded.available, frozen = excluded.frozen,
						account_version = excluded.account_version, updated_at = now()
					WHERE excluded.account_version > account_snapshots.account_version`,
				`INSERT INTO journal_keys (idem_key, request_hash, journal_id, seq, entry_type, posted_at)
					SELECT idem_key, request_hash, id, seq, entry_type, posted_at FROM pruned`,
			} {
				if _, err := tx.Exec(ctx, q); err != nil {
					return err
				}
			}
			if tag, err = tx.Exec(ctx, `DELETE FROM journal_line_types t USING journal_lines l, pruned p
				WHERE l.journal_id = p.id AND t.account_id = l.account_id AND t.entry_type = p.entry_type AND t.line_id = l.id`); err != nil {
				return err
			}
			types = tag.RowsAffected()
			if tag, err = tx.Exec(ctx, `DELETE FROM journal_lines l USING pruned p WHERE l.journal_id = p.id`); err != nil {
				return err
			}
			lines = tag.RowsAffected()
			if tag, err = tx.Exec(ctx, `DELETE FROM journals j USING pruned p WHERE j.id = p.id`); err != nil {
				return err
			}
			if tag.RowsAffected() != n {
				return fmt.Errorf("deleted %d journals of %d", tag.RowsAffected(), n)
			}
			return nil
		})
		if err != nil {
			return out, fmt.Errorf("retention: delete journals after %d: %w", out[0].Rows, err)
		}
		out[0].Rows += n
		out[1].Rows += lines
		out[2].Rows += types
		if n < int64(w.Batch) {
			return out, nil
		}
		if err := retention.Wait(ctx, w.Pause); err != nil {
			return out, fmt.Errorf("retention: journals stopped after %d: %w", out[0].Rows, err)
		}
	}
}

// pruneTrades deletes the settled trades recorded before cutoff, a batch
// per transaction, their count by symbol and their amounts and fees by
// asset kept as checkpoints in the same transaction.
func pruneTrades(ctx context.Context, db *pg.DB, w retention.Window, cutoff time.Time) (retention.Result, error) {
	res := retention.Result{Table: "trades", Rule: "settled trades past the topic's replay, their sums kept as checkpoints"}
	var err error
	if res.Total, res.Bytes, err = retention.Stats(ctx, db, res.Table); err != nil {
		return res, err
	}
	const settled = `status = 'SETTLED' AND recorded_at < $1`
	if w.DryRun {
		if err := db.QueryRow(ctx, `SELECT count(*) FROM trades WHERE `+settled, cutoff).Scan(&res.Rows); err != nil {
			return res, fmt.Errorf("retention: count the trades: %w", err)
		}
		return res, nil
	}
	for {
		var n int64
		err := db.InTx(ctx, func(tx pgx.Tx) error {
			n = 0
			if _, err := tx.Exec(ctx, `CREATE TEMP TABLE gone ON COMMIT DROP AS SELECT trade_id, symbol, base_asset, quote_asset,
				quantity, quote_quantity, buyer_fee, seller_fee FROM trades WITH NO DATA`); err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, `INSERT INTO gone SELECT trade_id, symbol, base_asset, quote_asset, quantity, quote_quantity,
				buyer_fee, seller_fee FROM trades WHERE `+settled+` ORDER BY recorded_at LIMIT $2`, cutoff, w.Batch)
			if err != nil || tag.RowsAffected() == 0 {
				return err
			}
			n = tag.RowsAffected()
			if _, err := tx.Exec(ctx, `INSERT INTO checkpoints (name, key, amount, count)
				SELECT name, key, sum(amount), sum(count) FROM (
					SELECT 'TRADES' AS name, symbol AS key, 0 AS amount, 1 AS count FROM gone
					UNION ALL SELECT 'TRADE_SETTLE_EXPECTED', base_asset, quantity, 1 FROM gone
					UNION ALL SELECT 'TRADE_SETTLE_EXPECTED', quote_asset, quote_quantity, 1 FROM gone
					UNION ALL SELECT 'TRADE_FEE_EXPECTED', base_asset, buyer_fee, 1 FROM gone
					UNION ALL SELECT 'TRADE_FEE_EXPECTED', quote_asset, seller_fee, 1 FROM gone) s
				GROUP BY name, key
				ON CONFLICT (name, key) DO UPDATE SET amount = checkpoints.amount + excluded.amount,
					count = checkpoints.count + excluded.count, updated_at = now()`); err != nil {
				return err
			}
			if tag, err = tx.Exec(ctx, `DELETE FROM trades t USING gone g WHERE t.trade_id = g.trade_id`); err != nil {
				return err
			}
			if tag.RowsAffected() != n {
				return fmt.Errorf("deleted %d trades of %d", tag.RowsAffected(), n)
			}
			return nil
		})
		if err != nil {
			return res, fmt.Errorf("retention: delete trades after %d: %w", res.Rows, err)
		}
		res.Rows += n
		if n < int64(w.Batch) {
			return res, nil
		}
		if err := retention.Wait(ctx, w.Pause); err != nil {
			return res, fmt.Errorf("retention: trades stopped after %d: %w", res.Rows, err)
		}
	}
}
