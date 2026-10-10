package postgres

import (
	"context"

	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
)

// gone is an order the run deletes: ended and released before the window.
const gone = "status IN ('FILLED', 'CANCELED', 'REJECTED', 'EXPIRED') AND released AND updated_at < $1"

// Retention deletes spot-trading-service's history older than the window
// (M1): orders that ended and were released before it, then the fills of
// orders no longer kept. An order still open, or ended but not released,
// stays whatever its age, with its fills. A replayed engine update of a
// deleted order is ignored (OnUpdate logs it), and a replayed trade only
// writes its fill again, which the next run deletes: nothing of the
// balances is here (ADR-0001).
type Retention struct{}

// Schema is spot-trading-service's.
func (Retention) Schema() string { return "trading" }

// Run applies the rules in order: the orders go before their fills.
func (Retention) Run(ctx context.Context, db *pg.DB, w retention.Window) ([]retention.Result, error) {
	h := w.History()
	return retention.ApplyAll(ctx, db, w, []retention.Rule{
		{Table: "orders", Name: "orders ended and released before the window", Cutoff: h, Where: gone},
		{
			// Said of the orders kept, not of the rows left, so a dry
			// run counts the fills of the orders it would delete too.
			Table: "fills", Name: "fills before the window of orders not kept", Cutoff: h,
			Where: "executed_at < $1 AND NOT EXISTS (SELECT 1 FROM orders WHERE orders.id = fills.order_id AND NOT (" + gone + "))",
		},
	})
}
