package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/platform/pg"
)

// Uploads implements ports.AppUploads on app_uploads (admin 00017).
type Uploads struct{ db *pg.DB }

// NewUploads returns the uploads on db.
func NewUploads(db *pg.DB) *Uploads { return &Uploads{db: db} }

const uploadColumns = `upload_id::text, platform, kind, name, size, sha256, part_size, received, started_by, started_at, expires_at`

func scanUpload(row pgx.Row) (*domain.AppUpload, error) {
	var u domain.AppUpload
	var received []int32
	err := row.Scan(&u.ID, &u.Platform, &u.Kind, &u.Name, &u.Size, &u.SHA256, &u.PartSize, &received, &u.StartedBy, &u.StartedAt, &u.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read an app upload: %w", err)
	}
	u.Received = make([]int, 0, len(received))
	for _, n := range received {
		u.Received = append(u.Received, int(n))
	}
	return &u, nil
}

// Create inserts an upload with no part.
func (s *Uploads) Create(ctx context.Context, u domain.AppUpload) error {
	_, err := s.db.Exec(ctx, `INSERT INTO app_uploads (upload_id, platform, kind, name, size, sha256, part_size, started_by, started_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		u.ID, u.Platform, u.Kind, u.Name, u.Size, u.SHA256, u.PartSize, u.StartedBy, u.StartedAt, u.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create an app upload: %w", err)
	}
	return nil
}

// Get returns an upload; nil when unknown.
func (s *Uploads) Get(ctx context.Context, id string) (*domain.AppUpload, error) {
	return scanUpload(s.db.QueryRow(ctx, `SELECT `+uploadColumns+` FROM app_uploads WHERE upload_id = $1`, id))
}

// Received adds part n to the parts an upload has, once, in order.
func (s *Uploads) Received(ctx context.Context, id string, n int) (*domain.AppUpload, error) {
	return scanUpload(s.db.QueryRow(ctx, `UPDATE app_uploads SET received = ARRAY(SELECT DISTINCT p FROM unnest(received || $2::integer) p ORDER BY p)
		WHERE upload_id = $1 RETURNING `+uploadColumns, id, n))
}

// Claim holds an upload for its completion until until, unless another
// completion holds it at now.
func (s *Uploads) Claim(ctx context.Context, id string, now, until time.Time) (bool, error) {
	tag, err := s.db.Exec(ctx, `UPDATE app_uploads SET completing_until = $3
		WHERE upload_id = $1 AND (completing_until IS NULL OR completing_until <= $2)`, id, now, until)
	if err != nil {
		return false, fmt.Errorf("claim an app upload: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Release lets another completion take an upload.
func (s *Uploads) Release(ctx context.Context, id string) error {
	if _, err := s.db.Exec(ctx, `UPDATE app_uploads SET completing_until = NULL WHERE upload_id = $1`, id); err != nil {
		return fmt.Errorf("release an app upload: %w", err)
	}
	return nil
}

// Busy reports whether a completion holds an upload at now.
func (s *Uploads) Busy(ctx context.Context, id string, now time.Time) (bool, error) {
	var busy bool
	err := s.db.QueryRow(ctx, `SELECT coalesce(completing_until > $2, false) FROM app_uploads WHERE upload_id = $1`, id, now).Scan(&busy)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read an app upload: %w", err)
	}
	return busy, nil
}

// Delete forgets an upload.
func (s *Uploads) Delete(ctx context.Context, id string) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM app_uploads WHERE upload_id = $1`, id); err != nil {
		return fmt.Errorf("delete an app upload: %w", err)
	}
	return nil
}

// Open counts the uploads not expired at now.
func (s *Uploads) Open(ctx context.Context, now time.Time) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM app_uploads WHERE expires_at > $1`, now).Scan(&n); err != nil {
		return 0, fmt.Errorf("count the app uploads: %w", err)
	}
	return n, nil
}

// Expired returns the uploads expired at now that no completion holds.
func (s *Uploads) Expired(ctx context.Context, now time.Time) ([]domain.AppUpload, error) {
	rows, err := s.db.Query(ctx, `SELECT `+uploadColumns+` FROM app_uploads
		WHERE expires_at <= $1 AND (completing_until IS NULL OR completing_until <= $1) ORDER BY expires_at LIMIT 100`, now)
	if err != nil {
		return nil, fmt.Errorf("list the expired app uploads: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.AppUpload, error) {
		u, err := scanUpload(row)
		if err != nil {
			return domain.AppUpload{}, err
		}
		return *u, nil
	})
}
