// Package postgres stores orders in the trading schema.
package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/outbox"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/trading/domain"
	"github.com/skill/exchange/internal/trading/ports"
)

// Store implements ports.Store.
type Store struct {
	db     *pg.DB
	events *event.Factory
}

// NewStore returns a store whose events are built by events.
func NewStore(db *pg.DB, events *event.Factory) *Store { return &Store{db: db, events: events} }

// Tx runs fn in a transaction.
func (s *Store) Tx(ctx context.Context, fn func(ports.Repos) error) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error { return fn(repos{q: tx, events: s.events}) })
}

// Read returns repositories on the pool.
func (s *Store) Read() ports.Repos { return repos{q: s.db, events: s.events} }

type repos struct {
	q      pg.Querier
	events *event.Factory
}

func (r repos) Orders() ports.OrderRepo { return orders(r) }

func (r repos) Emit(ctx context.Context, topic string, msg proto.Message, aggregateType, aggregateID string) error {
	env, err := r.events.New(ctx, msg, aggregateType, aggregateID)
	if err != nil {
		return err
	}
	return outbox.Add(ctx, r.q, topic, env)
}

type orders repos

const columns = `id, user_id, client_order_id, symbol, side, type, time_in_force, stp, price, quantity, quote_amount,
	status, reject_reason, filled_quantity, filled_quote, frozen_asset, frozen_amount, freeze_state, maker_fee_rate,
	taker_fee_rate, base_decimals, quote_decimals, protection_price, cancel_requested, sequence, created_at, updated_at,
	tick_size, lot_size, base_asset, quote_asset, cancel_reason, released, account_type, side_effect, borrowed, borrow_id,
	reject_status, reject_details`

var activeStatuses = []string{string(domain.StatusNew), string(domain.StatusOpen), string(domain.StatusPartiallyFilled)}

func scan(row pgx.Row) (domain.Order, error) {
	var o domain.Order
	var price, qty, quote, protection, tick, lot, borrowed decimal.NullDecimal
	var reject, base, quoteAsset, cancel, borrowID *string
	var rejectStatus *int
	var rejectDetails []byte
	err := row.Scan(&o.ID, &o.UserID, &o.ClientOrderID, &o.Symbol, &o.Side, &o.Type, &o.TimeInForce, &o.STP,
		&price, &qty, &quote, &o.Status, &reject, &o.FilledQuantity, &o.FilledQuote, &o.FrozenAsset, &o.FrozenAmount,
		&o.FreezeState, &o.MakerFeeRate, &o.TakerFeeRate, &o.BaseDecimals, &o.QuoteDecimals, &protection,
		&o.CancelRequested, &o.Sequence, &o.CreatedAt, &o.UpdatedAt, &tick, &lot, &base, &quoteAsset, &cancel, &o.Released,
		&o.AccountType, &o.SideEffect, &borrowed, &borrowID, &rejectStatus, &rejectDetails)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, domain.ErrOrderNotFound
	}
	if err != nil {
		return domain.Order{}, fmt.Errorf("scan order: %w", err)
	}
	o.Price, o.Quantity, o.QuoteAmount, o.ProtectionPrice = price.Decimal, qty.Decimal, quote.Decimal, protection.Decimal
	o.TickSize, o.LotSize = tick.Decimal, lot.Decimal
	o.RejectReason, o.BaseAsset, o.QuoteAsset, o.CancelReason = str(reject), str(base), str(quoteAsset), str(cancel)
	o.Borrowed, o.BorrowID = borrowed.Decimal, str(borrowID)
	if rejectStatus != nil {
		o.RejectStatus = *rejectStatus
	}
	if len(rejectDetails) > 0 {
		// Numbers stay exact, as they came from the refusing service.
		dec := json.NewDecoder(bytes.NewReader(rejectDetails))
		dec.UseNumber()
		if err := dec.Decode(&o.RejectDetails); err != nil {
			return domain.Order{}, fmt.Errorf("scan order %s: reject details: %w", o.ID, err)
		}
	}
	return o, nil
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// null stores zero amounts as NULL.
func null(d decimal.Decimal) decimal.NullDecimal {
	return decimal.NullDecimal{Decimal: d, Valid: !d.IsZero()}
}

// accountType and sideEffect store an order built without them as a SPOT
// order without a side effect.
func accountType(a domain.AccountType) domain.AccountType {
	if a == "" {
		return domain.AccountSpot
	}
	return a
}

func sideEffect(e domain.SideEffect) domain.SideEffect {
	if e == "" {
		return domain.SideEffectNone
	}
	return e
}

