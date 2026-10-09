// Package postgres stores auth-service's data in the auth schema.
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	"github.com/skill/exchange/internal/auth/domain"
	"github.com/skill/exchange/internal/auth/ports"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/inbox"
	"github.com/skill/exchange/internal/platform/outbox"
	"github.com/skill/exchange/internal/platform/pg"
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

// Read returns repositories on the pool.
func (s *Store) Read() ports.Repos { return repos{q: s.db, events: s.events} }

// Once runs fn in a transaction that also records the event in the inbox.
func (s *Store) Once(ctx context.Context, consumer, eventID string, fn func(ports.Repos) error) (bool, error) {
	return inbox.ProcessID(ctx, s.db, consumer, eventID, func(_ context.Context, tx pgx.Tx) error {
		return fn(repos{q: tx, events: s.events})
	})
}

// Purge deletes short-lived records that expired before cutoff: OTP
// challenges and tickets, login challenges, step-up tokens and refresh
// tokens. Sessions and the login history stay.
func (s *Store) Purge(ctx context.Context, cutoff time.Time) (int64, error) {
	var total int64
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		total = 0
		for _, q := range []string{
			`DELETE FROM otp_tickets WHERE expires_at < $1`,
			`DELETE FROM otp_challenges c WHERE expires_at < $1
				AND NOT EXISTS (SELECT 1 FROM otp_tickets t WHERE t.challenge_id = c.id)`,
			`DELETE FROM login_challenges WHERE expires_at < $1`,
			`DELETE FROM step_up_tokens WHERE expires_at < $1`,
			`DELETE FROM refresh_tokens WHERE expires_at < $1`,
		} {
			tag, err := tx.Exec(ctx, q, cutoff)
			if err != nil {
				return fmt.Errorf("purge: %w", err)
			}
			total += tag.RowsAffected()
		}
		return nil
	})
	return total, err
}

// repos binds the repositories to a querier: the pool or a transaction.
type repos struct {
	q      pg.Querier
	events *event.Factory
}

func (r repos) Challenges() ports.ChallengeRepo { return challenges(r) }
func (r repos) Tickets() ports.TicketRepo       { return tickets(r) }
func (r repos) Identities() ports.IdentityRepo  { return identities(r) }

func (r repos) Emit(ctx context.Context, msg proto.Message, aggregateType, aggregateID string) error {
	env, err := r.events.New(ctx, msg, aggregateType, aggregateID)
	if err != nil {
		return err
	}
	return outbox.Add(ctx, r.q, event.TopicAuth, env)
}

type challenges repos

func (r challenges) Create(ctx context.Context, c *domain.Challenge) error {
	_, err := r.q.Exec(ctx, `INSERT INTO otp_challenges (id, scene, channel, target, user_id, login_challenge_id,
		device_id, code_hash, attempts, status, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		c.ID, string(c.Scene), string(c.Channel), c.Target, nullUUID(c.UserID), nullUUID(c.LoginChallengeID),
		c.DeviceID, c.CodeHash, c.Attempts, c.Status, c.ExpiresAt, c.CreatedAt)
	if err != nil {
		return fmt.Errorf("create challenge: %w", err)
	}
	return nil
}

func (r challenges) GetForUpdate(ctx context.Context, id string) (*domain.Challenge, error) {
	var c domain.Challenge
	var scene, channel string
	var userID, loginID *string
	var verifiedAt *time.Time
	err := r.q.QueryRow(ctx, `SELECT id, scene, channel, target, user_id::text, login_challenge_id::text, device_id,
		code_hash, attempts, status, expires_at, created_at, verified_at
		FROM otp_challenges WHERE id = $1 FOR UPDATE`, id).
		Scan(&c.ID, &scene, &channel, &c.Target, &userID, &loginID, &c.DeviceID,
			&c.CodeHash, &c.Attempts, &c.Status, &c.ExpiresAt, &c.CreatedAt, &verifiedAt)
	if pg.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load challenge: %w", err)
	}
	c.Scene, c.Channel = domain.Scene(scene), domain.Channel(channel)
	c.UserID, c.LoginChallengeID = deref(userID), deref(loginID)
	if verifiedAt != nil {
		c.VerifiedAt = *verifiedAt
	}
	return &c, nil
}

func (r challenges) Update(ctx context.Context, c *domain.Challenge) error {
	var verifiedAt any
	if !c.VerifiedAt.IsZero() {
		verifiedAt = c.VerifiedAt
	}
	_, err := r.q.Exec(ctx, `UPDATE otp_challenges SET attempts = $2, status = $3, verified_at = $4 WHERE id = $1`,
		c.ID, c.Attempts, c.Status, verifiedAt)
	if err != nil {
		return fmt.Errorf("update challenge: %w", err)
	}
	return nil
}

type tickets repos

func (r tickets) Create(ctx context.Context, t domain.Ticket) error {
	_, err := r.q.Exec(ctx, `INSERT INTO otp_tickets (ticket_hash, challenge_id, scene, device_id, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, t.Hash, t.ChallengeID, string(t.Scene), t.DeviceID, t.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create ticket: %w", err)
	}
	return nil
}

func (r tickets) Consume(ctx context.Context, hash []byte, scene domain.Scene, deviceID string, now time.Time) (string, error) {
	var id string
	err := r.q.QueryRow(ctx, `UPDATE otp_tickets SET consumed_at = $4
		WHERE ticket_hash = $1 AND scene = $2 AND device_id = $3 AND consumed_at IS NULL AND expires_at > $4
		RETURNING challenge_id::text`, hash, string(scene), deviceID, now).Scan(&id)
	if pg.IsNoRows(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("consume ticket: %w", err)
	}
	return id, nil
}

type identities repos

func (r identities) Find(ctx context.Context, kind, value string) (*domain.Identity, error) {
	var id domain.Identity
	err := r.q.QueryRow(ctx, `SELECT id::text, user_id::text, kind, value FROM identities WHERE kind = $1 AND value = $2`,
		kind, value).Scan(&id.ID, &id.UserID, &id.Kind, &id.Value)
	if pg.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find identity: %w", err)
	}
	return &id, nil
}

func (r identities) SearchUsers(ctx context.Context, q string, limit int) ([]string, error) {
	rows, err := r.q.Query(ctx, `SELECT user_id::text FROM identities WHERE strpos(lower(value), lower($1)) > 0
		GROUP BY user_id ORDER BY user_id DESC LIMIT $2`, q, limit)
	if err != nil {
		return nil, fmt.Errorf("search identities: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("search identities: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r identities) ByUser(ctx context.Context, userID string) ([]domain.Identity, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, user_id::text, kind, value, verified_at, created_at FROM identities
		WHERE user_id = $1 ORDER BY kind`, userID)
	if err != nil {
		return nil, fmt.Errorf("list identities: %w", err)
	}
	defer rows.Close()
	var out []domain.Identity
	for rows.Next() {
		var id domain.Identity
		if err := rows.Scan(&id.ID, &id.UserID, &id.Kind, &id.Value, &id.VerifiedAt, &id.CreatedAt); err != nil {
			return nil, fmt.Errorf("list identities: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func nullUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
