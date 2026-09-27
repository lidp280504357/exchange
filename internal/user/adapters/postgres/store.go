// Package postgres stores user-service's data in the users schema.
package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/user/domain"
)

// Store implements ports.Users.
type Store struct{ db *pg.DB }

// NewStore returns the store.
func NewStore(db *pg.DB) *Store { return &Store{db: db} }

const userColumns = `id, status, region, language, timezone, anti_phishing_code, kyc_level, version, created_at, updated_at`

func scanUser(row pgx.Row) (domain.User, error) {
	var u domain.User
	var id uuid.UUID
	err := row.Scan(&id, &u.Status, &u.Region, &u.Language, &u.Timezone, &u.AntiPhishingCode, &u.KYCLevel, &u.Version, &u.CreatedAt, &u.UpdatedAt)
	u.ID = id.String()
	return u, err
}

// Create inserts the profile and consents idempotently.
func (s *Store) Create(ctx context.Context, u domain.User, consents []domain.Consent) (domain.User, error) {
	var out domain.User
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO users (id, status, region, language, timezone) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (id) DO NOTHING`, u.ID, u.Status, u.Region, u.Language, u.Timezone); err != nil {
			return fmt.Errorf("insert user: %w", err)
		}
		for _, c := range consents {
			if _, err := tx.Exec(ctx, `INSERT INTO consents (user_id, document, version) VALUES ($1, $2, $3)
				ON CONFLICT DO NOTHING`, u.ID, c.Document, c.Version); err != nil {
				return fmt.Errorf("insert consent: %w", err)
			}
		}
		var err error
		out, err = scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, u.ID))
		if err != nil {
			return fmt.Errorf("read user: %w", err)
		}
		return nil
	})
	return out, err
}

// Get reads a profile.
func (s *Store) Get(ctx context.Context, id string) (domain.User, error) {
	if _, err := uuid.Parse(id); err != nil {
		return domain.User{}, domain.ErrUserNotFound
	}
	u, err := scanUser(s.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
	if pg.IsNoRows(err) {
		return domain.User{}, domain.ErrUserNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}
