// Package postgres stores risk data in the risk schema.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/inbox"
	"github.com/lidp280504357/exchange/internal/platform/outbox"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/risk/domain"
	"github.com/lidp280504357/exchange/internal/risk/ports"
)

// Store implements ports.Store.
type Store struct {
	db     *pg.DB
	events *event.Factory
}

// NewStore returns a store whose events are built by events.
func NewStore(db *pg.DB, events *event.Factory) *Store { return &Store{db: db, events: events} }

// Once runs fn in a transaction that records the event for consumer.
func (s *Store) Once(ctx context.Context, consumer, eventID string, fn func(ports.Repos) error) (bool, error) {
	return inbox.ProcessID(ctx, s.db, consumer, eventID, func(_ context.Context, tx pgx.Tx) error {
		return fn(repos{q: tx, events: s.events})
	})
}

// Read returns repositories on the pool.
func (s *Store) Read() ports.Repos { return repos{q: s.db, events: s.events} }

type repos struct {
	q      pg.Querier
	events *event.Factory
}

func (r repos) Devices() ports.DeviceRepo         { return devices(r) }
func (r repos) Velocity() ports.VelocityRepo      { return velocity(r) }
func (r repos) Assessments() ports.AssessmentRepo { return assessments(r) }

func (r repos) Emit(ctx context.Context, topic string, msg proto.Message, aggregateType, aggregateID string) error {
	env, err := r.events.New(ctx, msg, aggregateType, aggregateID)
	if err != nil {
		return err
	}
	return outbox.Add(ctx, r.q, topic, env)
}

type devices repos

func (r devices) Record(ctx context.Context, userID, deviceID string, at time.Time) (bool, error) {
	var inserted bool
	err := r.q.QueryRow(ctx, `INSERT INTO user_devices (user_id, device_id, first_seen_at) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING RETURNING true`, userID, deviceID, at).Scan(&inserted)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil // seen before
	case err != nil:
		return false, fmt.Errorf("record device: %w", err)
	}
	var others bool
	if err := r.q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM user_devices WHERE user_id = $1 AND device_id <> $2)`,
		userID, deviceID).Scan(&others); err != nil {
		return false, fmt.Errorf("record device: %w", err)
	}
	return others, nil
}

type velocity repos

func (r velocity) Tally(ctx context.Context, rule, value, eventID string, at time.Time, window time.Duration) (int, error) {
	if _, err := r.q.Exec(ctx, `INSERT INTO velocity_events (rule, key, event_id, at) VALUES ($1, $2, $3, $4)
		ON CONFLICT DO NOTHING`, rule, value, eventID, at); err != nil {
		return 0, fmt.Errorf("tally %s: %w", rule, err)
	}
	var n int
	err := r.q.QueryRow(ctx, `SELECT count(*) FROM velocity_events WHERE rule = $1 AND key = $2 AND at > $3 AND at <= $4`,
		rule, value, at.Add(-window), at).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("tally %s: %w", rule, err)
	}
	return n, nil
}

func (r velocity) Purge(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := r.q.Exec(ctx, `DELETE FROM velocity_events WHERE at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("purge velocity events: %w", err)
	}
	return tag.RowsAffected(), nil
}

type assessments repos

func (r assessments) Insert(ctx context.Context, a domain.Record) error {
	hits, err := json.Marshal(a.Hits)
	if err != nil {
		return err
	}
	_, err = r.q.Exec(ctx, `INSERT INTO assessments (id, user_id, source_event_id, source_event_type, score, action, hits, enforced, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		a.ID, a.UserID, a.SourceEventID, a.SourceEventType, a.Score, a.Action.String(), hits, a.Enforced, a.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert assessment: %w", err)
	}
	return nil
}

func (r assessments) List(ctx context.Context, userID string, limit int) ([]domain.Record, error) {
	rows, err := r.q.Query(ctx, `SELECT id, user_id, source_event_id, source_event_type, score, action, hits, enforced, created_at
		FROM assessments WHERE $1 = '' OR user_id::text = $1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list assessments: %w", err)
	}
	defer rows.Close()
	var out []domain.Record
	for rows.Next() {
		var a domain.Record
		var action string
		var hits []byte
		if err := rows.Scan(&a.ID, &a.UserID, &a.SourceEventID, &a.SourceEventType, &a.Score, &action, &hits, &a.Enforced, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("list assessments: %w", err)
		}
		if a.Action, err = domain.ParseAction(action); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(hits, &a.Hits); err != nil {
			return nil, fmt.Errorf("list assessments: hits: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
