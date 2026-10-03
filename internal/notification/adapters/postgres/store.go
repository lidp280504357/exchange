// Package postgres stores notification-service's data in the notify schema.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	notificationv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/notification/v1"
	"github.com/lidp280504357/exchange/internal/notification/domain"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/inbox"
	"github.com/lidp280504357/exchange/internal/platform/outbox"
	"github.com/lidp280504357/exchange/internal/platform/pg"
)

// Store implements ports.DeliveryStore and ports.NoticeStore.
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
		if _, err := tx.Exec(ctx, `UPDATE deliveries SET status = 'FAILED', failure_class = $2, next_attempt_at = NULL, updated_at = now()
			WHERE id = $1`, d.ID, string(class)); err != nil {
			return fmt.Errorf("fail delivery: %w", err)
		}
		return outbox.Add(ctx, tx, event.TopicNotification, env)
	})
}

// CreateNotice stores n with its NotificationCreated event, once per
// consumed event.
func (s *Store) CreateNotice(ctx context.Context, consumer, eventID string, n domain.Notice, mail *domain.Delivery) (bool, error) {
	env, err := s.events.New(ctx, &notificationv1.NotificationCreated{
		NotificationId: n.ID, UserId: n.UserID, Type: n.Type, Title: n.Title, Body: n.Body,
	}, "user", n.UserID)
	if err != nil {
		return false, err
	}
	data, err := json.Marshal(n.Data)
	if err != nil {
		return false, fmt.Errorf("notice data: %w", err)
	}
	return inbox.ProcessID(ctx, s.db, consumer, eventID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO notifications (id, user_id, type, title, body, data, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, n.ID, n.UserID, n.Type, n.Title, n.Body, data, n.CreatedAt); err != nil {
			return fmt.Errorf("create notice: %w", err)
		}
		if mail != nil {
			if _, err := tx.Exec(ctx, `INSERT INTO deliveries (id, kind, channel, template, target_mask, user_id, next_attempt_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (id) DO NOTHING`,
				mail.ID, string(mail.Kind), string(mail.Channel), mail.Template, mail.TargetMask, mail.UserID, n.CreatedAt); err != nil {
				return fmt.Errorf("queue notice mail: %w", err)
			}
		}
		return outbox.Add(ctx, tx, event.TopicNotification, env)
	})
}

// ListNotices pages through a user's notices, newest first.
func (s *Store) ListNotices(ctx context.Context, userID, beforeID string, limit int) ([]domain.Notice, error) {
	var before any
	if beforeID != "" {
		before = beforeID
	}
	rows, err := s.db.Query(ctx, `SELECT id, type, title, body, data, created_at, read_at FROM notifications
		WHERE user_id = $1 AND ($2::uuid IS NULL OR id < $2::uuid) ORDER BY id DESC LIMIT $3`, userID, before, limit)
	if err != nil {
		return nil, fmt.Errorf("list notices: %w", err)
	}
	defer rows.Close()
	var out []domain.Notice
	for rows.Next() {
		n := domain.Notice{UserID: userID}
		var id uuid.UUID
		var data []byte
		var read *time.Time
		if err := rows.Scan(&id, &n.Type, &n.Title, &n.Body, &data, &n.CreatedAt, &read); err != nil {
			return nil, fmt.Errorf("list notices: %w", err)
		}
		n.ID = id.String()
		if err := json.Unmarshal(data, &n.Data); err != nil {
			return nil, fmt.Errorf("notice data: %w", err)
		}
		if read != nil {
			n.ReadAt = *read
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// UnreadCount counts a user's unread notices.
func (s *Store) UnreadCount(ctx context.Context, userID string) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id = $1 AND read_at IS NULL`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count unread notices: %w", err)
	}
	return n, nil
}

// MarkRead marks the given notices, or all of the user's, as read.
func (s *Store) MarkRead(ctx context.Context, userID string, ids []string) (int64, error) {
	var tag pgconn.CommandTag
	var err error
	if len(ids) == 0 {
		tag, err = s.db.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE user_id = $1 AND read_at IS NULL`, userID)
	} else {
		tag, err = s.db.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE user_id = $1 AND read_at IS NULL
			AND id = ANY($2::uuid[])`, userID, ids)
	}
	if err != nil {
		return 0, fmt.Errorf("mark notices read: %w", err)
	}
	return tag.RowsAffected(), nil
}
