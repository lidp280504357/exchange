package postgres

import (
	"context"

	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
)

// Retention deletes admin-service's history older than the window (M1):
// decided approvals, ended console sessions and closed instrument
// changes. Administrators, notes and tags on users, settings and uploads
// are state and stay; a pending approval or change stays whatever its age.
type Retention struct{}

// Schema is admin-service's.
func (Retention) Schema() string { return "admin" }

// Run applies the rules.
func (Retention) Run(ctx context.Context, db *pg.DB, w retention.Window) ([]retention.Result, error) {
	h := w.History()
	return retention.ApplyAll(ctx, db, w, []retention.Rule{
		{Table: "approvals", Name: "approvals decided before the window", Cutoff: h, Where: "status <> 'PENDING' AND decided_at < $1"},
		{
			Table: "admin_sessions", Name: "sessions ended before the window", Cutoff: h,
			Where: "revoked_at < $1 OR (revoked_at IS NULL AND expires_at < $1)",
		},
		{
			Table: "instrument_changes", Name: "changes closed or applied before the window", Cutoff: h,
			Where: "(status IN ('CANCELED', 'REJECTED') AND closed_at < $1) OR (status IN ('APPLIED', 'FAILED') AND applied_at < $1)",
		},
	})
}
