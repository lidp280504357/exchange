package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/skill/exchange/internal/admin/domain"
)

// access stores the console's access switches in console_access, one row
// (N1).
type access repos

const accessColumns = `require_totp, updated_by, updated_at`

func (r access) get(ctx context.Context, lock string) (*domain.ConsoleAccess, error) {
	var a domain.ConsoleAccess
	err := r.q.QueryRow(ctx, `SELECT `+accessColumns+` FROM console_access`+lock).Scan(&a.RequireTOTP, &a.UpdatedBy, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get console access: %w", err)
	}
	return &a, nil
}

func (r access) Get(ctx context.Context) (*domain.ConsoleAccess, error) { return r.get(ctx, "") }

func (r access) GetForUpdate(ctx context.Context) (*domain.ConsoleAccess, error) {
	return r.get(ctx, " FOR UPDATE")
}

func (r access) Init(ctx context.Context, a domain.ConsoleAccess) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO console_access (`+accessColumns+`) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING`,
		a.RequireTOTP, a.UpdatedBy, a.UpdatedAt)
	if err != nil {
		return false, fmt.Errorf("store console access: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r access) Put(ctx context.Context, a domain.ConsoleAccess) error {
	_, err := r.q.Exec(ctx, `INSERT INTO console_access (`+accessColumns+`) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET require_totp = EXCLUDED.require_totp, updated_by = EXCLUDED.updated_by,
			updated_at = EXCLUDED.updated_at`,
		a.RequireTOTP, a.UpdatedBy, a.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update console access: %w", err)
	}
	return nil
}
