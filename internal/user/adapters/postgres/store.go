// Package postgres stores user-service's data in the users schema.
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/inbox"
	"github.com/lidp280504357/exchange/internal/platform/outbox"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/user/domain"
	"github.com/lidp280504357/exchange/internal/user/ports"
)

// Store implements ports.Store.
type Store struct {
	db     *pg.DB
	events *event.Factory
}

// NewStore returns a store whose events are built by events.
func NewStore(db *pg.DB, events *event.Factory) *Store { return &Store{db: db, events: events} }

// Tx runs fn in a transaction.
func (s *Store) Tx(ctx context.Context, fn func(ports.Repos) error) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error { return fn(repos{q: tx, events: s.events}) })
}

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

func (r repos) Users() ports.UserRepo { return users(r) }

func (r repos) Favorites() ports.FavoriteRepo { return favorites(r) }

type favorites repos

func (r favorites) Get(ctx context.Context, userID string) ([]string, time.Time, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return nil, time.Time{}, domain.ErrUserNotFound
	}
	var symbols []string
	var at time.Time
	err := r.q.QueryRow(ctx, `SELECT symbols, updated_at FROM favorites WHERE user_id = $1`, userID).Scan(&symbols, &at)
	if pg.IsNoRows(err) {
		return []string{}, time.Time{}, nil
	}
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("get favorites: %w", err)
	}
	return symbols, at, nil
}

func (r favorites) Set(ctx context.Context, userID string, symbols []string) (time.Time, error) {
	if symbols == nil {
		symbols = []string{}
	}
	var at time.Time
	err := r.q.QueryRow(ctx, `INSERT INTO favorites (user_id, symbols) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET symbols = EXCLUDED.symbols, updated_at = now() RETURNING updated_at`,
		userID, symbols).Scan(&at)
	if err != nil {
		return time.Time{}, fmt.Errorf("set favorites: %w", err)
	}
	return at, nil
}

func (r repos) Emit(ctx context.Context, topic string, msg proto.Message, aggregateType, aggregateID string) error {
	env, err := r.events.New(ctx, msg, aggregateType, aggregateID)
	if err != nil {
		return err
	}
	return outbox.Add(ctx, r.q, topic, env)
}

type users repos

const userColumns = `id, status, region, language, timezone, anti_phishing_code, kyc_level, version, created_at, updated_at`

func scanUser(row pgx.Row) (domain.User, error) {
	var u domain.User
	var id uuid.UUID
	err := row.Scan(&id, &u.Status, &u.Region, &u.Language, &u.Timezone, &u.AntiPhishingCode, &u.KYCLevel, &u.Version, &u.CreatedAt, &u.UpdatedAt)
	u.ID = id.String()
	return u, err
}

func (r users) Create(ctx context.Context, u domain.User, consents []domain.Consent) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO users (id, status, region, language, timezone) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO NOTHING`, u.ID, u.Status, u.Region, u.Language, u.Timezone)
	if err != nil {
		return false, fmt.Errorf("insert user: %w", err)
	}
	for _, c := range consents {
		if _, err := r.q.Exec(ctx, `INSERT INTO consents (user_id, document, version) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`, u.ID, c.Document, c.Version); err != nil {
			return false, fmt.Errorf("insert consent: %w", err)
		}
	}
	return tag.RowsAffected() == 1, nil
}

func (r users) get(ctx context.Context, id, suffix string) (domain.User, error) {
	if _, err := uuid.Parse(id); err != nil {
		return domain.User{}, domain.ErrUserNotFound
	}
	u, err := scanUser(r.q.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`+suffix, id))
	if pg.IsNoRows(err) {
		return domain.User{}, domain.ErrUserNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}

func (r users) Get(ctx context.Context, id string) (domain.User, error) { return r.get(ctx, id, "") }

func (r users) GetForUpdate(ctx context.Context, id string) (domain.User, error) {
	return r.get(ctx, id, " FOR UPDATE")
}

func (r users) Update(ctx context.Context, u domain.User) (domain.User, error) {
	out, err := scanUser(r.q.QueryRow(ctx, `UPDATE users SET status = $2, language = $3, timezone = $4, anti_phishing_code = $5,
		version = version + 1, updated_at = now() WHERE id = $1 RETURNING `+userColumns,
		u.ID, u.Status, u.Language, u.Timezone, u.AntiPhishingCode))
	if err != nil {
		return domain.User{}, fmt.Errorf("update user: %w", err)
	}
	return out, nil
}

func (r users) AddStatusChange(ctx context.Context, c domain.StatusChange) error {
	_, err := r.q.Exec(ctx, `INSERT INTO user_status_changes (user_id, from_status, to_status, reason_code, actor, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, c.UserID, c.From, c.To, c.Reason, c.Actor, c.At)
	if err != nil {
		return fmt.Errorf("record status change: %w", err)
	}
	return nil
}

func (r users) StatusHistory(ctx context.Context, userID string, limit int) ([]domain.StatusChange, error) {
	rows, err := r.q.Query(ctx, `SELECT from_status, to_status, reason_code, actor, created_at FROM user_status_changes
		WHERE user_id = $1 ORDER BY id DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("status history: %w", err)
	}
	defer rows.Close()
	var out []domain.StatusChange
	for rows.Next() {
		c := domain.StatusChange{UserID: userID}
		if err := rows.Scan(&c.From, &c.To, &c.Reason, &c.Actor, &c.At); err != nil {
			return nil, fmt.Errorf("status history: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r users) List(ctx context.Context, f ports.UserFilter) ([]domain.User, error) {
	var after *time.Time
	var afterID *uuid.UUID
	if f.AfterID != "" {
		id, err := uuid.Parse(f.AfterID)
		if err != nil {
			return nil, domain.ErrUserNotFound
		}
		after, afterID = &f.AfterTime, &id
	}
	var from, before *time.Time
	if !f.CreatedFrom.IsZero() {
		from = &f.CreatedFrom
	}
	if !f.CreatedBefore.IsZero() {
		before = &f.CreatedBefore
	}
	rows, err := r.q.Query(ctx, `SELECT `+userColumns+` FROM users
		WHERE ($1 = '' OR status = $1) AND ($2 = '' OR region = $2)
		AND ($3::timestamptz IS NULL OR created_at >= $3) AND ($4::timestamptz IS NULL OR created_at < $4)
		AND ($5::timestamptz IS NULL OR (created_at, id) < ($5, $6::uuid))
		ORDER BY created_at DESC, id DESC LIMIT $7`, f.Status, f.Region, from, before, after, afterID, f.Limit)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var out []domain.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("list users: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r users) Stats(ctx context.Context, since time.Time, days int) (ports.UserStats, error) {
	out := ports.UserStats{Days: map[string]int64{}}
	if err := r.q.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE created_at >= $1) FROM users`, since).
		Scan(&out.Total, &out.CreatedSince); err != nil {
		return out, fmt.Errorf("user stats: %w", err)
	}
	if days <= 0 {
		return out, nil
	}
	rows, err := r.q.Query(ctx, `SELECT to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS day, count(*) FROM users
		WHERE created_at >= (date_trunc('day', now() AT TIME ZONE 'UTC') - make_interval(days => $1 - 1)) AT TIME ZONE 'UTC'
		GROUP BY day`, days)
	if err != nil {
		return out, fmt.Errorf("user stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var day string
		var n int64
		if err := rows.Scan(&day, &n); err != nil {
			return out, fmt.Errorf("user stats: %w", err)
		}
		out.Days[day] = n
	}
	return out, rows.Err()
}
