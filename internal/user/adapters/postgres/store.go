// Package postgres stores user-service's data in the users schema.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/inbox"
	"github.com/skill/exchange/internal/platform/outbox"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/user/domain"
	"github.com/skill/exchange/internal/user/ports"
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

const userColumns = `id, status, region, language, timezone, anti_phishing_code, kyc_level, version, created_at, updated_at,
	username, username_changed_at, avatar, kind, purged_at`

func scanUser(row pgx.Row) (domain.User, error) {
	var u domain.User
	var id uuid.UUID
	var changed, purged *time.Time
	var avatar []byte
	err := row.Scan(&id, &u.Status, &u.Region, &u.Language, &u.Timezone, &u.AntiPhishingCode, &u.KYCLevel, &u.Version, &u.CreatedAt, &u.UpdatedAt,
		&u.Username, &changed, &avatar, &u.Kind, &purged)
	if err != nil {
		return u, err
	}
	u.ID = id.String()
	if changed != nil {
		u.UsernameChangedAt = *changed
	}
	if purged != nil {
		u.PurgedAt = *purged
	}
	if avatar != nil {
		u.Avatar = &domain.Avatar{}
		if err := json.Unmarshal(avatar, u.Avatar); err != nil {
			return u, fmt.Errorf("user %s's avatar: %w", u.ID, err)
		}
	}
	return u, nil
}

// usernameTaken maps a clash on the username's unique index (design
// 2026-10-07: unique whatever the case) to domain.ErrUsernameTaken.
func usernameTaken(err error) error {
	if c, ok := pg.UniqueViolation(err); ok && c == "users_username_lower" {
		return domain.ErrUsernameTaken
	}
	return err
}

func (r users) Create(ctx context.Context, u domain.User, consents []domain.Consent) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO users (id, status, region, language, timezone, username) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO NOTHING`, u.ID, u.Status, u.Region, u.Language, u.Timezone, u.Username)
	if err := usernameTaken(err); errors.Is(err, domain.ErrUsernameTaken) {
		return false, err
	}
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
	var changed *time.Time
	if !u.UsernameChangedAt.IsZero() {
		changed = &u.UsernameChangedAt
	}
	var avatar []byte
	if u.Avatar != nil {
		var err error
		if avatar, err = json.Marshal(u.Avatar); err != nil {
			return domain.User{}, err
		}
	}
	out, err := scanUser(r.q.QueryRow(ctx, `UPDATE users SET status = $2, language = $3, timezone = $4, anti_phishing_code = $5,
		username = $6, username_changed_at = $7, avatar = $8, version = version + 1, updated_at = now() WHERE id = $1 RETURNING `+userColumns,
		u.ID, u.Status, u.Language, u.Timezone, u.AntiPhishingCode, u.Username, changed, avatar))
	if err := usernameTaken(err); errors.Is(err, domain.ErrUsernameTaken) {
		return domain.User{}, err
	}
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

func (r users) Consents(ctx context.Context, userID string) ([]domain.Consent, error) {
	rows, err := r.q.Query(ctx, `SELECT document, version, accepted_at FROM consents WHERE user_id = $1
		ORDER BY accepted_at DESC, document`, userID)
	if err != nil {
		return nil, fmt.Errorf("consents: %w", err)
	}
	defer rows.Close()
	var out []domain.Consent
	for rows.Next() {
		var c domain.Consent
		if err := rows.Scan(&c.Document, &c.Version, &c.AcceptedAt); err != nil {
			return nil, fmt.Errorf("consents: %w", err)
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
	ids := make([]uuid.UUID, 0, len(f.IDs))
	for _, s := range f.IDs {
		if id, err := uuid.Parse(s); err == nil {
			ids = append(ids, id)
		}
	}
	rows, err := r.q.Query(ctx, `SELECT `+userColumns+` FROM users
		WHERE ($1 = '' OR status = $1) AND ($2 = '' OR region = $2)
		AND ($3::timestamptz IS NULL OR created_at >= $3) AND ($4::timestamptz IS NULL OR created_at < $4)
		AND ($5::timestamptz IS NULL OR (created_at, id) < ($5, $6::uuid))
		AND ($8 = '' OR strpos(lower(username), lower($8)) > 0 OR id = ANY($9::uuid[]))
		AND (cardinality($10::text[]) = 0 OR kind = ANY($10)) AND ($11 OR purged_at IS NULL)
		ORDER BY created_at DESC, id DESC LIMIT $7`, f.Status, f.Region, from, before, after, afterID, f.Limit, f.Q, ids, kindsOrEmpty(f.Kinds),
		f.IncludePurged)
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

// kindsOrEmpty keeps a nil filter an empty array (no NULL in the query).
func kindsOrEmpty(kinds []string) []string {
	if kinds == nil {
		return []string{}
	}
	return kinds
}

func (r users) SetKind(ctx context.Context, userID, kind string) error {
	tag, err := r.q.Exec(ctx, `UPDATE users SET kind = $2, version = version + 1, updated_at = now() WHERE id = $1`, userID, kind)
	if err != nil {
		return fmt.Errorf("set kind: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrUserNotFound
	}
	return nil
}

func (r users) AddKindChange(ctx context.Context, c domain.KindChange) error {
	_, err := r.q.Exec(ctx, `INSERT INTO user_kind_changes (user_id, from_kind, to_kind, actor, reason, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, c.UserID, c.From, c.To, c.Actor, c.Reason, c.At)
	if err != nil {
		return fmt.Errorf("record kind change: %w", err)
	}
	return nil
}

