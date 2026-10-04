package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/ledger/domain"
	"github.com/skill/exchange/internal/ledger/ports"
)

type settings repos

func (r repos) Settings() ports.SettingsRepo { return settings(r) }

const welcomeKey = "welcome_credits"

// creditJSON is a welcome credit as the settings table keeps it: the
// amount a decimal string.
type creditJSON struct {
	Asset  string          `json:"asset"`
	Amount decimal.Decimal `json:"amount"`
}

func (r settings) welcome(ctx context.Context, lock string) (*domain.WelcomeCredits, error) {
	var w domain.WelcomeCredits
	var raw []byte
	err := r.q.QueryRow(ctx, `SELECT value, version, updated_by, updated_at FROM settings WHERE key = $1`+lock, welcomeKey).
		Scan(&raw, &w.Version, &w.UpdatedBy, &w.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read welcome credits: %w", err)
	}
	var list []creditJSON
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("decode welcome credits: %w", err)
	}
	w.Credits = make([]domain.AssetAmount, 0, len(list))
	for _, c := range list {
		w.Credits = append(w.Credits, domain.AssetAmount{Asset: c.Asset, Amount: c.Amount})
	}
	return &w, nil
}

func (r settings) WelcomeCredits(ctx context.Context) (*domain.WelcomeCredits, error) {
	return r.welcome(ctx, "")
}

func (r settings) WelcomeCreditsForUpdate(ctx context.Context) (*domain.WelcomeCredits, error) {
	return r.welcome(ctx, " FOR UPDATE")
}

func encodeCredits(list []domain.AssetAmount) ([]byte, error) {
	out := make([]creditJSON, 0, len(list))
	for _, c := range list {
		out = append(out, creditJSON{Asset: c.Asset, Amount: c.Amount})
	}
	return json.Marshal(out)
}

func (r settings) SaveWelcomeCredits(ctx context.Context, w domain.WelcomeCredits) error {
	raw, err := encodeCredits(w.Credits)
	if err != nil {
		return err
	}
	_, err = r.q.Exec(ctx, `INSERT INTO settings (key, value, version, updated_by, updated_at) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, version = EXCLUDED.version, updated_by = EXCLUDED.updated_by,
			updated_at = EXCLUDED.updated_at`, welcomeKey, raw, w.Version, w.UpdatedBy, w.UpdatedAt)
	if err != nil {
		return fmt.Errorf("save welcome credits: %w", err)
	}
	return nil
}

func (r settings) SeedWelcomeCredits(ctx context.Context, w domain.WelcomeCredits) (bool, error) {
	raw, err := encodeCredits(w.Credits)
	if err != nil {
		return false, err
	}
	tag, err := r.q.Exec(ctx, `INSERT INTO settings (key, value, version, updated_by, updated_at) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (key) DO NOTHING`, welcomeKey, raw, w.Version, w.UpdatedBy, w.UpdatedAt)
	if err != nil {
		return false, fmt.Errorf("seed welcome credits: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
