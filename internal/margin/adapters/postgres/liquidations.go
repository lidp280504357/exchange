package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
)

type liquidations repos

const liquidationColumns = `liquidation_id, user_id, account_type, symbol, trigger, approval_id, requested_by, status, step,
	prior_status, margin_level, total_asset, total_liability, fee_rate, quote_asset, traded, fee, fee_usdt,
	insurance_covered, repaid, remaining, note, started_at, step_at, completed_at`

func scanLiquidation(row pgx.Row) (ports.Liquidation, error) {
	var l ports.Liquidation
	var accountType, symbol string
	var approval *string
	var repaid, remaining []byte
	var completed *time.Time
	if err := row.Scan(&l.ID, &l.UserID, &accountType, &symbol, &l.Trigger, &approval, &l.RequestedBy, &l.Status, &l.Step,
		&l.PriorStatus, &l.MarginLevel, &l.TotalAsset, &l.TotalLiability, &l.FeeRate, &l.QuoteAsset, &l.Traded, &l.Fee,
		&l.FeeUSDT, &l.InsuranceCovered, &repaid, &remaining, &l.Note, &l.StartedAt, &l.StepAt, &completed); err != nil {
		return ports.Liquidation{}, err
	}
	if err := json.Unmarshal(repaid, &l.Repaid); err != nil {
		return ports.Liquidation{}, fmt.Errorf("liquidation %s repaid: %w", l.ID, err)
	}
	if err := json.Unmarshal(remaining, &l.Remaining); err != nil {
		return ports.Liquidation{}, fmt.Errorf("liquidation %s remaining: %w", l.ID, err)
	}
	l.Account, l.ApprovalID, l.CompletedAt = account(accountType, symbol), stringOf(approval), timeOf(completed)
	return l, nil
}

// amounts stores a list of amounts as JSON ([] when empty).
func amounts(list []ports.AssetAmount) []byte {
	if list == nil {
		list = []ports.AssetAmount{}
	}
	raw, _ := json.Marshal(list) // decimals and strings only
	return raw
}

