package postgres

import (
	"context"

	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
)

// Retention deletes instrument-service's history older than the window
// (M1): the record of reference-data changes. Assets, networks, pairs,
// contracts, fee schedules and the platform's profile are state and stay.
type Retention struct{}

// Schema is instrument-service's.
func (Retention) Schema() string { return "instrument" }

// Run applies the rules.
func (Retention) Run(ctx context.Context, db *pg.DB, w retention.Window) ([]retention.Result, error) {
	return retention.ApplyAll(ctx, db, w, []retention.Rule{
		{Table: "config_history", Name: "changes before the window", Cutoff: w.History(), Where: "created_at < $1"},
	})
}
