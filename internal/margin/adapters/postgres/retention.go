package postgres

import (
	"context"

	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
)

// Retention is margin-service's part of the history policy (M1, the
// user's decision of 2026-10-10: Postgres keeps the last 15 days), run by
// exchangectl retention. What is current state stays whatever its age:
// the accounts, loans, pools and terms, liquidations under way and their
// orders, writes waiting for the ledger (PENDING), the interest run under
// way and the latest one done (the next starts from it). The
// reconciliation (invariant 7, the pools) reads only those and the
// ledger's debts, so it holds after a run.
//
// Borrows, repayments, transfers and order reservations are the
// idempotency records of their keys (a client's Idempotency-Key, an
// order's, an automatic repayment's from the ledger): small rows, kept
// whole for the window's keys (90 days).
type Retention struct{}

// Schema is margin-service's schema.
func (Retention) Schema() string { return "margin" }

// kept are the tables of current state, reported with their size.
var kept = []string{"accounts", "loans", "pools", "asset_terms", "pair_terms", "cross_terms"}

// Run deletes, a batch at a time, or with DryRun counts, what the window
// keeps no longer, then reports the tables it keeps whole.
func (Retention) Run(ctx context.Context, db *pg.DB, w retention.Window) ([]retention.Result, error) {
	cut, keys := w.History(), w.Keys()
	out, err := retention.ApplyAll(ctx, db, w, []retention.Rule{
		{Table: "interest_charges", Name: "booked, charged before the cutoff", Cutoff: cut, Where: "status = 'DONE' AND created_at < $1"},
		{Table: "hourly_rates", Name: "hours before the cutoff", Cutoff: cut, Where: "hour < $1"},
		{
			Table: "interest_runs", Name: "done, hours before the cutoff, all but the latest done", Cutoff: cut,
			Where: "status = 'DONE' AND hour < $1 AND hour < (SELECT max(r.hour) FROM interest_runs r WHERE r.status = 'DONE')",
		},
		{Table: "reconciliation_runs", Name: "started before the cutoff", Cutoff: cut, Where: "started_at < $1"},
		// Before their liquidations (the foreign key).
		{
			Table: "liquidation_orders", Name: "of liquidations completed before the cutoff", Cutoff: cut,
			Where: `EXISTS (SELECT 1 FROM liquidations l WHERE l.liquidation_id = liquidation_orders.liquidation_id
				AND l.status = 'COMPLETED' AND l.completed_at < $1)`,
		},
		{Table: "liquidations", Name: "completed before the cutoff", Cutoff: cut, Where: "status = 'COMPLETED' AND completed_at < $1"},
		{Table: "borrows", Name: "key records: finished, made before the keys' cutoff", Cutoff: keys, Where: "status <> 'PENDING' AND created_at < $1"},
		{Table: "repays", Name: "key records: finished, made before the keys' cutoff", Cutoff: keys, Where: "status <> 'PENDING' AND created_at < $1"},
		{Table: "transfers", Name: "key records: finished, made before the keys' cutoff", Cutoff: keys, Where: "status <> 'PENDING' AND created_at < $1"},
		{Table: "order_reservations", Name: "key records: made before the keys' cutoff", Cutoff: keys, Where: "created_at < $1"},
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
