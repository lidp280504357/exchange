package postgres

import (
	"context"

	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
)

// Retention deletes market-maker's history older than the window (M1):
// changes of HOUSE's caps but the latest. The caps in force stay.
type Retention struct{}

// Schema is market-maker's.
func (Retention) Schema() string { return "marketmaker" }

// Run applies the rule.
func (Retention) Run(ctx context.Context, db *pg.DB, w retention.Window) ([]retention.Result, error) {
	return retention.ApplyAll(ctx, db, w, []retention.Rule{{
		Table: "house_caps_changes", Name: "changes before the window but the latest", Cutoff: w.History(),
		Where: "at < $1 AND version < (SELECT max(version) FROM house_caps_changes)",
	}})
}