// refusal stores a refusal's HTTP status and details, NULL without them.
func refusal(o domain.Order) (*int, []byte, error) {
	var status *int
	if o.RejectStatus != 0 {
		status = &o.RejectStatus
	}
	if len(o.RejectDetails) == 0 {
		return status, nil, nil
	}
	details, err := json.Marshal(o.RejectDetails)
	if err != nil {
		return nil, nil, fmt.Errorf("order %s: reject details: %w", o.ID, err)
	}
	return status, details, nil
}

func text(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (r orders) LockUser(ctx context.Context, userID string) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('orders:' || $1, 0))`, userID); err != nil {
		return fmt.Errorf("lock user orders: %w", err)
	}
	return nil
}

func (r orders) Insert(ctx context.Context, o domain.Order) error {
	rejectStatus, rejectDetails, err := refusal(o)
	if err != nil {
		return err
	}
	_, err = r.q.Exec(ctx, `INSERT INTO orders (`+columns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27,
			$28, $29, $30, $31, $32, $33, $34, $35, $36, $37, $38, $39)`,
		o.ID, o.UserID, o.ClientOrderID, o.Symbol, o.Side, o.Type, o.TimeInForce, o.STP, null(o.Price), null(o.Quantity),
		null(o.QuoteAmount), o.Status, text(o.RejectReason), o.FilledQuantity, o.FilledQuote, o.FrozenAsset, o.FrozenAmount,
		o.FreezeState, o.MakerFeeRate, o.TakerFeeRate, o.BaseDecimals, o.QuoteDecimals, null(o.ProtectionPrice),
		o.CancelRequested, o.Sequence, o.CreatedAt, o.UpdatedAt, null(o.TickSize), null(o.LotSize), text(o.BaseAsset),
		text(o.QuoteAsset), text(o.CancelReason), o.Released, accountType(o.AccountType), sideEffect(o.SideEffect),
		null(o.Borrowed), text(o.BorrowID), rejectStatus, rejectDetails)
	if err != nil {
		return fmt.Errorf("insert order: %w", err)
	}
	return nil
}

func (r orders) Get(ctx context.Context, id string) (domain.Order, error) {
	return scan(r.q.QueryRow(ctx, `SELECT `+columns+` FROM orders WHERE id = $1`, id))
}

func (r orders) GetForUpdate(ctx context.Context, id string) (domain.Order, error) {
	return scan(r.q.QueryRow(ctx, `SELECT `+columns+` FROM orders WHERE id = $1 FOR UPDATE`, id))
}

func (r orders) ByClientID(ctx context.Context, userID, clientOrderID string) (domain.Order, error) {
	return scan(r.q.QueryRow(ctx, `SELECT `+columns+` FROM orders WHERE user_id = $1 AND client_order_id = $2`,
		userID, clientOrderID))
}

func (r orders) Update(ctx context.Context, o domain.Order) error {
	rejectStatus, rejectDetails, err := refusal(o)
	if err != nil {
		return err
	}
	_, err = r.q.Exec(ctx, `UPDATE orders SET status = $2, reject_reason = $3, filled_quantity = $4, filled_quote = $5,
		freeze_state = $6, cancel_requested = $7, sequence = $8, updated_at = $9, cancel_reason = $10, released = $11,
		borrowed = $12, borrow_id = $13, reject_status = $14, reject_details = $15
		WHERE id = $1`,
		o.ID, o.Status, text(o.RejectReason), o.FilledQuantity, o.FilledQuote, o.FreezeState, o.CancelRequested,
		o.Sequence, o.UpdatedAt, text(o.CancelReason), o.Released, null(o.Borrowed), text(o.BorrowID), rejectStatus, rejectDetails)
	if err != nil {
		return fmt.Errorf("update order: %w", err)
	}
	return nil
}

func (r orders) CountActive(ctx context.Context, userID, symbol string) (int, int, error) {
	var onSymbol, total int
	err := r.q.QueryRow(ctx, `SELECT count(*) FILTER (WHERE symbol = $2), count(*) FROM orders
		WHERE user_id = $1 AND status = ANY($3)`, userID, symbol, activeStatuses).Scan(&onSymbol, &total)
	if err != nil {
		return 0, 0, fmt.Errorf("count active orders: %w", err)
	}
	return onSymbol, total, nil
}

func (r orders) Active(ctx context.Context, userID, symbol string) ([]domain.Order, error) {
	return r.query(ctx, `SELECT `+columns+` FROM orders WHERE user_id = $1 AND ($2 = '' OR symbol = $2)
		AND status = ANY($3) ORDER BY id FOR UPDATE`, userID, symbol, activeStatuses)
}

