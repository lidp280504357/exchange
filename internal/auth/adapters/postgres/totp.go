package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lidp280504357/exchange/internal/auth/domain"
	"github.com/lidp280504357/exchange/internal/auth/ports"
)

func (r repos) TOTP() ports.TOTPRepo { return totps(r) }

type totps repos

func (r totps) get(ctx context.Context, userID, lock string) (*ports.SealedTOTP, error) {
	var t ports.SealedTOTP
	var activated *time.Time
	err := r.q.QueryRow(ctx, `SELECT user_id::text, secret_enc, status, last_step, created_at, activated_at
		FROM totp_credentials WHERE user_id = $1`+lock, userID).
		Scan(&t.UserID, &t.Sealed, &t.Status, &t.LastStep, &t.CreatedAt, &activated)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load totp: %w", err)
	}
	if activated != nil {
		t.ActivatedAt = *activated
	}
	return &t, nil
}

func (r totps) Get(ctx context.Context, userID string) (*ports.SealedTOTP, error) {
	return r.get(ctx, userID, "")
}

func (r totps) GetForUpdate(ctx context.Context, userID string) (*ports.SealedTOTP, error) {
	return r.get(ctx, userID, " FOR UPDATE")
}

func (r totps) Put(ctx context.Context, t ports.SealedTOTP) error {
	var activated *time.Time
	if !t.ActivatedAt.IsZero() {
		activated = &t.ActivatedAt
	}
	_, err := r.q.Exec(ctx, `INSERT INTO totp_credentials (user_id, secret_enc, status, last_step, created_at, activated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id) DO UPDATE SET secret_enc = $2, status = $3, last_step = $4, created_at = $5, activated_at = $6`,
		t.UserID, t.Sealed, t.Status, t.LastStep, t.CreatedAt, activated)
	if err != nil {
		return fmt.Errorf("save totp: %w", err)
	}
	return nil
}

func (r totps) Delete(ctx context.Context, userID string) error {
	if _, err := r.q.Exec(ctx, `DELETE FROM totp_credentials WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("delete totp: %w", err)
	}
	return nil
}

func (r repos) Security() ports.SecurityRepo { return security(r) }

type security repos

// Context reads the user's identities, authenticator, credential and the
// session's device in one query.
func (r security) Context(ctx context.Context, userID, sessionID string) (domain.SecurityContext, error) {
	var c domain.SecurityContext
	var device *string
	var firstSeen, identityChanged, passwordChanged, totpChanged *time.Time
	err := r.q.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM identities WHERE user_id = $1),
			coalesce((SELECT status = 'ACTIVE' FROM totp_credentials WHERE user_id = $1), false),
			(SELECT max(verified_at) FROM identities WHERE user_id = $1),
			(SELECT password_changed_at FROM credentials WHERE user_id = $1),
			(SELECT totp_changed_at FROM credentials WHERE user_id = $1),
			s.device_id, d.first_seen_at
		FROM (SELECT 1) one
		LEFT JOIN sessions s ON s.id = $2 AND s.user_id = $1
		LEFT JOIN known_devices d ON d.user_id = s.user_id AND d.device_id = s.device_id`, userID, sessionID).
		Scan(&c.Identities, &c.TOTPEnabled, &identityChanged, &passwordChanged, &totpChanged, &device, &firstSeen)
	if err != nil {
		return domain.SecurityContext{}, fmt.Errorf("read security context: %w", err)
	}
	if device != nil {
		c.DeviceID = *device
	}
	for dst, src := range map[*time.Time]*time.Time{
		&c.DeviceFirstSeenAt: firstSeen, &c.IdentityChangedAt: identityChanged, &c.PasswordChangedAt: passwordChanged, &c.TOTPChangedAt: totpChanged,
	} {
		if src != nil {
			*dst = *src
		}
	}
	return c, nil
}
