package postgres

import (
	"context"

	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
)

// ended is a withdrawal that has ended, without a custodian's fee held for
// a person to book or write off by its ID.
const ended = `status IN ('CONFIRMED', 'INTERNAL_TRANSFER', 'REJECTED', 'CANCELED', 'FAILED') AND updated_at < $1
	AND NOT EXISTS (SELECT 1 FROM chain_fees f WHERE f.purpose = 'WITHDRAWAL' AND f.reference = withdrawals.id::text AND f.status = 'HELD')`

// Retention deletes wallet-service's history older than the window (M1):
// processed custodian callbacks that were applied, ignored or refused, chain checks
// but each network's and asset's latest, settled chain fees, finished
// commands and sweeps, daily prices. Kept for the keys' window instead:
// deposits - a deposit's row (its transaction) is what tells a
// custodian's late callback or a rescan from a new deposit, and an
// unclaimed or disputed one goes only once a person resolved it; ended
// withdrawals with their broadcast attempts - a user's monthly limit adds
// up the month's withdrawals (B200); callbacks that failed, matched
// nothing or disagreed, which a person may replay (B200). Addresses, the
// retired addresses' owners, the address book, nonces, cursors, the
// custodians' baselines and fee units, the suspensions and the
// platform's own fundings (told apart by their transaction) are state
// and stay; the scanned blocks the scanner prunes itself below its reorg
// window (Blocks.Prune).
type Retention struct{}

// Schema is wallet-service's.
func (Retention) Schema() string { return "wallet" }

// Run applies the rules in order: a withdrawal's attempts go before it.
func (Retention) Run(ctx context.Context, db *pg.DB, w retention.Window) ([]retention.Result, error) {
	h, k := w.History(), w.Keys()
	return retention.ApplyAll(ctx, db, w, []retention.Rule{
		{
			Table: "withdrawal_attempts", Name: "attempts of withdrawals ended before the keys' window", Cutoff: k,
			Where: "withdrawal_id IN (SELECT id FROM withdrawals WHERE " + ended + ")",
		},
		{Table: "withdrawals", Name: "withdrawals ended before the keys' window", Cutoff: k, Where: ended},
		{
			Table: "deposits", Name: "deposits settled before the keys' window", Cutoff: k,
			Where: "status IN ('CREDITED', 'REJECTED', 'ORPHANED') AND updated_at < $1 AND discrepancy = '' AND (NOT unclaimed OR resolution <> '')",
		},
		{
			Table: "custody_callbacks", Name: "callbacks applied, ignored or refused before the window", Cutoff: h,
			Where: "processed_at IS NOT NULL AND result IN ('APPLIED', 'IGNORED', 'REJECTED') AND received_at < $1",
		},
		{
			Table: "custody_callbacks", Name: "other processed callbacks before the keys' window", Cutoff: k,
			Where: "processed_at IS NOT NULL AND result NOT IN ('APPLIED', 'IGNORED', 'REJECTED') AND received_at < $1",
		},
		{
			Table: "chain_checks", Name: "checks before the window but the latest of each network and asset", Cutoff: h,
			Where: `checked_at < $1 AND id NOT IN (SELECT DISTINCT ON (network, asset) id FROM chain_checks ORDER BY network, asset, checked_at DESC)`,
		},
		{
			Table: "chain_fees", Name: "fees booked or written off before the window", Cutoff: h,
			Where: "(status = 'BOOKABLE' AND booked_at < $1) OR (status = 'WRITTEN_OFF' AND resolved_at < $1)",
		},
		{Table: "commands", Name: "commands done before the window", Cutoff: h, Where: "status IN ('DONE', 'FAILED') AND done_at < $1"},
		{Table: "sweeps", Name: "sweeps ended before the window", Cutoff: h, Where: "status IN ('CONFIRMED', 'FAILED') AND updated_at < $1"},
		{Table: "price_snapshots", Name: "daily prices before the window", Cutoff: h, Where: "created_at < $1"},
	})
}
