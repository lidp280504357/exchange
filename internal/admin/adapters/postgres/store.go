// Package postgres stores the admin console in the admin schema.
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

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/outbox"
	"github.com/lidp280504357/exchange/internal/platform/pg"
)

// Store implements ports.Store.
type Store struct {
	db     *pg.DB
	events *event.Factory
}

// NewStore returns a store whose audit events are built by events.
func NewStore(db *pg.DB, events *event.Factory) *Store { return &Store{db: db, events: events} }

// Tx runs fn in a transaction.
func (s *Store) Tx(ctx context.Context, fn func(ports.Repos) error) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error { return fn(repos{q: tx, events: s.events}) })
}

// Read returns repositories on the pool.
func (s *Store) Read() ports.Repos { return repos{q: s.db, events: s.events} }

type repos struct {
	q      pg.Querier
	events *event.Factory
}

func (r repos) Admins() ports.AdminRepo       { return admins(r) }
func (r repos) Sessions() ports.SessionRepo   { return sessions(r) }
func (r repos) Approvals() ports.ApprovalRepo { return approvals(r) }

func (r repos) Audit(ctx context.Context, msg proto.Message, actor string) error {
	env, err := r.events.New(ctx, msg, "actor", actor)
	if err != nil {
		return err
	}
	return outbox.Add(ctx, r.q, event.TopicAudit, env)
}

func at(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return *p
}

