// Package inbox makes event handlers idempotent (requirements §8.2): the
// event ID is recorded in the same transaction as the handler's changes,
// so a redelivered event is skipped instead of applied twice.
package inbox

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	"github.com/lidp280504357/exchange/internal/platform/pg"
)

// Process runs fn in a transaction that also records env for consumer.
// If consumer already handled env, fn is not called and processed is
// false. An error from fn rolls everything back, so the event can be
// retried.
func Process(ctx context.Context, db *pg.DB, consumer string, env *eventv1.Envelope, fn func(ctx context.Context, tx pgx.Tx) error) (processed bool, err error) {
	err = db.InTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO inbox (consumer, event_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			consumer, env.GetEventId())
		if err != nil {
			return fmt.Errorf("inbox: record: %w", err)
		}
		if tag.RowsAffected() == 0 {
			processed = false
			return nil
		}
		processed = true
		return fn(ctx, tx)
	})
	if err != nil {
		return false, err
	}
	return processed, nil
}

// Purge deletes entries received before cutoff. Keep them longer than the
// longest topic retention (30 days), or old redeliveries would be applied.
func Purge(ctx context.Context, q pg.Querier, cutoff time.Time) (int64, error) {
	tag, err := q.Exec(ctx, `DELETE FROM inbox WHERE received_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("inbox: purge: %w", err)
	}
	return tag.RowsAffected(), nil
}
