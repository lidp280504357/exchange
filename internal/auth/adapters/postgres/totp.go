package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

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