func (r liquidations) Insert(ctx context.Context, l ports.Liquidation) error {
	_, err := r.q.Exec(ctx, `INSERT INTO liquidations (`+liquidationColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
		$11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25)`,
		l.ID, l.UserID, l.Account.Type, l.Account.Symbol, l.Trigger, nullUUID(l.ApprovalID), l.RequestedBy, l.Status, l.Step,
		l.PriorStatus, l.MarginLevel, l.TotalAsset, l.TotalLiability, l.FeeRate, l.QuoteAsset, l.Traded, l.Fee, l.FeeUSDT,
		l.InsuranceCovered, amounts(l.Repaid), amounts(l.Remaining), l.Note, l.StartedAt, l.StepAt, nullTime(l.CompletedAt))
	if err != nil {
		return fmt.Errorf("insert liquidation: %w", err)
	}
	return nil
}

func (r liquidations) one(ctx context.Context, sql string, args ...any) (ports.Liquidation, bool, error) {
	l, err := scanLiquidation(r.q.QueryRow(ctx, sql, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.Liquidation{}, false, nil
	}
	if err != nil {
		return ports.Liquidation{}, false, fmt.Errorf("load liquidation: %w", err)
	}
	return l, true, nil
}

func (r liquidations) Get(ctx context.Context, id string) (ports.Liquidation, bool, error) {
	return r.one(ctx, `SELECT `+liquidationColumns+` FROM liquidations WHERE liquidation_id = $1`, id)
}

func (r liquidations) GetForUpdate(ctx context.Context, id string) (ports.Liquidation, error) {
	l, ok, err := r.one(ctx, `SELECT `+liquidationColumns+` FROM liquidations WHERE liquidation_id = $1 FOR UPDATE`, id)
	if err == nil && !ok {
		err = fmt.Errorf("liquidation %s vanished", id)
	}
	return l, err
}

func (r liquidations) Update(ctx context.Context, l ports.Liquidation) error {
	tag, err := r.q.Exec(ctx, `UPDATE liquidations SET status = $2, step = $3, traded = $4, fee = $5, fee_usdt = $6,
		insurance_covered = $7, repaid = $8, remaining = $9, note = $10, step_at = $11, completed_at = $12 WHERE liquidation_id = $1`,
		l.ID, l.Status, l.Step, l.Traded, l.Fee, l.FeeUSDT, l.InsuranceCovered, amounts(l.Repaid), amounts(l.Remaining),
		l.Note, l.StepAt, nullTime(l.CompletedAt))
	if err != nil {
		return fmt.Errorf("update liquidation: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("update liquidation %s: no row", l.ID)
	}
	return nil
}

func (r liquidations) list(ctx context.Context, sql string, args ...any) ([]ports.Liquidation, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	return collect(rows, err, "liquidations", scanLiquidation)
}

func (r liquidations) Running(ctx context.Context) ([]ports.Liquidation, error) {
	return r.list(ctx, `SELECT `+liquidationColumns+` FROM liquidations WHERE status <> 'COMPLETED' ORDER BY started_at`)
}

func (r liquidations) RunningOf(ctx context.Context, userID string, a domain.Account) (ports.Liquidation, bool, error) {
	return r.one(ctx, `SELECT `+liquidationColumns+` FROM liquidations WHERE user_id = $1 AND account_type = $2 AND symbol = $3
		AND status <> 'COMPLETED'`, userID, a.Type, a.Symbol)
}

func (r liquidations) ByApproval(ctx context.Context, approvalID string) (ports.Liquidation, bool, error) {
	if _, err := uuid.Parse(approvalID); err != nil {
		return ports.Liquidation{}, false, nil
	}
	return r.one(ctx, `SELECT `+liquidationColumns+` FROM liquidations WHERE approval_id = $1`, approvalID)
}

func (r liquidations) OfUser(ctx context.Context, userID string, a *domain.Account, before string, limit int) ([]ports.Liquidation, error) {
	var accountType, symbol *string
	if a != nil {
		t := string(a.Type)
		accountType, symbol = &t, &a.Symbol
	}
	var cursor any // NULL: the first page
	if before != "" {
		if _, err := uuid.Parse(before); err != nil {
			return nil, nil
		}
		cursor = before
	}
	return r.list(ctx, `SELECT `+liquidationColumns+` FROM liquidations WHERE user_id = $1
		AND ($2::text IS NULL OR (account_type = $2 AND symbol = $3)) AND ($4::uuid IS NULL OR liquidation_id < $4::uuid)
		ORDER BY liquidation_id DESC LIMIT $5`, userID, accountType, symbol, cursor, limit)
}

const liquidationOrderColumns = `liquidation_id, symbol, side, attempt, quantity, quote_amount, order_id, status, order_status,
	filled_quantity, filled_quote, error, created_at, updated_at`

func scanLiquidationOrder(row pgx.Row) (ports.LiquidationOrder, error) {
	var o ports.LiquidationOrder
	var order *string
	if err := row.Scan(&o.LiquidationID, &o.Symbol, &o.Side, &o.Attempt, &o.Quantity, &o.QuoteAmount, &order, &o.Status,
		&o.OrderStatus, &o.FilledQuantity, &o.FilledQuote, &o.Error, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return ports.LiquidationOrder{}, err
	}
	o.OrderID = stringOf(order)
	return o, nil
}

func (r liquidations) Orders(ctx context.Context, id string) ([]ports.LiquidationOrder, error) {
	rows, err := r.q.Query(ctx, `SELECT `+liquidationOrderColumns+` FROM liquidation_orders WHERE liquidation_id = $1
		ORDER BY created_at, symbol, side, attempt`, id)
	return collect(rows, err, "liquidation orders", scanLiquidationOrder)
}

func (r liquidations) AddOrder(ctx context.Context, o ports.LiquidationOrder) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO liquidation_orders (`+liquidationOrderColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14) ON CONFLICT DO NOTHING`, o.LiquidationID, o.Symbol,
		o.Side, o.Attempt, o.Quantity, o.QuoteAmount, nullUUID(o.OrderID), o.Status, o.OrderStatus, o.FilledQuantity, o.FilledQuote,
		o.Error, o.CreatedAt, o.CreatedAt); err != nil {
		return fmt.Errorf("insert liquidation order: %w", err)
	}
	return nil
}

func (r liquidations) SetOrder(ctx context.Context, o ports.LiquidationOrder) error {
	tag, err := r.q.Exec(ctx, `UPDATE liquidation_orders SET order_id = $5, status = $6, order_status = $7, filled_quantity = $8,
		filled_quote = $9, error = $10, updated_at = $11 WHERE liquidation_id = $1 AND symbol = $2 AND side = $3 AND attempt = $4`,
		o.LiquidationID, o.Symbol, o.Side, o.Attempt, nullUUID(o.OrderID), o.Status, o.OrderStatus, o.FilledQuantity, o.FilledQuote,
		o.Error, o.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update liquidation order: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("update liquidation order %s %s %s %d: no row", o.LiquidationID, o.Symbol, o.Side, o.Attempt)
	}
	return nil
}
