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
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/outbox"
	"github.com/skill/exchange/internal/platform/pg"
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
func (r repos) Settings() ports.SettingsRepo  { return settings(r) }
func (r repos) Notes() ports.NoteRepo         { return notes(r) }
func (r repos) Tags() ports.TagRepo           { return tags(r) }
func (r repos) Changes() ports.ChangeRepo     { return changes(r) }
func (r repos) Keys() ports.IdempotencyRepo   { return keys(r) }

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
	last_login_at, created_at, setup_kind, setup_hash, setup_totp_sealed, setup_expires_at, must_change_password`

func scanAdmin(row pgx.Row) (domain.Admin, error) {
	var a domain.Admin
	var locked, last, setupExpires *time.Time
	var failed int32
	var setupKind *string
	err := row.Scan(&a.ID, &a.Email, &a.Name, &a.Role, &a.PasswordHash, &a.TOTPSealed, &a.TOTPLastStep, &a.Status, &failed, &locked,
		&last, &a.CreatedAt, &setupKind, &a.SetupHash, &a.SetupTOTPSealed, &setupExpires, &a.MustChangePassword)
	a.FailedAttempts, a.LockedUntil, a.LastLoginAt, a.SetupExpiresAt = int(failed), at(locked), at(last), at(setupExpires)
	if setupKind != nil {
		a.SetupKind = *setupKind
	}
	return a, err
}

func optKind(kind string) *string {
	if kind == "" {
		return nil
	}
	return &kind
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
	_, err := r.q.Exec(ctx, `INSERT INTO admins (`+adminColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
		$15, $16, $17)`,
		a.ID, a.Email, a.Name, a.Role, a.PasswordHash, a.TOTPSealed, a.TOTPLastStep, a.Status, a.FailedAttempts,
		stamp(a.LockedUntil), stamp(a.LastLoginAt), a.CreatedAt, optKind(a.SetupKind), a.SetupHash, a.SetupTOTPSealed,
		stamp(a.SetupExpiresAt), a.MustChangePassword)
	if err != nil {
		return fmt.Errorf("insert admin: %w", err)
	}
	return nil
}

func (r admins) Update(ctx context.Context, a domain.Admin) error {
	_, err := r.q.Exec(ctx, `UPDATE admins SET name = $2, role = $3, password_hash = $4, totp_sealed = $5, totp_last_step = $6,
		status = $7, failed_attempts = $8, locked_until = $9, last_login_at = $10, setup_kind = $11, setup_hash = $12,
		setup_totp_sealed = $13, setup_expires_at = $14, must_change_password = $15, updated_at = now() WHERE id = $1`,
		a.ID, a.Name, a.Role, a.PasswordHash, a.TOTPSealed, a.TOTPLastStep, a.Status, a.FailedAttempts, stamp(a.LockedUntil),
		stamp(a.LastLoginAt), optKind(a.SetupKind), a.SetupHash, a.SetupTOTPSealed, stamp(a.SetupExpiresAt), a.MustChangePassword)
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

func (r admins) GetForUpdate(ctx context.Context, id string) (*domain.Admin, error) {
	return r.one(ctx, `SELECT `+adminColumns+` FROM admins WHERE id = $1 FOR UPDATE`, id)
}

func (r admins) BySetupForUpdate(ctx context.Context, hash []byte) (*domain.Admin, error) {
	return r.one(ctx, `SELECT `+adminColumns+` FROM admins WHERE setup_hash = $1 FOR UPDATE`, hash)
}

// lockRoster serializes the changes that could leave no active ADMIN
// (C5.5 ⑪): a transaction-scoped advisory lock.
const lockRoster = 7_331_001

func (r admins) LockRoster(ctx context.Context) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(lockRoster)); err != nil {
		return fmt.Errorf("lock the administrators: %w", err)
	}
	return nil
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

