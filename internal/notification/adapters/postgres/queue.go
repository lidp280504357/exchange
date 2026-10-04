package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/skill/exchange/internal/notification/domain"
)

// The mails sent later, the retention of old rows and a notice by its ID
// (C5.5 ⑫: ports.MailQueue, ports.RetentionStore).

// TakeDue holds up to limit due deliveries until now plus lease and
// returns them.
func (s *Store) TakeDue(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]domain.Delivery, error) {
	rows, err := s.db.Query(ctx, `UPDATE deliveries d SET next_attempt_at = $2 FROM (
			SELECT id FROM deliveries WHERE next_attempt_at <= $1 AND status NOT IN ('SENT', 'FAILED')
			ORDER BY next_attempt_at LIMIT $3 FOR UPDATE SKIP LOCKED
		) due WHERE d.id = due.id
		RETURNING d.id, d.kind, d.channel, d.template, d.target_mask, d.user_id, d.created_at, d.attempts, d.rounds`,
		now, now.Add(lease), limit)
	if err != nil {
		return nil, fmt.Errorf("due deliveries: %w", err)
	}
	defer rows.Close()
	var out []domain.Delivery
	for rows.Next() {
		var d domain.Delivery
		var id uuid.UUID
		var user *uuid.UUID
		var kind, channel string
		if err := rows.Scan(&id, &kind, &channel, &d.Template, &d.TargetMask, &user, &d.CreatedAt, &d.Attempts, &d.Rounds); err != nil {
			return nil, fmt.Errorf("due deliveries: %w", err)
		}
		d.ID, d.Kind, d.Channel = id.String(), domain.Kind(kind), domain.Channel(channel)
		if user != nil {
			d.UserID = user.String()
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Retry makes a queued delivery due again at at, a failed round more.
func (s *Store) Retry(ctx context.Context, id string, at time.Time) error {
	if _, err := s.db.Exec(ctx, `UPDATE deliveries SET next_attempt_at = $2, rounds = rounds + 1, updated_at = now() WHERE id = $1`,
		id, at); err != nil {
		return fmt.Errorf("retry delivery: %w", err)
	}
	return nil
}

// Settle ends a queued delivery's wait.
func (s *Store) Settle(ctx context.Context, id string) error {
	if _, err := s.db.Exec(ctx, `UPDATE deliveries SET next_attempt_at = NULL, updated_at = now() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("settle delivery: %w", err)
	}
	return nil
}

// Notice reads one notice by its ID.
func (s *Store) Notice(ctx context.Context, id string) (*domain.Notice, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, nil
	}
	var n domain.Notice
	var nid, user uuid.UUID
	var data []byte
	var read *time.Time
	err := s.db.QueryRow(ctx, `SELECT id, user_id, type, title, body, data, created_at, read_at FROM notifications WHERE id = $1`, id).
		Scan(&nid, &user, &n.Type, &n.Title, &n.Body, &data, &n.CreatedAt, &read)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("notice: %w", err)
	}
	n.ID, n.UserID = nid.String(), user.String()
	if err := json.Unmarshal(data, &n.Data); err != nil {
		return nil, fmt.Errorf("notice data: %w", err)
	}
	if read != nil {
		n.ReadAt = *read
	}
	return &n, nil
}

// PurgeNotices deletes up to limit notices created before before.
func (s *Store) PurgeNotices(ctx context.Context, before time.Time, limit int) (int64, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM notifications WHERE id IN (
		SELECT id FROM notifications WHERE created_at < $1 LIMIT $2)`, before, limit)
	if err != nil {
		return 0, fmt.Errorf("purge notices: %w", err)
	}
	return tag.RowsAffected(), nil
}

// PurgeBroadcasts deletes up to limit broadcasts created before before
// that are not sending (FAILED ones too: half a year on, nobody resumes
// them).
func (s *Store) PurgeBroadcasts(ctx context.Context, before time.Time, limit int) (int64, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM broadcasts WHERE id IN (
		SELECT id FROM broadcasts WHERE created_at < $1 AND status <> 'SENDING' LIMIT $2)`, before, limit)
	if err != nil {
		return 0, fmt.Errorf("purge broadcasts: %w", err)
	}
	return tag.RowsAffected(), nil
}

// PurgeDeliveries deletes up to limit delivery records created before
// before that wait for nothing.
func (s *Store) PurgeDeliveries(ctx context.Context, before time.Time, limit int) (int64, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM deliveries WHERE id IN (
		SELECT id FROM deliveries WHERE created_at < $1 AND next_attempt_at IS NULL LIMIT $2)`, before, limit)
	if err != nil {
		return 0, fmt.Errorf("purge deliveries: %w", err)
	}
	return tag.RowsAffected(), nil
}