func (r users) SetPurged(ctx context.Context, userID string, at time.Time) error {
	tag, err := r.q.Exec(ctx, `UPDATE users SET purged_at = $2, version = version + 1, updated_at = now() WHERE id = $1`, userID, at)
	if err != nil {
		return fmt.Errorf("set purged: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrUserNotFound
	}
	return nil
}

func (r users) IDsOfKinds(ctx context.Context, kinds []string) ([]string, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text FROM users WHERE kind = ANY($1) ORDER BY id`, kindsOrEmpty(kinds))
	if err != nil {
		return nil, fmt.Errorf("users of kinds: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("users of kinds: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r users) FindUsername(ctx context.Context, name string) (string, error) {
	var id string
	err := r.q.QueryRow(ctx, `SELECT id::text FROM users WHERE lower(username) = lower($1)`, name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrUserNotFound
	}
	if err != nil {
		return "", fmt.Errorf("find username: %w", err)
	}
	return id, nil
}

func (r users) Stats(ctx context.Context, since time.Time, days int) (ports.UserStats, error) {
	out := ports.UserStats{Days: map[string]int64{}, ByKind: map[string]ports.KindCount{}}
	kinds, err := r.q.Query(ctx, `SELECT kind, count(*), count(*) FILTER (WHERE created_at >= $1) FROM users WHERE purged_at IS NULL GROUP BY kind`, since)
	if err != nil {
		return out, fmt.Errorf("user stats: %w", err)
	}
	for kinds.Next() {
		var kind string
		var c ports.KindCount
		if err := kinds.Scan(&kind, &c.Total, &c.CreatedSince); err != nil {
			kinds.Close()
			return out, fmt.Errorf("user stats: %w", err)
		}
		out.ByKind[kind] = c
		out.Total += c.Total
		out.CreatedSince += c.CreatedSince
	}
	kinds.Close()
	if err := kinds.Err(); err != nil {
		return out, fmt.Errorf("user stats: %w", err)
	}
	if days <= 0 {
		return out, nil
	}
	rows, err := r.q.Query(ctx, `SELECT to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS day, count(*) FROM users
		WHERE created_at >= (date_trunc('day', now() AT TIME ZONE 'UTC') - make_interval(days => $1 - 1)) AT TIME ZONE 'UTC'
		AND purged_at IS NULL GROUP BY day`, days)
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
