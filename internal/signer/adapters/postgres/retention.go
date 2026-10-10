package postgres

import (
	"context"

	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
)

// Retention deletes the signer's history older than the window (M1): the
// record of what it signed and refused. The keys never were in Postgres.
type Retention struct{}

// Schema is the signer's.
func (Retention) Schema() string { return "signer" }

// Run applies the rules.
func (Retention) Run(ctx context.Context, db *pg.DB, w retention.Window) ([]retention.Result, error) {
	h := w.History()
	return retention.ApplyAll(ctx, db, w, []retention.Rule{
		{Table: "signatures", Name: "signatures before the window", Cutoff: h, Where: "created_at < $1"},
		{Table: "refusals", Name: "refusals before the window", Cutoff: h, Where: "created_at < $1"},
	})
}
