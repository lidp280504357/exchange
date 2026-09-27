// Package postgres stores notification-service's data in the notify schema.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	notificationv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/notification/v1"
	"github.com/lidp280504357/exchange/internal/notification/domain"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/outbox"
	"github.com/lidp280504357/exchange/internal/platform/pg"
)

// Store implements ports.DeliveryStore.
type Store struct {
	db     *pg.DB
	events *event.Factory
}

// NewStore returns a store that emits events built by events.
func NewStore(db *pg.DB, events *event.Factory) *Store { return &Store{db: db, events: events} }

// CreateDelivery inserts a QUEUED delivery.
func (s *Store) CreateDelivery(ctx context.Context, d domain.Delivery) error {
	var userID any
	if d.UserID != "" {
		userID = d.UserID
	}
	_, err := s.db.Exec(ctx, `INSERT INTO deliveries (id, kind, channel, template, target_mask, user_id)
		VALUES ($1, $2, $3, $4, $5, $6)`, d.ID, string(d.Kind), string(d.Channel), d.Template, d.TargetMask, userID)
	if err != nil {
		return fmt.Errorf("create delivery: %w", err)
	}
	return nil
}

// RecordAttempt stores one provider attempt.
func (s *Store) RecordAttempt(ctx context.Context, id string, status domain.Status, provider string, class domain.FailureClass, providerMessageID string) error {
	_, err := s.db.Exec(ctx, `UPDATE deliveries SET status = $2, provider = $3, attempts = attempts + 1,
		failure_class = $4, provider_message_id = CASE WHEN $5 = '' THEN provider_message_id ELSE $5 END,
		updated_at = now() WHERE id = $1`, id, string(status), provider, string(class), providerMessageID)
	if err != nil {
		return fmt.Errorf("record attempt: %w", err)
	}
	return nil
}

// FailDelivery marks the delivery FAILED and queues DeliveryFailed.
func (s *Store) FailDelivery(ctx context.Context, d domain.Delivery, class domain.FailureClass) error {
	key := d.UserID
	if key == "" {
		key = d.ID
	}
	env, err := s.events.New(ctx, &notificationv1.DeliveryFailed{
		DeliveryId: d.ID, UserId: d.UserID, Channel: string(d.Channel), Template: d.Template, FailureClass: string(class),
	}, "delivery", key)
	if err != nil {
		return err
	}
	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE deliveries SET status = 'FAILED', failure_class = $2, updated_at = now()
			WHERE id = $1`, d.ID, string(class)); err != nil {
			return fmt.Errorf("fail delivery: %w", err)
		}
		return outbox.Add(ctx, tx, event.TopicNotification, env)
	})
}
