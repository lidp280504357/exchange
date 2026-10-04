package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/ledger/domain"
	"github.com/skill/exchange/internal/ledger/ports"
	"github.com/skill/exchange/internal/platform/pg"
)

type holds repos

func (r repos) Holds() ports.HoldRepo { return holds(r) }

const holdColumns = `id, user_id, account_type, asset, amount, reason, actor, journal_id, created_at, released_at,
	COALESCE(released_by, ''), COALESCE(release_reason, ''), release_journal_id`

func scanHold(row pgx.Row) (domain.Hold, error) {
	var h domain.Hold
	var id, user, journal uuid.UUID
	var released *time.Time
	var releaseJournal *uuid.UUID
	err := row.Scan(&id, &user, &h.AccountType, &h.Asset, &h.Amount, &h.Reason, &h.Actor, &journal, &h.CreatedAt, &released,
		&h.ReleasedBy, &h.ReleaseReason, &releaseJournal)
	h.ID, h.UserID, h.JournalID = id.String(), user.String(), journal.String()
	if released != nil {
		h.ReleasedAt = *released
	}
	if releaseJournal != nil {
		h.ReleaseJournalID = releaseJournal.String()
	}
	return h, err
}

func (r holds) get(ctx context.Context, id, lock string) (*domain.Hold, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, nil
	}
	h, err := scanHold(r.q.QueryRow(ctx, `SELECT `+holdColumns+` FROM holds WHERE id = $1`+lock, id))
	if pg.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load hold: %w", err)
	}
	return &h, nil
}

func (r holds) Get(ctx context.Context, id string) (*domain.Hold, error) { return r.get(ctx, id, "") }

func (r holds) GetForUpdate(ctx context.Context, id string) (*domain.Hold, error) {
	return r.get(ctx, id, " FOR UPDATE")
}

func (r holds) Insert(ctx context.Context, h domain.Hold) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO holds (id, user_id, account_type, asset, amount, reason, actor, journal_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		h.ID, h.UserID, h.AccountType, h.Asset, h.Amount, h.Reason, h.Actor, h.JournalID, h.CreatedAt); err != nil {
		return fmt.Errorf("insert hold: %w", err)
	}
	return nil
}

func (r holds) OthersActive(ctx context.Context, h domain.Hold) (decimal.Decimal, error) {
	var sum decimal.Decimal
	if err := r.q.QueryRow(ctx, `SELECT coalesce(sum(amount), 0) FROM holds
		WHERE user_id = $1 AND account_type = $2 AND asset = $3 AND released_at IS NULL AND id <> $4`,
		h.UserID, h.AccountType, h.Asset, h.ID).Scan(&sum); err != nil {
		return decimal.Zero, fmt.Errorf("other holds: %w", err)
	}
	return sum, nil
}

func (r holds) Release(ctx context.Context, h domain.Hold) error {
	// A forced release of nothing only marks the hold: no journal (C5.5 ㉒).
	var journal any
	if h.ReleaseJournalID != "" {
		journal = h.ReleaseJournalID
	}
	tag, err := r.q.Exec(ctx, `UPDATE holds SET released_at = $2, released_by = $3, release_reason = $4, release_journal_id = $5
		WHERE id = $1 AND released_at IS NULL`, h.ID, h.ReleasedAt, h.ReleasedBy, h.ReleaseReason, journal)
	if err != nil {
		return fmt.Errorf("release hold: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrHoldReleased
	}
	return nil
}

func (r holds) OfUser(ctx context.Context, userID string, activeOnly bool, limit int) ([]domain.Hold, error) {
	rows, err := r.q.Query(ctx, `SELECT `+holdColumns+` FROM holds WHERE user_id = $1 AND (NOT $2 OR released_at IS NULL)
		ORDER BY created_at DESC, id DESC LIMIT $3`, userID, activeOnly, limit)
	if err != nil {
		return nil, fmt.Errorf("list holds: %w", err)
	}
	defer rows.Close()
	var out []domain.Hold
	for rows.Next() {
		h, err := scanHold(rows)
		if err != nil {
			return nil, fmt.Errorf("list holds: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
