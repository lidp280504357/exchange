package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/lidp280504357/exchange/internal/auth/domain"
	"github.com/lidp280504357/exchange/internal/auth/ports"
	"github.com/lidp280504357/exchange/internal/platform/pg"
)

func (r repos) Credentials() ports.CredentialRepo         { return credentials(r) }
func (r repos) Sessions() ports.SessionRepo               { return sessions(r) }
func (r repos) LoginChallenges() ports.LoginChallengeRepo { return loginChallenges(r) }
func (r repos) StepUps() ports.StepUpRepo                 { return stepUps(r) }
func (r repos) Devices() ports.DeviceRepo                 { return devices(r) }
func (r repos) History() ports.HistoryRepo                { return history(r) }
func (r repos) RebindRequests() ports.RebindRepo          { return rebinds(r) }

func (r identities) Create(ctx context.Context, id domain.Identity, verifiedAt time.Time) error {
	_, err := r.q.Exec(ctx, `INSERT INTO identities (id, user_id, kind, value, verified_at) VALUES ($1, $2, $3, $4, $5)`,
		id.ID, id.UserID, id.Kind, id.Value, verifiedAt)
	if c, ok := pg.UniqueViolation(err); ok {
		if c == "identities_user_kind_key" {
			return domain.ErrIdentityKindBound
		}
		return domain.ErrIdentityTaken
	}
	if err != nil {
		return fmt.Errorf("create identity: %w", err)
	}
	return nil
}

func (r identities) UpdateValue(ctx context.Context, id, value string, verifiedAt time.Time) error {
	_, err := r.q.Exec(ctx, `UPDATE identities SET value = $2, verified_at = $3 WHERE id = $1`, id, value, verifiedAt)
	if _, ok := pg.UniqueViolation(err); ok {
		return domain.ErrIdentityTaken
	}
	if err != nil {
		return fmt.Errorf("update identity: %w", err)
	}
	return nil
}

type credentials repos

func (r credentials) Create(ctx context.Context, userID, hash string, now time.Time) error {
	_, err := r.q.Exec(ctx, `INSERT INTO credentials (user_id, password_hash, password_changed_at, updated_at)
		VALUES ($1, $2, $3, $3)`, userID, hash, now)
	if err != nil {
		return fmt.Errorf("create credential: %w", err)
	}
	return nil
}

func (r credentials) Get(ctx context.Context, userID string) (*domain.Credential, error) {
	var c domain.Credential
	var locked, last *time.Time
	err := r.q.QueryRow(ctx, `SELECT user_id::text, password_hash, failed_attempts, locked_until, last_login_at
		FROM credentials WHERE user_id = $1`, userID).Scan(&c.UserID, &c.PasswordHash, &c.FailedAttempts, &locked, &last)
	if pg.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load credential: %w", err)
	}
	if locked != nil {
		c.LockedUntil = *locked
	}
	if last != nil {
		c.LastLoginAt = *last
	}
	return &c, nil
}

func (r credentials) RecordLogin(ctx context.Context, userID string, now time.Time) error {
	_, err := r.q.Exec(ctx, `UPDATE credentials SET last_login_at = $2, failed_attempts = 0, locked_until = NULL,
		updated_at = $2 WHERE user_id = $1`, userID, now)
	if err != nil {
		return fmt.Errorf("record login: %w", err)
	}
	return nil
}

func (r credentials) RecordFailure(ctx context.Context, userID string, lockedUntil time.Time) error {
	var until any
	if !lockedUntil.IsZero() {
		until = lockedUntil
	}
	_, err := r.q.Exec(ctx, `UPDATE credentials SET failed_attempts = failed_attempts + 1,
		locked_until = COALESCE($2, locked_until), updated_at = now() WHERE user_id = $1`, userID, until)
	if err != nil {
		return fmt.Errorf("record failure: %w", err)
	}
	return nil
}

func (r credentials) SetPassword(ctx context.Context, userID, hash string, now time.Time) error {
	_, err := r.q.Exec(ctx, `UPDATE credentials SET password_hash = $2, password_changed_at = $3, failed_attempts = 0,
		locked_until = NULL, updated_at = $3 WHERE user_id = $1`, userID, hash, now)
	if err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	return nil
}