func (r sessions) RevokeOthers(ctx context.Context, adminID string, keep []byte, now time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE admin_sessions SET revoked_at = $3 WHERE admin_id = $1 AND token_hash <> $2 AND revoked_at IS NULL`,
		adminID, keep, now); err != nil {
		return fmt.Errorf("revoke other sessions: %w", err)
	}
	return nil
}

func (r sessions) Live(ctx context.Context, adminID string, now time.Time) ([]domain.Session, error) {
	rows, err := r.q.Query(ctx, `SELECT token_hash, admin_id, ip, user_agent, created_at, last_seen_at, expires_at FROM admin_sessions
		WHERE admin_id = $1 AND revoked_at IS NULL AND expires_at > $2 AND last_seen_at > $3 ORDER BY last_seen_at DESC LIMIT 50`,
		adminID, now, now.Add(-domain.SessionIdle))
	if err != nil {
		return nil, fmt.Errorf("live sessions: %w", err)
	}
	defer rows.Close()
	out := []domain.Session{}
	for rows.Next() {
		var s domain.Session
		if err := rows.Scan(&s.TokenHash, &s.AdminID, &s.IP, &s.UserAgent, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt); err != nil {
			return nil, fmt.Errorf("live sessions: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

type approvals repos

const approvalColumns = `id, kind, payload, reason, status, requested_by, decided_by, result, created_at, decided_at, mode,
	value_usdt, escalation, journal_id, attempted_at`

// approvalSelect reads approvals with their administrators' emails.
const approvalSelect = `SELECT a.id, a.kind, a.payload, a.reason, a.status, a.requested_by, a.decided_by, a.result, a.created_at,
	a.decided_at, a.mode, a.value_usdt, a.escalation, a.journal_id, a.attempted_at, coalesce(r.email, ''), coalesce(d.email, '')
	FROM approvals a LEFT JOIN admins r ON r.id = a.requested_by LEFT JOIN admins d ON d.id = a.decided_by`

func scanApproval(row pgx.Row, emails bool) (domain.Approval, error) {
	var a domain.Approval
	var payload []byte
	var decidedBy *string
	var decided, attempted *time.Time
	var value decimal.NullDecimal
	dest := []any{
		&a.ID, &a.Kind, &payload, &a.Reason, &a.Status, &a.RequestedBy, &decidedBy, &a.Result, &a.CreatedAt, &decided, &a.Mode, &value,
		&a.Escalation, &a.JournalID, &attempted,
	}
	if emails {
		dest = append(dest, &a.RequestedByEmail, &a.DecidedByEmail)
	}
	if err := row.Scan(dest...); err != nil {
		return domain.Approval{}, err
	}
	if err := json.Unmarshal(payload, &a.Payload); err != nil {
		return domain.Approval{}, fmt.Errorf("approval %s payload: %w", a.ID, err)
	}
	if decidedBy != nil {
		a.DecidedBy = *decidedBy
	}
	if value.Valid {
		a.ValueUSDT = &value.Decimal
	}
	a.DecidedAt, a.AttemptedAt = at(decided), at(attempted)
	return a, nil
}

func (r approvals) Insert(ctx context.Context, a domain.Approval) error {
	payload, err := json.Marshal(a.Payload)
	if err != nil {
		return err
	}
	if a.Mode == "" {
		a.Mode = domain.ModeTwoPerson
	}
	var value decimal.NullDecimal
	if a.ValueUSDT != nil {
		value = decimal.NullDecimal{Decimal: *a.ValueUSDT, Valid: true}
	}
	_, err = r.q.Exec(ctx, `INSERT INTO approvals (`+approvalColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		a.ID, a.Kind, payload, a.Reason, a.Status, a.RequestedBy, nullable(a.DecidedBy), a.Result, a.CreatedAt, stamp(a.DecidedAt),
		a.Mode, value, a.Escalation, a.JournalID, stamp(a.AttemptedAt))
	if c, dup := pg.UniqueViolation(err); dup && c == "approvals_deposit_assign_live" {
		return domain.ErrDepositAssignOpen
	}
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
	_, err := r.q.Exec(ctx, `UPDATE approvals SET status = $2, decided_by = $3, result = $4, decided_at = $5, journal_id = $6
		WHERE id = $1`, a.ID, a.Status, nullable(a.DecidedBy), a.Result, stamp(a.DecidedAt), a.JournalID)
	if err != nil {
		return fmt.Errorf("update approval: %w", err)
	}
	return nil
}

func (r approvals) MarkAttempted(ctx context.Context, id string, at time.Time, note string) error {
	_, err := r.q.Exec(ctx, `UPDATE approvals SET attempted_at = coalesce(attempted_at, $2),
		result = CASE WHEN $3 = '' THEN result ELSE $3 END WHERE id = $1 AND status = 'PENDING'`, id, at, note)
	if err != nil {
		return fmt.Errorf("mark approval attempted: %w", err)
	}
	return nil
}

// keys keeps the Idempotency-Keys in the platform's idempotency_keys
// table: the ID of what a request made is its response.
type keys repos

func (r keys) Claim(ctx context.Context, scope, key string, hash []byte, ref string, now time.Time) ([]byte, string, bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO idempotency_keys (scope, key, request_hash, response, created_at)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING`, scope, key, hash, []byte(ref), now)
	if err != nil {
		return nil, "", false, fmt.Errorf("claim idempotency key: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return hash, ref, true, nil
	}
	var stored, first []byte
	if err := r.q.QueryRow(ctx, `SELECT request_hash, coalesce(response, '') FROM idempotency_keys WHERE scope = $1 AND key = $2`, scope, key).
		Scan(&stored, &first); err != nil {
		return nil, "", false, fmt.Errorf("read idempotency key: %w", err)
	}
	return stored, string(first), false, nil
}

func (r keys) Purge(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := r.q.Exec(ctx, `DELETE FROM idempotency_keys WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("purge idempotency keys: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (r approvals) one(ctx context.Context, emails bool, sql string, args ...any) (*domain.Approval, error) {
	a, err := scanApproval(r.q.QueryRow(ctx, sql, args...), emails)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get approval: %w", err)
	}
	return &a, nil
}

func (r approvals) GetForUpdate(ctx context.Context, id string) (*domain.Approval, error) {
	return r.one(ctx, false, `SELECT `+approvalColumns+` FROM approvals WHERE id = $1 FOR UPDATE`, id)
}

func (r approvals) Get(ctx context.Context, id string) (*domain.Approval, error) {
	return r.one(ctx, true, approvalSelect+` WHERE a.id = $1`, id)
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
	rows, err := r.q.Query(ctx, approvalSelect+` WHERE ($1 = '' OR a.status = $1)
		AND ($2::timestamptz IS NULL OR (a.created_at, a.id) < ($2, $3::uuid)) ORDER BY a.created_at DESC, a.id DESC LIMIT $4`,
		status, after, afterUUID, limit)
	if err != nil {
		return nil, fmt.Errorf("list approvals: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Approval, error) { return scanApproval(row, true) })
	if err != nil {
		return nil, fmt.Errorf("list approvals: %w", err)
	}
	return out, nil
}

func (r approvals) SingleUsage(ctx context.Context, adminID string, since time.Time) (decimal.Decimal, error) {
	var sum decimal.Decimal
	err := r.q.QueryRow(ctx, `SELECT coalesce(sum(abs(value_usdt)), 0) FROM approvals
		WHERE requested_by = $1 AND mode = 'SINGLE' AND created_at >= $2 AND status IN ('PENDING', 'EXECUTED')`, adminID, since).Scan(&sum)
	if err != nil {
		return decimal.Zero, fmt.Errorf("single-person usage: %w", err)
	}
	return sum, nil
}

// lockRequests is the advisory lock class of LockRequests (the two-key
// form, apart from lockRoster's single key); the second key is a hash of
// the kind and the requester.
const lockRequests = 7_331_002

func (r approvals) LockRequests(ctx context.Context, kind, requestedBy string) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock($1::int, hashtext($2))`, lockRequests, kind+":"+requestedBy); err != nil {
		return fmt.Errorf("lock the requests: %w", err)
	}
	return nil
}

func (r approvals) PendingOf(ctx context.Context, kind, requestedBy string) ([]domain.Approval, error) {
	rows, err := r.q.Query(ctx, approvalSelect+` WHERE a.status = 'PENDING' AND a.kind = $1 AND a.requested_by = $2 ORDER BY a.created_at, a.id`,
		kind, requestedBy)
	if err != nil {
		return nil, fmt.Errorf("pending requests: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Approval, error) { return scanApproval(row, true) })
	if err != nil {
		return nil, fmt.Errorf("pending requests: %w", err)
	}
	return out, nil
}

func (r approvals) PendingOfKind(ctx context.Context, kind string) ([]domain.Approval, error) {
	rows, err := r.q.Query(ctx, approvalSelect+` WHERE a.status = 'PENDING' AND a.kind = $1 ORDER BY a.created_at, a.id`, kind)
	if err != nil {
		return nil, fmt.Errorf("pending requests of a kind: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Approval, error) { return scanApproval(row, true) })
	if err != nil {
		return nil, fmt.Errorf("pending requests of a kind: %w", err)
	}
	return out, nil
}

func (r approvals) CountPending(ctx context.Context) (int, error) {
	var n int
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM approvals WHERE status = 'PENDING'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count approvals: %w", err)
	}
	return n, nil
}

type settings repos

func (r settings) Get(ctx context.Context) (*domain.Settings, error) {
	var s domain.Settings
	var delay int64
	err := r.q.QueryRow(ctx, `SELECT single_max_usdt, daily_max_usdt, withdrawal_max_usdt, change_delay_seconds, updated_by, updated_at
		FROM settings`).Scan(&s.SingleMax, &s.DailyMax, &s.WithdrawalMax, &delay, &s.UpdatedBy, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get settings: %w", err)
	}
	s.ChangeDelay = time.Duration(delay) * time.Second
	return &s, nil
}

func (r settings) Put(ctx context.Context, s domain.Settings) error {
	_, err := r.q.Exec(ctx, `INSERT INTO settings (single_max_usdt, daily_max_usdt, withdrawal_max_usdt, change_delay_seconds, updated_by,
		updated_at) VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (id) DO UPDATE SET single_max_usdt = $1, daily_max_usdt = $2,
		withdrawal_max_usdt = $3, change_delay_seconds = $4, updated_by = $5, updated_at = $6`,
		s.SingleMax, s.DailyMax, s.WithdrawalMax, int64(s.ChangeDelay/time.Second), s.UpdatedBy, s.UpdatedAt)
	if err != nil {
		return fmt.Errorf("put settings: %w", err)
	}
	return nil
}

type changes repos

const changeColumns = `c.id, c.kind, c.target, c.payload, c.summary, c.reason, c.status, c.requested_by, coalesce(rq.email, ''),
	c.approved_by, coalesce(ap.email, ''), c.approved_at, c.closed_by, coalesce(cl.email, ''), c.closed_at, c.effective_at,
	c.applied_at, c.result, c.created_at, c.applying_at, coalesce(c.confirmation_hash, '')`

const changeFrom = ` FROM instrument_changes c LEFT JOIN admins rq ON rq.id = c.requested_by LEFT JOIN admins ap ON ap.id = c.approved_by
	LEFT JOIN admins cl ON cl.id = c.closed_by`

func scanChange(row pgx.Row) (domain.InstrumentChange, error) {
	var c domain.InstrumentChange
	var approvedBy, closedBy *uuid.UUID
	var approvedAt, closedAt, effectiveAt, appliedAt, applyingAt *time.Time
	err := row.Scan(&c.ID, &c.Kind, &c.Target, &c.Payload, &c.Summary, &c.Reason, &c.Status, &c.RequestedBy, &c.RequestedByEmail,
		&approvedBy, &c.ApprovedByEmail, &approvedAt, &closedBy, &c.ClosedByEmail, &closedAt, &effectiveAt, &appliedAt, &c.Result,
		&c.CreatedAt, &applyingAt, &c.ConfirmationHash)
	if approvedBy != nil {
		c.ApprovedBy = approvedBy.String()
	}
	if closedBy != nil {
		c.ClosedBy = closedBy.String()
	}
	c.ApprovedAt, c.ClosedAt, c.EffectiveAt, c.AppliedAt, c.ApplyingAt = at(approvedAt), at(closedAt), at(effectiveAt), at(appliedAt),
		at(applyingAt)
	return c, err
}

func optUUID(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}

func (r changes) Create(ctx context.Context, c domain.InstrumentChange) error {
	var confirmation *string
	if c.ConfirmationHash != "" {
		confirmation = &c.ConfirmationHash
	}
	_, err := r.q.Exec(ctx, `INSERT INTO instrument_changes (id, kind, target, payload, summary, reason, status, requested_by,
		effective_at, created_at, confirmation_hash) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		c.ID, c.Kind, c.Target, c.Payload, c.Summary, c.Reason, c.Status, c.RequestedBy, stamp(c.EffectiveAt), c.CreatedAt, confirmation)
	if err != nil {
		return fmt.Errorf("create instrument change: %w", err)
	}
	return nil
}

func (r changes) ByConfirmation(ctx context.Context, hash string) (*domain.InstrumentChange, error) {
	c, err := scanChange(r.q.QueryRow(ctx, `SELECT `+changeColumns+changeFrom+` WHERE c.confirmation_hash = $1`, hash))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("instrument change by confirmation: %w", err)
	}
	return &c, nil
}

func (r changes) get(ctx context.Context, id, lock string) (*domain.InstrumentChange, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, nil
	}
	c, err := scanChange(r.q.QueryRow(ctx, `SELECT `+changeColumns+changeFrom+` WHERE c.id = $1`+lock, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get instrument change: %w", err)
	}
	return &c, nil
}

func (r changes) Get(ctx context.Context, id string) (*domain.InstrumentChange, error) {
	return r.get(ctx, id, "")
}

func (r changes) GetForUpdate(ctx context.Context, id string) (*domain.InstrumentChange, error) {
	return r.get(ctx, id, " FOR UPDATE OF c")
}

func (r changes) Update(ctx context.Context, c domain.InstrumentChange) error {
	_, err := r.q.Exec(ctx, `UPDATE instrument_changes SET status = $2, approved_by = $3, approved_at = $4, closed_by = $5,
		closed_at = $6, effective_at = $7, applied_at = $8, result = $9, applying_at = $10 WHERE id = $1`,
		c.ID, c.Status, optUUID(c.ApprovedBy), stamp(c.ApprovedAt), optUUID(c.ClosedBy), stamp(c.ClosedAt), stamp(c.EffectiveAt),
		stamp(c.AppliedAt), c.Result, stamp(c.ApplyingAt))
	if err != nil {
		return fmt.Errorf("update instrument change: %w", err)
	}
	return nil
}

func (r changes) List(ctx context.Context, status string, afterTime time.Time, afterID string, limit int) ([]domain.InstrumentChange, error) {
	var after *time.Time
	var afterUUID *uuid.UUID
	if afterID != "" {
		id, err := uuid.Parse(afterID)
		if err != nil {
			return nil, apperr.Invalid("bad cursor")
		}
		after, afterUUID = &afterTime, &id
	}
	rows, err := r.q.Query(ctx, `SELECT `+changeColumns+changeFrom+`
		WHERE ($1 = '' OR c.status = $1) AND ($2::timestamptz IS NULL OR (c.created_at, c.id) < ($2, $3::uuid))
		ORDER BY c.created_at DESC, c.id DESC LIMIT $4`, status, after, afterUUID, limit)
	if err != nil {
		return nil, fmt.Errorf("list instrument changes: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.InstrumentChange, error) { return scanChange(row) })
	if err != nil {
		return nil, fmt.Errorf("list instrument changes: %w", err)
	}
	return out, nil
}

func (r changes) Due(ctx context.Context, now time.Time, limit int) ([]domain.InstrumentChange, error) {
	rows, err := r.q.Query(ctx, `SELECT `+changeColumns+changeFrom+`
		WHERE c.status = 'SCHEDULED' AND c.effective_at <= $1 AND (c.applying_at IS NULL OR c.applying_at <= $1 - $3 * interval '1 second')
		ORDER BY c.effective_at, c.id LIMIT $2 FOR UPDATE OF c SKIP LOCKED`, now, limit, int64(domain.ClaimHold/time.Second))
	if err != nil {
		return nil, fmt.Errorf("due instrument changes: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.InstrumentChange, error) { return scanChange(row) })
	if err != nil {
		return nil, fmt.Errorf("due instrument changes: %w", err)
	}
	return out, nil
}

func (r changes) Open(ctx context.Context) (int, error) {
	var n int
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM instrument_changes WHERE status IN ('PENDING_APPROVAL', 'SCHEDULED')`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count instrument changes: %w", err)
	}
	return n, nil
}

type notes repos

func (r notes) Insert(ctx context.Context, n domain.Note) error {
	_, err := r.q.Exec(ctx, `INSERT INTO user_notes (id, user_id, admin_id, body, created_at) VALUES ($1, $2, $3, $4, $5)`,
		n.ID, n.UserID, n.AdminID, n.Body, n.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert note: %w", err)
	}
	return nil
}

func (r notes) List(ctx context.Context, userID string, afterTime time.Time, afterID string, limit int) ([]domain.Note, error) {
	var after *time.Time
	var afterUUID *uuid.UUID
	if afterID != "" {
		id, err := uuid.Parse(afterID)
		if err != nil {
			return nil, apperr.Invalid("bad cursor")
		}
		after, afterUUID = &afterTime, &id
	}
	rows, err := r.q.Query(ctx, `SELECT n.id, n.user_id, n.admin_id, coalesce(a.email, ''), n.body, n.created_at
		FROM user_notes n LEFT JOIN admins a ON a.id = n.admin_id
		WHERE n.user_id = $1 AND ($2::timestamptz IS NULL OR (n.created_at, n.id) < ($2, $3::uuid))
		ORDER BY n.created_at DESC, n.id DESC LIMIT $4`, userID, after, afterUUID, limit)
	if err != nil {
		return nil, fmt.Errorf("list notes: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Note, error) {
		var n domain.Note
		err := row.Scan(&n.ID, &n.UserID, &n.AdminID, &n.AdminEmail, &n.Body, &n.CreatedAt)
		return n, err
	})
	if err != nil {
		return nil, fmt.Errorf("list notes: %w", err)
	}
	return out, nil
}

type tags repos

func (r tags) Of(ctx context.Context, userIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := r.q.Query(ctx, `SELECT user_id, tag FROM user_tags WHERE user_id = ANY($1::uuid[]) ORDER BY user_id, tag`, userIDs)
	if err != nil {
		return nil, fmt.Errorf("tags: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var user, tag string
		if err := rows.Scan(&user, &tag); err != nil {
			return nil, fmt.Errorf("tags: %w", err)
		}
		out[user] = append(out[user], tag)
	}
	return out, rows.Err()
}

func (r tags) Users(ctx context.Context, tag string, limit int) ([]string, error) {
	rows, err := r.q.Query(ctx, `SELECT user_id FROM user_tags WHERE tag = $1 ORDER BY user_id LIMIT $2`, tag, limit)
	if err != nil {
		return nil, fmt.Errorf("tagged users: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("tagged users: %w", err)
	}
	return out, nil
}

func (r tags) Set(ctx context.Context, userID string, tags []string, adminID string, now time.Time) error {
	if tags == nil {
		tags = []string{} // NULL would match nothing and keep every tag
	}
	if _, err := r.q.Exec(ctx, `DELETE FROM user_tags WHERE user_id = $1 AND NOT (tag = ANY($2::text[]))`, userID, tags); err != nil {
		return fmt.Errorf("set tags: %w", err)
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO user_tags (user_id, tag, added_by, added_at)
		SELECT $1, t, $3, $4 FROM unnest($2::text[]) AS t ON CONFLICT DO NOTHING`, userID, tags, adminID, now); err != nil {
		return fmt.Errorf("set tags: %w", err)
	}
	return nil
}
