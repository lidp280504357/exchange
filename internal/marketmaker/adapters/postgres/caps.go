// Package postgres keeps HOUSE's runtime caps in the marketmaker schema
// (review C45).
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketmaker/domain"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/pg"
)

// Store implements ports.CapsStore.
type Store struct{ db *pg.DB }

// NewStore returns a store on db.
func NewStore(db *pg.DB) *Store { return &Store{db: db} }

const capsColumns = `level, symbol, total, contract, safety, contract_leverage, version, updated_by, updated_at`

func scanCaps(row pgx.Row) (domain.StoredCaps, error) {
	var s domain.StoredCaps
	err := row.Scan(&s.Caps.Level, &s.Caps.Symbol, &s.Caps.Total, &s.Caps.Contract, &s.Caps.Safety, &s.Caps.ContractLeverage,
		&s.Version, &s.UpdatedBy, &s.UpdatedAt)
	return s, err
}

// Caps returns the stored caps; false when none are stored yet.
func (s *Store) Caps(ctx context.Context) (domain.StoredCaps, bool, error) {
	c, err := scanCaps(s.db.QueryRow(ctx, `SELECT `+capsColumns+` FROM house_caps`))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StoredCaps{}, false, nil
	}
	if err != nil {
		return domain.StoredCaps{}, false, fmt.Errorf("read house caps: %w", err)
	}
	return c, true, nil
}

// capsJSON is how a change keeps the caps: decimal strings by the API's
// names.
type capsJSON struct {
	Level            decimal.Decimal `json:"level"`
	Symbol           decimal.Decimal `json:"symbol"`
	Total            decimal.Decimal `json:"total"`
	Contract         decimal.Decimal `json:"contract"`
	Safety           decimal.Decimal `json:"safety"`
	ContractLeverage decimal.Decimal `json:"contract_leverage"`
}

func toJSON(c domain.Caps) capsJSON {
	return capsJSON{c.Level, c.Symbol, c.Total, c.Contract, c.Safety, c.ContractLeverage}
}

func (j capsJSON) caps() domain.Caps {
	return domain.Caps{Level: j.Level, Symbol: j.Symbol, Total: j.Total, Contract: j.Contract, Safety: j.Safety, ContractLeverage: j.ContractLeverage}
}

// Seed stores caps as version 1 unless some are stored, and returns what
// is stored.
func (s *Store) Seed(ctx context.Context, caps domain.Caps, actor string, at time.Time) (domain.StoredCaps, error) {
	var out domain.StoredCaps
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO house_caps (`+capsColumns+`) VALUES ($1, $2, $3, $4, $5, $6, 1, $7, $8) ON CONFLICT (id) DO NOTHING`,
			caps.Level, caps.Symbol, caps.Total, caps.Contract, caps.Safety, caps.ContractLeverage, actor, at)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			body, err := json.Marshal(toJSON(caps))
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO house_caps_changes (version, caps, actor, reason, at) VALUES (1, $1, $2, $3, $4)`,
				body, actor, "the service's environment (HOUSE_*)", at); err != nil {
				return err
			}
		}
		out, err = scanCaps(tx.QueryRow(ctx, `SELECT `+capsColumns+` FROM house_caps`))
		return err
	})
	if err != nil {
		return domain.StoredCaps{}, fmt.Errorf("seed house caps: %w", err)
	}
	return out, nil
}

// Change replaces the stored caps when they are still c.Version and keeps
// the change.
func (s *Store) Change(ctx context.Context, c domain.CapsChange, at time.Time) (domain.StoredCaps, error) {
	var out domain.StoredCaps
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		cur, err := scanCaps(tx.QueryRow(ctx, `SELECT `+capsColumns+` FROM house_caps FOR UPDATE`))
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && cur.Version != c.Version) {
			return domain.ErrCapsVersion.WithDetail("version", fmt.Sprint(cur.Version))
		}
		if err != nil {
			return err
		}
		next := cur.Version + 1
		out, err = scanCaps(tx.QueryRow(ctx, `UPDATE house_caps SET level = $1, symbol = $2, total = $3, contract = $4, safety = $5,
			contract_leverage = $6, version = $7, updated_by = $8, updated_at = $9 RETURNING `+capsColumns,
			c.Caps.Level, c.Caps.Symbol, c.Caps.Total, c.Caps.Contract, c.Caps.Safety, c.Caps.ContractLeverage, next, c.Actor, at))
		if err != nil {
			return err
		}
		body, err := json.Marshal(toJSON(c.Caps))
		if err != nil {
			return err
		}
		prev, err := json.Marshal(toJSON(cur.Caps))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO house_caps_changes (version, caps, previous, actor, approver, approval_id, reason, at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, next, body, prev, c.Actor, c.Approver, c.ApprovalID, c.Reason, at)
		return err
	})
	if err != nil {
		var refused *apperr.Error
		if errors.As(err, &refused) {
			return domain.StoredCaps{}, err
		}
		return domain.StoredCaps{}, fmt.Errorf("change house caps: %w", err)
	}
	return out, nil
}

// Changes returns the latest changes, newest first.
func (s *Store) Changes(ctx context.Context, limit int) ([]domain.CapsRecord, error) {
	rows, err := s.db.Query(ctx, `SELECT version, caps, previous, actor, approver, approval_id, reason, at
		FROM house_caps_changes ORDER BY version DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list house caps changes: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.CapsRecord, error) {
		var r domain.CapsRecord
		var caps []byte
		var prev []byte
		if err := row.Scan(&r.Version, &caps, &prev, &r.Actor, &r.Approver, &r.ApprovalID, &r.Reason, &r.At); err != nil {
			return r, err
		}
		var c capsJSON
		if err := json.Unmarshal(caps, &c); err != nil {
			return r, err
		}
		r.Caps = c.caps()
		if prev != nil {
			var p capsJSON
			if err := json.Unmarshal(prev, &p); err != nil {
				return r, err
			}
			pc := p.caps()
			r.Previous = &pc
		}
		return r, nil
	})
}
