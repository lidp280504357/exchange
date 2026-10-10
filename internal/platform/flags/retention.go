package flags

import (
	"context"
	"strconv"

	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
)

// KeptChanges is how many of each flag's latest changes outlive the
// retention window: the console reads a product line's reduce-only
// window off the latest 50 (History).
const KeptChanges = 50

// Retention deletes the config schema's history older than the window
// (M1): flag changes, but each flag's latest KeptChanges. The flags are
// state and stay.
type Retention struct{}

// Schema is the shared config schema.
func (Retention) Schema() string { return "config" }

// Run applies the rule.
func (Retention) Run(ctx context.Context, db *pg.DB, w retention.Window) ([]retention.Result, error) {
	return retention.ApplyAll(ctx, db, w, []retention.Rule{{
		Table: "flag_changes", Name: "changes before the window but each flag's latest 50", Cutoff: w.History(),
		Where: `changed_at < $1 AND id NOT IN (SELECT id FROM (
			SELECT id, row_number() OVER (PARTITION BY key ORDER BY id DESC) AS n FROM flag_changes) r WHERE n <= ` + strconv.Itoa(KeptChanges) + `)`,
	}})
}
