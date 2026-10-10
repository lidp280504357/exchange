package postgres

import (
	"context"

	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/retention"
)

// Retention deletes auth's history older than the window (M1): sign-in
// records, revoked sessions with their refresh tokens, decided rebinding
// requests. Identities, credentials, authenticators and known devices are
// the accounts' state and stay; expired codes and tokens have their own
// cleanup (Store.Purge).
type Retention struct{}

// Schema is auth's.
func (Retention) Schema() string { return "auth" }

// Run applies the rules in order: a session's refresh tokens go before it.
func (Retention) Run(ctx context.Context, db *pg.DB, w retention.Window) ([]retention.Result, error) {
	h := w.History()
	return retention.ApplyAll(ctx, db, w, []retention.Rule{
		{Table: "login_history", Name: "sign-ins before the window", Cutoff: h, Where: "created_at < $1"},
		{
			Table: "refresh_tokens", Name: "tokens of sessions revoked before the window", Cutoff: h,
			Where: "session_id IN (SELECT id FROM sessions WHERE revoked_at < $1)",
		},
		{Table: "sessions", Name: "sessions revoked before the window", Cutoff: h, Where: "revoked_at < $1"},
		{
			// It ends with its last refresh token, which the service's
			// own purge deletes once expired (B200).
			Table: "sessions", Name: "sessions idle since before the window, their tokens expired", Cutoff: h,
			Where: "revoked_at IS NULL AND coalesce(last_seen_at, created_at) < $1 AND NOT EXISTS (SELECT 1 FROM refresh_tokens r WHERE r.session_id = sessions.id)",
		},
		{
			Table: "identity_rebind_requests", Name: "rebinding requests decided before the window", Cutoff: h,
			Where: "status <> 'PENDING_REVIEW' AND decided_at < $1",
		},
	})
}
