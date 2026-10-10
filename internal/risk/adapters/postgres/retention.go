package postgres

import (
	"context"

	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
)

// Retention deletes risk-service's history older than the window (M1):
// the assessments. The velocity rules' events stay to the service, which
// keeps them as long as its rules look back (up to 30 days, B200); the
// devices users signed in from are state.
type Retention struct{}

// Schema is risk-service's.
func (Retention) Schema() string { return "risk" }

// Run applies the rules.
func (Retention) Run(ctx context.Context, db *pg.DB, w retention.Window) ([]retention.Result, error) {
	h := w.History()
	return retention.ApplyAll(ctx, db, w, []retention.Rule{
		{Table: "assessments", Name: "assessments before the window", Cutoff: h, Where: "created_at < $1"},
	})
}