type sessions repos

const sessionColumns = `id::text, user_id::text, device_id, client_type, user_agent, ip, created_at, last_seen_at, revoked_at`

func scanSession(row interface{ Scan(...any) error }) (domain.Session, error) {
	var s domain.Session
	var revoked *time.Time
	err := row.Scan(&s.ID, &s.UserID, &s.DeviceID, &s.ClientType, &s.UserAgent, &s.IP, &s.CreatedAt, &s.LastSeenAt, &revoked)
	if revoked != nil {
		s.RevokedAt = *revoked
	}
	return s, err
}

func (r sessions) Create(ctx context.Context, s domain.Session) error {
	_, err := r.q.Exec(ctx, `INSERT INTO sessions (id, user_id, device_id, client_type, user_agent, ip, created_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`, s.ID, s.UserID, s.DeviceID, s.ClientType, s.UserAgent, s.IP, s.CreatedAt)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (r sessions) Get(ctx context.Context, id string) (*domain.Session, error) {
	s, err := scanSession(r.q.QueryRow(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE id = $1`, id))
	if pg.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}
	return &s, nil
}

func (r sessions) Active(ctx context.Context, userID string) ([]domain.Session, error) {
	rows, err := r.q.Query(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE user_id = $1 AND revoked_at IS NULL
		ORDER BY last_seen_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	var out []domain.Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("list sessions: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r sessions) Touch(ctx context.Context, id, ip, userAgent string, now time.Time) error {
	_, err := r.q.Exec(ctx, `UPDATE sessions SET last_seen_at = $2, ip = $3, user_agent = $4 WHERE id = $1`, id, now, ip, userAgent)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

func (r sessions) Revoke(ctx context.Context, id, reason string, now time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE sessions SET revoked_at = $2, revoke_reason = $3 WHERE id = $1 AND revoked_at IS NULL`,
		id, now, reason)
	if err != nil {
		return false, fmt.Errorf("revoke session: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r sessions) CreateRefresh(ctx context.Context, t domain.RefreshToken) error {
	_, err := r.q.Exec(ctx, `INSERT INTO refresh_tokens (token_hash, session_id, generation, expires_at) VALUES ($1, $2, $3, $4)`,
		t.Hash, t.SessionID, t.Generation, t.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create refresh token: %w", err)
	}
	return nil
}

func (r sessions) RefreshForUpdate(ctx context.Context, hash []byte) (*domain.RefreshToken, error) {
	var t domain.RefreshToken
	var rotated *time.Time
	err := r.q.QueryRow(ctx, `SELECT token_hash, session_id::text, generation, expires_at, rotated_at
		FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE`, hash).Scan(&t.Hash, &t.SessionID, &t.Generation, &t.ExpiresAt, &rotated)
	if pg.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load refresh token: %w", err)
	}
	if rotated != nil {
		t.RotatedAt = *rotated
	}
	return &t, nil
}

func (r sessions) MarkRotated(ctx context.Context, hash []byte, now time.Time) error {
	_, err := r.q.Exec(ctx, `UPDATE refresh_tokens SET rotated_at = $2 WHERE token_hash = $1`, hash, now)
	if err != nil {
		return fmt.Errorf("rotate refresh token: %w", err)
	}
	return nil
}

type loginChallenges repos

func (r loginChallenges) Create(ctx context.Context, lc domain.LoginChallenge) error {
	_, err := r.q.Exec(ctx, `INSERT INTO login_challenges (id, user_id, device_id, expires_at) VALUES ($1, $2, $3, $4)`,
		lc.ID, lc.UserID, lc.DeviceID, lc.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create login challenge: %w", err)
	}
	return nil
}

func (r loginChallenges) Get(ctx context.Context, id string) (*domain.LoginChallenge, error) {
	var lc domain.LoginChallenge
	var consumed *time.Time
	err := r.q.QueryRow(ctx, `SELECT id::text, user_id::text, device_id, expires_at, consumed_at FROM login_challenges
		WHERE id = $1`, id).Scan(&lc.ID, &lc.UserID, &lc.DeviceID, &lc.ExpiresAt, &consumed)
	if pg.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load login challenge: %w", err)
	}
	if consumed != nil {
		lc.ConsumedAt = *consumed
	}
	return &lc, nil
}

func (r loginChallenges) Consume(ctx context.Context, id string, now time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE login_challenges SET consumed_at = $2 WHERE id = $1 AND consumed_at IS NULL AND expires_at > $2`,
		id, now)
	if err != nil {
		return false, fmt.Errorf("consume login challenge: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

type stepUps repos

func (r stepUps) Create(ctx context.Context, s domain.StepUp) error {
	_, err := r.q.Exec(ctx, `INSERT INTO step_up_tokens (token_hash, user_id, session_id, channel, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, s.Hash, s.UserID, s.SessionID, string(s.Channel), s.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create step-up: %w", err)
	}
	return nil
}

func (r stepUps) Consume(ctx context.Context, hash []byte, userID string, now time.Time) (*domain.StepUp, error) {
	var s domain.StepUp
	var channel string
	err := r.q.QueryRow(ctx, `UPDATE step_up_tokens SET consumed_at = $3
		WHERE token_hash = $1 AND user_id = $2 AND consumed_at IS NULL AND expires_at > $3
		RETURNING token_hash, user_id::text, session_id::text, channel, expires_at`, hash, userID, now).
		Scan(&s.Hash, &s.UserID, &s.SessionID, &channel, &s.ExpiresAt)
	if pg.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("consume step-up: %w", err)
	}
	s.Channel = domain.Channel(channel)
	return &s, nil
}

type devices repos

func (r devices) Seen(ctx context.Context, userID, deviceID string, now time.Time) (bool, error) {
	var inserted bool
	err := r.q.QueryRow(ctx, `INSERT INTO known_devices (user_id, device_id, first_seen_at, last_seen_at) VALUES ($1, $2, $3, $3)
		ON CONFLICT (user_id, device_id) DO UPDATE SET last_seen_at = EXCLUDED.last_seen_at
		RETURNING (xmax = 0)`, userID, deviceID, now).Scan(&inserted)
	if err != nil {
		return false, fmt.Errorf("record device: %w", err)
	}
	return inserted, nil
}

type history repos

func (r history) Add(ctx context.Context, e domain.LoginEvent) error {
	_, err := r.q.Exec(ctx, `INSERT INTO login_history (user_id, method, result, identity_mask, device_id, user_agent, ip, new_device, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		nullUUID(e.UserID), e.Method, e.Result, e.IdentityMask, e.DeviceID, e.UserAgent, e.IP, e.NewDevice, e.CreatedAt)
	if err != nil {
		return fmt.Errorf("add login history: %w", err)
	}
	return nil
}

func (r history) List(ctx context.Context, userID string, beforeID int64, limit int) ([]domain.LoginEvent, error) {
	if beforeID <= 0 {
		beforeID = 1<<63 - 1
	}
	rows, err := r.q.Query(ctx, `SELECT id, method, result, identity_mask, device_id, user_agent, ip, new_device, created_at
		FROM login_history WHERE user_id = $1 AND id < $2 ORDER BY id DESC LIMIT $3`, userID, beforeID, limit)
	if err != nil {
		return nil, fmt.Errorf("list login history: %w", err)
	}
	defer rows.Close()
	var out []domain.LoginEvent
	for rows.Next() {
		e := domain.LoginEvent{UserID: userID}
		if err := rows.Scan(&e.ID, &e.Method, &e.Result, &e.IdentityMask, &e.DeviceID, &e.UserAgent, &e.IP, &e.NewDevice, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("list login history: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type rebinds repos

func (r rebinds) Create(ctx context.Context, rr domain.RebindRequest) error {
	_, err := r.q.Exec(ctx, `INSERT INTO identity_rebind_requests (id, user_id, kind, new_value) VALUES ($1, $2, $3, $4)`,
		rr.ID, rr.UserID, rr.Kind, rr.NewValue)
	if err != nil {
		return fmt.Errorf("create rebind request: %w", err)
	}
	return nil
}
