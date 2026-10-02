package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
)

func (r repos) Callbacks() ports.CallbackRepo { return callbacks(r) }

type callbacks repos

const callbackColumns = `id, provider, trade_id, kind, status, business_id, coin, address, amount, tx_hash, raw, signature_ok, result,
	detail, attempts, received_at, processed_at`

func scanCallback(row pgx.Row) (domain.Callback, error) {
	var c domain.Callback
	var status *int32
	var amount *decimal.Decimal
	var processed *time.Time
	err := row.Scan(&c.ID, &c.Provider, &c.TradeID, &c.Kind, &status, &c.BusinessID, &c.Coin, &c.Address, &amount, &c.TxHash, &c.Raw,
		&c.SignatureOK, &c.Result, &c.Detail, &c.Attempts, &c.ReceivedAt, &processed)
	if err != nil {
		return domain.Callback{}, err
	}
	c.Status, c.Amount, c.ProcessedAt = -1, amount, at(processed)
	if status != nil {
		c.Status = int(*status)
	}
	return c, nil
}

func (r callbacks) Receive(ctx context.Context, c domain.Callback) (domain.Callback, bool, error) {
	var status *int
	if c.Status >= 0 {
		status = &c.Status
	}
	// Text the database takes (no NUL, valid UTF-8; a verified callback
	// already is, so a replay verifies it again), cut on a character.
	c.Raw = clip(strings.ToValidUTF8(strings.ReplaceAll(c.Raw, "\x00", ""), "\uFFFD"), 16384)
	if c.SignatureOK {
		// The custodian's retry of a callback it sent before: one more attempt.
		stored, err := scanCallback(r.q.QueryRow(ctx, `UPDATE custody_callbacks SET attempts = attempts + 1
			WHERE provider = $1 AND trade_id = $2 AND status IS NOT DISTINCT FROM $3 AND signature_ok
			RETURNING `+callbackColumns, c.Provider, c.TradeID, status))
		switch {
		case err == nil:
			return stored, false, nil
		case !errors.Is(err, pgx.ErrNoRows):
			return domain.Callback{}, false, fmt.Errorf("record callback: %w", err)
		}
	}
	_, err := r.q.Exec(ctx, `INSERT INTO custody_callbacks (`+callbackColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, 1, $15, $16)`,
		c.ID, c.Provider, c.TradeID, c.Kind, status, c.BusinessID, c.Coin, c.Address, c.Amount, c.TxHash, c.Raw, c.SignatureOK, c.Result,
		c.Detail, c.ReceivedAt, stamp(c.ProcessedAt))
	if err != nil {
		return domain.Callback{}, false, fmt.Errorf("record callback: %w", err)
	}
	c.Attempts = 1
	return c, true, nil
}

func (r callbacks) Finish(ctx context.Context, id, result, detail string, at time.Time) error {
	if len(detail) > 1000 {
		detail = detail[:1000]
	}
	if _, err := r.q.Exec(ctx, `UPDATE custody_callbacks SET result = $2, detail = $3, processed_at = $4 WHERE id = $1`,
		id, result, detail, at); err != nil {
		return fmt.Errorf("finish callback: %w", err)
	}
	return nil
}

func (r callbacks) Get(ctx context.Context, id string) (*domain.Callback, error) {
	c, err := scanCallback(r.q.QueryRow(ctx, `SELECT `+callbackColumns+` FROM custody_callbacks WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get callback: %w", err)
	}
	return &c, nil
}

func (r callbacks) Page(ctx context.Context, f ports.CallbackFilter) ([]domain.Callback, error) {
	var after *uuid.UUID
	if f.After != "" {
		id, err := uuid.Parse(f.After)
		if err != nil {
			return nil, apperr.Invalid("not a callback ID: " + f.After)
		}
		after = &id
	}
	rows, err := r.q.Query(ctx, `SELECT `+callbackColumns+` FROM custody_callbacks
		WHERE ($1 = '' OR result = $1) AND ($2 = '' OR kind = $2)
		AND ($3 = '' OR trade_id = $3 OR business_id = $3 OR lower(tx_hash) = lower($3) OR lower(address) = lower($3))
		AND ($4::uuid IS NULL OR id < $4) ORDER BY id DESC LIMIT $5`, f.Result, f.Kind, f.Query, after, f.Limit)
	if err != nil {
		return nil, fmt.Errorf("list callbacks: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Callback, error) { return scanCallback(row) })
	if err != nil {
		return nil, fmt.Errorf("list callbacks: %w", err)
	}
	return out, nil
}

func (r callbacks) RejectedSince(ctx context.Context, t time.Time) (int, error) {
	var n int
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM custody_callbacks WHERE result = 'REJECTED' AND received_at >= $1`, t).Scan(&n); err != nil {
		return 0, fmt.Errorf("count refused callbacks: %w", err)
	}
	return n, nil
}

// clip cuts s to at most n bytes without splitting a character.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

func (r callbacks) Attention(ctx context.Context) (int, time.Time, error) {
	var n int
	var last *time.Time
	err := r.q.QueryRow(ctx, `SELECT count(*) FILTER (WHERE signature_ok AND result IN ('FAILED', 'UNMATCHED', 'DISCREPANCY')), max(received_at)
		FROM custody_callbacks`).Scan(&n, &last)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("count callbacks: %w", err)
	}
	return n, at(last), nil
}
