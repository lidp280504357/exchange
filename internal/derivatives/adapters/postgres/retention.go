package postgres

import (
	"context"

	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
)

// Retention is derivatives-service's part of the history policy (M1, the
// user's decision of 2026-10-10: Postgres keeps the last 15 days), run by
// exchangectl retention. What is current state stays whatever its age:
// positions (flat ones too), settings, the contracts' states, the cross
// accounts' warnings, settlements waiting on the ledger, active
// take-profits and stop-losses, open cross liquidations, funding rounds
// waiting for their rate. The reconciliation (invariant 6) reads only
// those and the ledger's balances, so it holds after a run.
//
// A fill is applied once by its key, and the engine's trades stay on
// their topic 30 days: the keys of the fills it deletes go to fill_keys,
// kept for the window's keys (90 days), which Fills().Has reads too. An
// order's client_order_id is unique while the order is kept (15 days,
// past the platform's 24 hours of idempotency; the coordinator's
// decision of 19:0x): no key table.
type Retention struct{}

// Schema is derivatives-service's schema.
func (Retention) Schema() string { return "derivatives" }

// finishedOrder: the engine is done with it, what it reserved is released
// (or it reserved nothing), its fills are applied, and none of them waits
// on the ledger.
const finishedOrder = `status IN ('FILLED', 'CANCELED', 'REJECTED', 'EXPIRED') AND (released OR freeze_state <> 'FROZEN')
	AND consumed_quantity >= filled_quantity AND updated_at < $1
	AND NOT EXISTS (SELECT 1 FROM pending_settlements p JOIN fills f ON f.trade_id = p.trade_id AND f.side = p.side
		WHERE f.order_id = orders.order_id)`

// settledFill: booked in the ledger, not waiting on it.
const settledFill = `settled AND executed_at < $1
	AND NOT EXISTS (SELECT 1 FROM pending_settlements p WHERE p.trade_id = fills.trade_id AND p.side = fills.side)`

// moveFillKeys deletes a batch of settled fills and keeps their keys; its
// command tag (the INSERT's) counts them.
const moveFillKeys = `WITH gone AS (
		DELETE FROM fills WHERE ctid IN (SELECT ctid FROM fills WHERE ` + settledFill + ` LIMIT $2)
		RETURNING trade_id, side, executed_at)
	INSERT INTO fill_keys (trade_id, side, executed_at) SELECT trade_id, side, executed_at FROM gone ON CONFLICT DO NOTHING`

// kept are the tables of current state, reported with their size.
var kept = []string{"positions", "settings", "contract_states", "cross_accounts", "pending_settlements"}

// Run deletes, a batch at a time, or with DryRun counts, what the window
// keeps no longer, then reports the tables it keeps whole.
func (Retention) Run(ctx context.Context, db *pg.DB, w retention.Window) ([]retention.Result, error) {
	cut, keys := w.History(), w.Keys()
	out, err := retention.ApplyAll(ctx, db, w, []retention.Rule{
		{Table: "orders", Name: "finished, released, fills applied, last changed before the cutoff", Cutoff: cut, Where: finishedOrder},
		{Table: "fills", Name: "settled, executed before the cutoff; their keys to fill_keys", Cutoff: cut, Where: settledFill, Delete: moveFillKeys},
		{Table: "fill_keys", Name: "keys of deleted fills executed before the keys' cutoff", Cutoff: keys, Where: "executed_at < $1"},
		{
			Table: "funding_payments", Name: "of rounds settled or skipped, funding time before the cutoff", Cutoff: cut,
			Where: `EXISTS (SELECT 1 FROM funding_rounds r WHERE r.symbol = funding_payments.symbol
				AND r.funding_time = funding_payments.funding_time AND r.status IN ('SETTLED', 'SKIPPED') AND r.funding_time < $1)`,
		},
		// After their payments, the rule before (the foreign key).
		{
			Table: "funding_rounds", Name: "settled or skipped, funding time before the cutoff", Cutoff: cut,
			Where: "status IN ('SETTLED', 'SKIPPED') AND funding_time < $1",
		},
		{Table: "conditional_orders", Name: "ended, last changed before the cutoff", Cutoff: cut, Where: "status <> 'ACTIVE' AND updated_at < $1"},
		{Table: "cross_liquidations", Name: "done before the cutoff", Cutoff: cut, Where: "status = 'DONE' AND done_at < $1"},
		{Table: "reconciliation_runs", Name: "started before the cutoff", Cutoff: cut, Where: "started_at < $1"},
	})
	if err != nil {
		return out, err
	}
	for _, t := range kept {
		rows, bytes, err := retention.Stats(ctx, db, t)
		if err != nil {
			return out, err
		}
		out = append(out, retention.Result{Table: t, Rule: "kept: current state", Total: rows, Bytes: bytes})
	}
	return out, nil
}