func stamp(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

type admins repos

const adminColumns = `id, email, name, role, password_hash, totp_sealed, totp_last_step, status, failed_attempts, locked_until,
	last_login_at, created_at`

func scanAdmin(row pgx.Row) (domain.Admin, error) {
	var a domain.Admin
	var locked, last *time.Time
	var failed int32
	err := row.Scan(&a.ID, &a.Email, &a.Name, &a.Role, &a.PasswordHash, &a.TOTPSealed, &a.TOTPLastStep, &a.Status, &failed, &locked,
		&last, &a.CreatedAt)
	a.FailedAttempts, a.LockedUntil, a.LastLoginAt = int(failed), at(locked), at(last)
	return a, err
}

func (r admins) one(ctx context.Context, sql string, args ...any) (*domain.Admin, error) {
	a, err := scanAdmin(r.q.QueryRow(ctx, sql, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get admin: %w", err)
	}
	return &a, nil
}

func (r admins) Insert(ctx context.Context, a domain.Admin) error {
	_, err := r.q.Exec(ctx, `INSERT INTO admins (`+adminColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		a.ID, a.Email, a.Name, a.Role, a.PasswordHash, a.TOTPSealed, a.TOTPLastStep, a.Status, a.FailedAttempts,
		stamp(a.LockedUntil), stamp(a.LastLoginAt), a.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert admin: %w", err)
	}
	return nil
}

func (r admins) Update(ctx context.Context, a domain.Admin) error {
	_, err := r.q.Exec(ctx, `UPDATE admins SET name = $2, role = $3, password_hash = $4, totp_sealed = $5, totp_last_step = $6,
		status = $7, failed_attempts = $8, locked_until = $9, last_login_at = $10, updated_at = now() WHERE id = $1`,
		a.ID, a.Name, a.Role, a.PasswordHash, a.TOTPSealed, a.TOTPLastStep, a.Status, a.FailedAttempts, stamp(a.LockedUntil),
		stamp(a.LastLoginAt))
	if err != nil {
		return fmt.Errorf("update admin: %w", err)
	}
	return nil
}

func (r admins) ByEmail(ctx context.Context, email string) (*domain.Admin, error) {
	return r.one(ctx, `SELECT `+adminColumns+` FROM admins WHERE lower(email) = lower($1)`, email)
}

func (r admins) ByEmailForUpdate(ctx context.Context, email string) (*domain.Admin, error) {
	return r.one(ctx, `SELECT `+adminColumns+` FROM admins WHERE lower(email) = lower($1) FOR UPDATE`, email)
}

func (r admins) Get(ctx context.Context, id string) (*domain.Admin, error) {
	return r.one(ctx, `SELECT `+adminColumns+` FROM admins WHERE id = $1`, id)
}

func (r admins) List(ctx context.Context) ([]domain.Admin, error) {
	rows, err := r.q.Query(ctx, `SELECT `+adminColumns+` FROM admins ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list admins: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Admin, error) { return scanAdmin(row) })
	if err != nil {
		return nil, fmt.Errorf("list admins: %w", err)
	}
	return out, nil
}

type sessions repos

func (r sessions) Insert(ctx context.Context, s domain.Session) error {
	_, err := r.q.Exec(ctx, `INSERT INTO admin_sessions (token_hash, admin_id, ip, user_agent, created_at, last_seen_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, s.TokenHash, s.AdminID, s.IP, s.UserAgent, s.CreatedAt, s.LastSeenAt, s.ExpiresAt)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (r sessions) Get(ctx context.Context, hash []byte) (*domain.Session, error) {
	var s domain.Session
	err := r.q.QueryRow(ctx, `SELECT token_hash, admin_id, ip, user_agent, created_at, last_seen_at, expires_at FROM admin_sessions
		WHERE token_hash = $1 AND revoked_at IS NULL`, hash).
		Scan(&s.TokenHash, &s.AdminID, &s.IP, &s.UserAgent, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	return &s, nil
}

func (r sessions) Touch(ctx context.Context, hash []byte, now time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE admin_sessions SET last_seen_at = $2 WHERE token_hash = $1`, hash, now); err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

func (r sessions) Revoke(ctx context.Context, hash []byte, now time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE admin_sessions SET revoked_at = $2 WHERE token_hash = $1 AND revoked_at IS NULL`, hash, now); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

func (r sessions) RevokeAll(ctx context.Context, adminID string, now time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE admin_sessions SET revoked_at = $2 WHERE admin_id = $1 AND revoked_at IS NULL`, adminID, now); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	return nil
}

type approvals repos

const approvalColumns = `id, kind, payload, reason, status, requested_by, decided_by, result, created_at, decided_at`

func scanApproval(row pgx.Row) (domain.Approval, error) {
	var a domain.Approval
	var payload []byte
	var decidedBy *string
	var decided *time.Time
	if err := row.Scan(&a.ID, &a.Kind, &payload, &a.Reason, &a.Status, &a.RequestedBy, &decidedBy, &a.Result, &a.CreatedAt, &decided); err != nil {
		return domain.Approval{}, err
	}
	if err := json.Unmarshal(payload, &a.Payload); err != nil {
		return domain.Approval{}, fmt.Errorf("approval %s payload: %w", a.ID, err)
	}
	if decidedBy != nil {
		a.DecidedBy = *decidedBy
	}
	a.DecidedAt = at(decided)
	return a, nil
}

func (r approvals) Insert(ctx context.Context, a domain.Approval) error {
	payload, err := json.Marshal(a.Payload)
	if err != nil {
		return err
	}
	_, err = r.q.Exec(ctx, `INSERT INTO approvals (`+approvalColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		a.ID, a.Kind, payload, a.Reason, a.Status, a.RequestedBy, nullable(a.DecidedBy), a.Result, a.CreatedAt, stamp(a.DecidedAt))
	if err != nil {
		return fmt.Errorf("insert approval: %w", err)
	}
	return nil
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (r approvals) Update(ctx context.Context, a domain.Approval) error {
	_, err := r.q.Exec(ctx, `UPDATE approvals SET status = $2, decided_by = $3, result = $4, decided_at = $5 WHERE id = $1`,
		a.ID, a.Status, nullable(a.DecidedBy), a.Result, stamp(a.DecidedAt))
	if err != nil {
		return fmt.Errorf("update approval: %w", err)
	}
	return nil
}

func (r approvals) GetForUpdate(ctx context.Context, id string) (*domain.Approval, error) {
	a, err := scanApproval(r.q.QueryRow(ctx, `SELECT `+approvalColumns+` FROM approvals WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get approval: %w", err)
	}
	return &a, nil
}

func (r approvals) List(ctx context.Context, status string, afterTime time.Time, afterID string, limit int) ([]domain.Approval, error) {
	var after *time.Time
	var afterUUID *uuid.UUID
	if afterID != "" {
		id, err := uuid.Parse(afterID)
		if err != nil {
			return nil, apperr.Invalid("bad cursor")
		}
		after, afterUUID = &afterTime, &id
	}
	rows, err := r.q.Query(ctx, `SELECT `+approvalColumns+` FROM approvals WHERE ($1 = '' OR status = $1)
		AND ($2::timestamptz IS NULL OR (created_at, id) < ($2, $3::uuid)) ORDER BY created_at DESC, id DESC LIMIT $4`,
		status, after, afterUUID, limit)
	if err != nil {
		return nil, fmt.Errorf("list approvals: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Approval, error) { return scanApproval(row) })
	if err != nil {
		return nil, fmt.Errorf("list approvals: %w", err)
	}
	return out, nil
}
