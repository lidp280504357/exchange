package postgres

import (
	"context"

	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
)

// Retention deletes market-sim's history older than the window (M1): the
// price samples, price events that ended, and parameter changes but the
// latest (the guards' budgets look back an hour). The bots, the settings
// and the model's saved state stay.
type Retention struct{}

// Schema is market-sim's.
func (Retention) Schema() string { return "marketsim" }

// Run applies the rules.
func (Retention) Run(ctx context.Context, db *pg.DB, w retention.Window) ([]retention.Result, error) {
	h := w.History()
	return retention.ApplyAll(ctx, db, w, []retention.Rule{
		{Table: "samples", Name: "samples before the window", Cutoff: h, Where: "at < $1"},
		{
			Table: "events", Name: "events ended before the window", Cutoff: h,
			Where: "status IN ('DONE', 'CANCELED') AND coalesce(ended_at, created_at) < $1",
		},
		{
			Table: "param_changes", Name: "changes before the window but the latest", Cutoff: h,
			Where: "at < $1 AND version < (SELECT max(version) FROM param_changes)",
		},
	})
}
