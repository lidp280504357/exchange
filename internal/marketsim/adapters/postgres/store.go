// Package postgres stores the simulated market's bots, settings and state
// in the marketsim schema.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lidp280504357/exchange/internal/marketsim/domain"
	"github.com/lidp280504357/exchange/internal/marketsim/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/pg"
)

// Store implements ports.Store.
type Store struct{ db *pg.DB }

// NewStore returns a store on db.
func NewStore(db *pg.DB) *Store { return &Store{db: db} }

// Bots lists the bot accounts by label.
func (s *Store) Bots(ctx context.Context) ([]ports.Bot, error) {
	rows, err := s.db.Query(ctx, `SELECT user_id::text, role, label, enabled FROM bots ORDER BY label`)
	if err != nil {
		return nil, fmt.Errorf("list bots: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ports.Bot, error) {
		var b ports.Bot
		var role string
		err := row.Scan(&b.UserID, &role, &b.Label, &b.Enabled)
		b.Role = domain.Role(role)
		return b, err
	})
	if err != nil {
		return nil, fmt.Errorf("list bots: %w", err)
	}
	return out, nil
}

// AddBot registers an account as a bot; the same account again changes
// nothing, another account under a label in use is a conflict.
func (s *Store) AddBot(ctx context.Context, b ports.Bot) error {
	_, err := s.db.Exec(ctx, `INSERT INTO bots (user_id, role, label, enabled) VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id) DO NOTHING`, b.UserID, string(b.Role), b.Label, b.Enabled)
	if _, ok := pg.UniqueViolation(err); ok {
		return apperr.New(apperr.KindConflict, apperr.CodeConflict, "the label "+b.Label+" is another bot's")
	}
	if err != nil {
		return fmt.Errorf("add bot: %w", err)
	}
	return nil
}

// Settings returns the settings and their version.
func (s *Store) Settings(ctx context.Context) (domain.Params, int64, bool, error) {
	var raw []byte
	var version int64
	err := s.db.QueryRow(ctx, `SELECT params, version FROM settings WHERE id = 1`).Scan(&raw, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Params{}, 0, false, nil
	}
	if err != nil {
		return domain.Params{}, 0, false, fmt.Errorf("read settings: %w", err)
	}
	p := domain.DefaultParams() // settings saved before a field existed keep its default
	if err := json.Unmarshal(raw, &p); err != nil {
		return domain.Params{}, 0, false, fmt.Errorf("read settings: %w", err)
	}
	return p, version, true, nil
}

// SaveSettings stores new settings and returns their version.
func (s *Store) SaveSettings(ctx context.Context, p domain.Params, actor string) (int64, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return 0, err
	}
	var version int64
	err = s.db.QueryRow(ctx, `INSERT INTO settings (id, params, version, updated_by, updated_at) VALUES (1, $1, 1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET params = EXCLUDED.params, version = settings.version + 1,
			updated_by = EXCLUDED.updated_by, updated_at = EXCLUDED.updated_at
		RETURNING version`, raw, actor, time.Now()).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("save settings: %w", err)
	}
	return version, nil
}

// State returns the model's saved state.
func (s *Store) State(ctx context.Context) (domain.State, bool, error) {
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT state FROM state WHERE id = 1`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.State{}, false, nil
	}
	if err != nil {
		return domain.State{}, false, fmt.Errorf("read state: %w", err)
	}
	var st domain.State
	if err := json.Unmarshal(raw, &st); err != nil {
		return domain.State{}, false, fmt.Errorf("read state: %w", err)
	}
	return st, true, nil
}

// SaveState stores the model's state.
func (s *Store) SaveState(ctx context.Context, st domain.State) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO state (id, state, saved_at) VALUES (1, $1, $2)
		ON CONFLICT (id) DO UPDATE SET state = EXCLUDED.state, saved_at = EXCLUDED.saved_at`, raw, time.Now()); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	return nil
}
