package postgres

import (
	"context"

	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
)

// Retention deletes risk-service's history older than the window (M1):
// assessments and the velocity rules' events (their windows are minutes
// to hours). The devices users signed in from are state and stay.
type Retention struct{}

// Schema is risk-service's.
func (Retention) Schema() string { return "risk" }

// Run applies the rules.
func (Retention) Run(ctx context.Context, db *pg.DB, w retention.Window) ([]retention.Result, error) {
	h := w.History()
	return retention.ApplyAll(ctx, db, w, []retention.Rule{
		{Table: "assessments", Name: "assessments before the window", Cutoff: h, Where: "created_at < $1"},
		{Table: "velocity_events", Name: "velocity events before the window", Cutoff: h, Where: "at < $1"},
	})
}