func (r orders) List(ctx context.Context, userID string, f ports.ListFilter) ([]domain.Order, error) {
	statuses := make([]string, len(f.Statuses))
	for i, s := range f.Statuses {
		statuses[i] = string(s)
	}
	return r.query(ctx, `SELECT `+columns+` FROM orders WHERE user_id = $1 AND ($2 = '' OR symbol = $2)
		AND (cardinality($3::text[]) = 0 OR status = ANY($3)) AND ($4 = '' OR id < $4::uuid)
		ORDER BY id DESC LIMIT $5`, userID, f.Symbol, statuses, f.Before, f.Limit)
}

func (r orders) PendingFreeze(ctx context.Context, cutoff time.Time, limit int) ([]domain.Order, error) {
	return r.query(ctx, `SELECT `+columns+` FROM orders WHERE freeze_state = 'PENDING' AND created_at < $1
		ORDER BY created_at LIMIT $2`, cutoff, limit)
}

func (r orders) PendingStats(ctx context.Context) (int, time.Time, error) {
	var n int
	var oldest *time.Time
	err := r.q.QueryRow(ctx, `SELECT count(*), min(created_at) FROM orders WHERE freeze_state = 'PENDING'`).Scan(&n, &oldest)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("count pending orders: %w", err)
	}
	if oldest == nil {
		return n, time.Time{}, nil
	}
	return n, *oldest, nil
}

func (r orders) query(ctx context.Context, sql string, args ...any) ([]domain.Order, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query orders: %w", err)
	}
	defer rows.Close()
	var out []domain.Order
	for rows.Next() {
		o, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (r orders) Unreleased(ctx context.Context, cutoff time.Time, limit int) ([]domain.Order, error) {
	return r.query(ctx, `SELECT `+columns+` FROM orders WHERE released = false AND freeze_state = 'FROZEN'
		AND status IN ('FILLED', 'CANCELED', 'REJECTED', 'EXPIRED') AND updated_at < $1 ORDER BY updated_at LIMIT $2`, cutoff, limit)
}

func (r repos) Fills() ports.FillRepo { return fills(r) }

type fills repos

const fillColumns = `trade_id, order_id, user_id, symbol, side, maker, price, quantity, quote_quantity, fee_asset, fee, sequence, executed_at`

func (r fills) Insert(ctx context.Context, f domain.Fill) error {
	_, err := r.q.Exec(ctx, `INSERT INTO fills (`+fillColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT DO NOTHING`, f.TradeID, f.OrderID, f.UserID, f.Symbol, f.Side, f.Maker, f.Price, f.Quantity, f.Quote,
		f.FeeAsset, f.Fee, f.Seq, f.ExecutedAt)
	if err != nil {
		return fmt.Errorf("insert fill: %w", err)
	}
	return nil
}

func (r fills) OfOrder(ctx context.Context, orderID string) ([]domain.Fill, error) {
	return r.query(ctx, `SELECT `+fillColumns+` FROM fills WHERE order_id = $1 ORDER BY sequence`, orderID)
}

func (r fills) OfUser(ctx context.Context, userID, symbol, before string, limit int) ([]domain.Fill, error) {
	return r.query(ctx, `SELECT `+fillColumns+` FROM fills f WHERE user_id = $1 AND ($2 = '' OR symbol = $2)
		AND ($3 = '' OR (executed_at, trade_id) < (SELECT executed_at, trade_id FROM fills WHERE user_id = $1 AND trade_id = $3::uuid LIMIT 1))
		ORDER BY executed_at DESC, trade_id DESC LIMIT $4`, userID, symbol, before, limit)
}

func (r fills) LastTrade(ctx context.Context, symbol string) (decimal.Decimal, time.Time, error) {
	var price decimal.Decimal
	var at time.Time
	err := r.q.QueryRow(ctx, `SELECT price, executed_at FROM fills WHERE symbol = $1 ORDER BY sequence DESC LIMIT 1`, symbol).Scan(&price, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.Zero, time.Time{}, nil
	}
	if err != nil {
		return decimal.Zero, time.Time{}, fmt.Errorf("last trade: %w", err)
	}
	return price, at, nil
}

func (r fills) query(ctx context.Context, sql string, args ...any) ([]domain.Fill, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query fills: %w", err)
	}
	defer rows.Close()
	var out []domain.Fill
	for rows.Next() {
		var f domain.Fill
		if err := rows.Scan(&f.TradeID, &f.OrderID, &f.UserID, &f.Symbol, &f.Side, &f.Maker, &f.Price, &f.Quantity, &f.Quote,
			&f.FeeAsset, &f.Fee, &f.Seq, &f.ExecutedAt); err != nil {
			return nil, fmt.Errorf("scan fill: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
