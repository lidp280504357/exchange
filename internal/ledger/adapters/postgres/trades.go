package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/ledger/domain"
)

type trades repos

const tradeColumns = `trade_id, symbol, trade_number, base_asset, quote_asset, price, quantity, quote_quantity,
	buyer_order_id, buyer_user_id, seller_order_id, seller_user_id, buyer_is_maker, buyer_fee, seller_fee,
	buyer_limit_price, event_id, executed_at, status, error_code, error, attempts, settled_at, house_side`

func scanTrade(row pgx.Row) (domain.Trade, error) {
	var t domain.Trade
	var id uuid.UUID
	var limit decimal.NullDecimal
	var settled *time.Time
	err := row.Scan(&id, &t.Symbol, &t.Number, &t.BaseAsset, &t.QuoteAsset, &t.Price, &t.Quantity, &t.Quote,
		&t.BuyerOrderID, &t.BuyerUserID, &t.SellerOrderID, &t.SellerUserID, &t.BuyerIsMaker, &t.BuyerFee, &t.SellerFee,
		&limit, &t.EventID, &t.ExecutedAt, &t.Status, &t.ErrorCode, &t.Error, &t.Attempts, &settled, &t.HouseSide)
	t.ID, t.BuyerLimit = id.String(), limit.Decimal
	if settled != nil {
		t.SettledAt = *settled
	}
	return t, err
}

func (r trades) Statuses(ctx context.Context, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	rows, err := r.q.Query(ctx, `SELECT trade_id, status FROM trades WHERE trade_id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("trade statuses: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var status string
		if err := rows.Scan(&id, &status); err != nil {
			return nil, fmt.Errorf("trade statuses: %w", err)
		}
		out[id.String()] = status
	}
	return out, rows.Err()
}

func (r trades) Insert(ctx context.Context, t domain.Trade) error {
	_, err := r.q.Exec(ctx, `INSERT INTO trades (`+tradeColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)`,
		t.ID, t.Symbol, t.Number, t.BaseAsset, t.QuoteAsset, t.Price, t.Quantity, t.Quote,
		t.BuyerOrderID, t.BuyerUserID, t.SellerOrderID, t.SellerUserID, t.BuyerIsMaker, t.BuyerFee, t.SellerFee,
		nullDecimal(t.BuyerLimit), t.EventID, t.ExecutedAt, t.Status, t.ErrorCode, t.Error, t.Attempts, nullTime(t.SettledAt),
		t.HouseSide)
	if err != nil {
		return fmt.Errorf("insert trade: %w", err)
	}
	return nil
}

func (r trades) Update(ctx context.Context, t domain.Trade) error {
	_, err := r.q.Exec(ctx, `UPDATE trades SET status = $2, error_code = $3, error = $4, attempts = $5, settled_at = $6
		WHERE trade_id = $1`, t.ID, t.Status, t.ErrorCode, t.Error, t.Attempts, nullTime(t.SettledAt))
	if err != nil {
		return fmt.Errorf("update trade: %w", err)
	}
	return nil
}

func (r trades) List(ctx context.Context, status string, limit int) ([]domain.Trade, error) {
	rows, err := r.q.Query(ctx, `SELECT `+tradeColumns+` FROM trades WHERE ($1 = '' OR status = $1)
		ORDER BY recorded_at DESC, trade_id LIMIT $2`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("list trades: %w", err)
	}
	defer rows.Close()
	var out []domain.Trade
	for rows.Next() {
		t, err := scanTrade(rows)
		if err != nil {
			return nil, fmt.Errorf("list trades: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func nullDecimal(d decimal.Decimal) decimal.NullDecimal {
	return decimal.NullDecimal{Decimal: d, Valid: !d.IsZero()}
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
