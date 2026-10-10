package postgres

import (
	"context"

	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
)

// Retention deletes notification-service's history older than the window
// (M1): users' notifications, deliveries that are done (sent or failed for
// good) and wait for nothing, finished broadcasts and the dev inbox's
// messages - what the service's own purge deletes after half a year
// (PurgeNotices and the others). Articles are content and stay; a delivery
// still queued or retrying stays.
type Retention struct{}

// Schema is notification-service's.
func (Retention) Schema() string { return "notify" }

// Run applies the rules.
func (Retention) Run(ctx context.Context, db *pg.DB, w retention.Window) ([]retention.Result, error) {
	h := w.History()
	return retention.ApplyAll(ctx, db, w, []retention.Rule{
		{Table: "notifications", Name: "notifications before the window", Cutoff: h, Where: "created_at < $1"},
		{
			Table: "deliveries", Name: "deliveries done before the window", Cutoff: h,
			Where: "status IN ('SENT', 'FAILED') AND next_attempt_at IS NULL AND created_at < $1",
		},
		{Table: "broadcasts", Name: "broadcasts finished before the window", Cutoff: h, Where: "status IN ('SENT', 'FAILED') AND created_at < $1"},
		{Table: "mock_messages", Name: "dev inbox messages before the window", Cutoff: h, Where: "created_at < $1"},
	})
}
