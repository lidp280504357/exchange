package postgres

import (
	"context"

	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
)

// Retention deletes user-service's history older than the window (M1):
// the records of status and kind changes. The users themselves (cleared
// out test accounts too), their consents and favorites are state and
// stay.
type Retention struct{}

// Schema is user-service's.
func (Retention) Schema() string { return "users" }

// Run applies the rules.
func (Retention) Run(ctx context.Context, db *pg.DB, w retention.Window) ([]retention.Result, error) {
	h := w.History()
	return retention.ApplyAll(ctx, db, w, []retention.Rule{
		{Table: "user_status_changes", Name: "status changes before the window", Cutoff: h, Where: "created_at < $1"},
		{Table: "user_kind_changes", Name: "kind changes before the window", Cutoff: h, Where: "created_at < $1"},
	})
}
