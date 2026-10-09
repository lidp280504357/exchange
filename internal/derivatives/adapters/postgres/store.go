// Package postgres stores derivatives-service's state in the derivatives
// schema.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/skill/exchange/internal/derivatives/domain"
	"github.com/skill/exchange/internal/derivatives/ports"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/outbox"
	"github.com/skill/exchange/internal/platform/pg"
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

func (r repos) Settings() ports.SettingsRepo       { return settings(r) }
func (r repos) Orders() ports.OrderRepo            { return orders(r) }
func (r repos) Positions() ports.PositionRepo      { return positions(r) }
func (r repos) Fills() ports.FillRepo              { return fills(r) }
func (r repos) Pending() ports.PendingRepo         { return pending(r) }
func (r repos) Contracts() ports.ContractStateRepo { return contracts(r) }
func (r repos) Runs() ports.RunRepo                { return runs(r) }

func (r repos) LockUser(ctx context.Context, userID string) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('derivatives:' || $1, 0))`, userID); err != nil {
		return fmt.Errorf("lock user: %w", err)
	}
	return nil
}

func (r repos) Emit(ctx context.Context, topic string, msg proto.Message, aggregateType, aggregateID string) error {
	env, err := r.events.New(ctx, msg, aggregateType, aggregateID)
	if err != nil {
		return err
	}
	return outbox.Add(ctx, r.q, topic, env)
}

type settings repos

func (r settings) Get(ctx context.Context, userID, symbol string) (*domain.Settings, error) {
	var s domain.Settings
	err := r.q.QueryRow(ctx, `SELECT user_id, symbol, position_mode, margin_mode, leverage, updated_at FROM settings
		WHERE user_id = $1 AND symbol = $2`, userID, symbol).Scan(&s.UserID, &s.Symbol, &s.PositionMode, &s.MarginMode, &s.Leverage, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	return &s, nil
}

func (r settings) Save(ctx context.Context, s domain.Settings) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO settings (user_id, symbol, position_mode, margin_mode, leverage, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (user_id, symbol) DO UPDATE SET position_mode = $3, margin_mode = $4,
		leverage = $5, updated_at = $6`, s.UserID, s.Symbol, s.PositionMode, s.MarginMode, s.Leverage, s.UpdatedAt); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}

type orders repos

const orderColumns = `order_id, client_order_id, user_id, symbol, side, position_side, type, time_in_force, price, quantity,
	reduce_only, kind, leverage, margin_mode, maker_fee_rate, taker_fee_rate, lot_size, margin_per_lot, fee_per_lot,
	consumed_quantity, released, status, freeze_state, cancel_requested, cancel_reason, reject_reason, filled_quantity,
	filled_quote, fee, realized_pnl, sequence, created_at, updated_at`

var activeStatuses = []string{string(domain.StatusNew), string(domain.StatusOpen), string(domain.StatusPartiallyFilled)}

func scanOrder(row pgx.Row) (domain.Order, error) {
	var o domain.Order
	err := row.Scan(&o.ID, &o.ClientOrderID, &o.UserID, &o.Symbol, &o.Side, &o.PositionSide, &o.Type, &o.TimeInForce, &o.Price,
		&o.Qty, &o.ReduceOnly, &o.Kind, &o.Leverage, &o.MarginMode, &o.MakerFee, &o.TakerFee, &o.LotSize, &o.MarginPerLot,
		&o.FeePerLot, &o.Consumed, &o.Released, &o.Status, &o.FreezeState, &o.CancelRequested, &o.CancelReason,
		&o.RejectReason, &o.Filled, &o.FilledQuote, &o.Fee, &o.RealizedPnL, &o.Sequence, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, domain.ErrOrderNotFound
	}
	if err != nil {
		return domain.Order{}, fmt.Errorf("scan order: %w", err)
	}
	return o, nil
}

func (r orders) Insert(ctx context.Context, o domain.Order) error {
	_, err := r.q.Exec(ctx, `INSERT INTO orders (`+orderColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
		$13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33)`,
		o.ID, o.ClientOrderID, o.UserID, o.Symbol, o.Side, o.PositionSide, o.Type, o.TimeInForce, o.Price, o.Qty, o.ReduceOnly,
		o.Kind, o.Leverage, o.MarginMode, o.MakerFee, o.TakerFee, o.LotSize, o.MarginPerLot, o.FeePerLot, o.Consumed, o.Released,
		o.Status, o.FreezeState, o.CancelRequested, o.CancelReason, o.RejectReason, o.Filled, o.FilledQuote, o.Fee,
		o.RealizedPnL, o.Sequence, o.CreatedAt, o.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert order: %w", err)
	}
	return nil
}

func (r orders) Get(ctx context.Context, id string) (domain.Order, error) {
	if _, err := uuid.Parse(id); err != nil {
		return domain.Order{}, domain.ErrOrderNotFound
	}
	return scanOrder(r.q.QueryRow(ctx, `SELECT `+orderColumns+` FROM orders WHERE order_id = $1`, id))
}

func (r orders) GetForUpdate(ctx context.Context, id string) (domain.Order, error) {
	if _, err := uuid.Parse(id); err != nil {
		return domain.Order{}, domain.ErrOrderNotFound
	}
	return scanOrder(r.q.QueryRow(ctx, `SELECT `+orderColumns+` FROM orders WHERE order_id = $1 FOR UPDATE`, id))
}

func (r orders) ByClientID(ctx context.Context, userID, clientOrderID string) (domain.Order, error) {
	return scanOrder(r.q.QueryRow(ctx, `SELECT `+orderColumns+` FROM orders WHERE user_id = $1 AND client_order_id = $2`,
		userID, clientOrderID))
}

func (r orders) Update(ctx context.Context, o domain.Order) error {
	_, err := r.q.Exec(ctx, `UPDATE orders SET consumed_quantity = $2, released = $3, status = $4, freeze_state = $5,
		cancel_requested = $6, cancel_reason = $7, reject_reason = $8, filled_quantity = $9, filled_quote = $10, fee = $11,
		realized_pnl = $12, sequence = $13, updated_at = $14 WHERE order_id = $1`,
		o.ID, o.Consumed, o.Released, o.Status, o.FreezeState, o.CancelRequested, o.CancelReason, o.RejectReason, o.Filled,
		o.FilledQuote, o.Fee, o.RealizedPnL, o.Sequence, o.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update order: %w", err)
	}
	return nil
}

func (r orders) Active(ctx context.Context, userID, symbol string) ([]domain.Order, error) {
	return r.query(ctx, `SELECT `+orderColumns+` FROM orders WHERE user_id = $1 AND ($2 = '' OR symbol = $2)
		AND status = ANY($3) ORDER BY order_id`, userID, symbol, activeStatuses)
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

func (r orders) ActiveOn(ctx context.Context, symbols []string) ([]domain.Order, error) {
	return r.query(ctx, `SELECT `+orderColumns+` FROM orders WHERE symbol = ANY($1) AND status = ANY($2)
		ORDER BY order_id`, symbols, activeStatuses)
}

func (r orders) Unreleased(ctx context.Context, userID string) ([]domain.Order, error) {
	return r.query(ctx, `SELECT `+orderColumns+` FROM orders WHERE user_id = $1
		AND (NOT released OR consumed_quantity < filled_quantity)
		AND freeze_state = 'FROZEN' AND (margin_per_lot > 0 OR fee_per_lot > 0) ORDER BY order_id`, userID)
}

func (r orders) List(ctx context.Context, userID string, f ports.ListFilter) ([]domain.Order, error) {
	statuses := make([]string, len(f.Statuses))
	for i, s := range f.Statuses {
		statuses[i] = string(s)
	}
	var before any // NULL: the first page
	if f.Before != "" {
		before = f.Before
	}
	return r.query(ctx, `SELECT `+orderColumns+` FROM orders WHERE user_id = $1 AND ($2 = '' OR symbol = $2)
		AND (cardinality($3::text[]) = 0 OR status = ANY($3)) AND ($4::uuid IS NULL OR order_id < $4::uuid)
		ORDER BY order_id DESC LIMIT $5`, userID, f.Symbol, statuses, before, f.Limit)
}

func (r orders) PendingFreeze(ctx context.Context, cutoff time.Time, limit int) ([]domain.Order, error) {
	return r.query(ctx, `SELECT `+orderColumns+` FROM orders WHERE freeze_state = 'PENDING' AND created_at < $1
		ORDER BY created_at LIMIT $2`, cutoff, limit)
}

func (r orders) ToRelease(ctx context.Context, cutoff time.Time, limit int) ([]domain.Order, error) {
	return r.query(ctx, `SELECT `+orderColumns+` FROM orders WHERE NOT released AND freeze_state = 'FROZEN'
		AND status IN ('FILLED', 'CANCELED', 'REJECTED', 'EXPIRED') AND updated_at < $1 ORDER BY updated_at LIMIT $2`, cutoff, limit)
}

func (r orders) query(ctx context.Context, sql string, args ...any) ([]domain.Order, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query orders: %w", err)
	}
	defer rows.Close()
	var out []domain.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

type positions repos

const positionColumns = `position_id, user_id, symbol, position_side, quantity, entry_cost, margin, margin_mode, leverage,
	realized_pnl, funding, fees, version, opened_at, updated_at, liquidating, liquidation_attempts, liquidation_at, warned_at`

func scanPosition(row pgx.Row) (domain.Position, error) {
	var p domain.Position
	var opened, liquidated, warned *time.Time
	if err := row.Scan(&p.ID, &p.UserID, &p.Symbol, &p.Side, &p.Qty, &p.EntryCost, &p.Margin, &p.MarginMode, &p.Leverage,
		&p.RealizedPnL, &p.Funding, &p.Fees, &p.Version, &opened, &p.UpdatedAt, &p.Liquidating, &p.LiquidationAttempts,
		&liquidated, &warned); err != nil {
		return domain.Position{}, fmt.Errorf("scan position: %w", err)
	}
	for _, t := range []struct {
		dst *time.Time
		src *time.Time
	}{{&p.OpenedAt, opened}, {&p.LiquidationAt, liquidated}, {&p.WarnedAt, warned}} {
		if t.src != nil {
			*t.dst = *t.src
		}
	}
	return p, nil
}

// nullTime stores a zero time as NULL.
func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (r positions) query(ctx context.Context, sql string, args ...any) ([]domain.Position, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query positions: %w", err)
	}
	defer rows.Close()
	var out []domain.Position
	for rows.Next() {
		p, err := scanPosition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r positions) OfUser(ctx context.Context, userID, symbol string) ([]domain.Position, error) {
	return r.query(ctx, `SELECT `+positionColumns+` FROM positions WHERE user_id = $1 AND ($2 = '' OR symbol = $2)
		ORDER BY symbol, position_side`, userID, symbol)
}

func (r positions) Open(ctx context.Context, symbol string) ([]domain.Position, error) {
	return r.query(ctx, `SELECT `+positionColumns+` FROM positions WHERE quantity <> 0 AND ($1 = '' OR symbol = $1)
		ORDER BY symbol, user_id, position_side`, symbol)
}

func (r positions) Totals(ctx context.Context) (map[string]ports.Totals, error) {
	rows, err := r.q.Query(ctx, `SELECT symbol, sum(quantity), sum(CASE WHEN quantity > 0 THEN entry_cost ELSE -entry_cost END),
		coalesce(sum(quantity) FILTER (WHERE quantity > 0), 0), count(*) FILTER (WHERE quantity <> 0)
		FROM positions GROUP BY symbol`)
	if err != nil {
		return nil, fmt.Errorf("position totals: %w", err)
	}
	defer rows.Close()
	out := map[string]ports.Totals{}
	for rows.Next() {
		var symbol string
		var t ports.Totals
		if err := rows.Scan(&symbol, &t.NetQty, &t.NetCost, &t.LongQty, &t.Positions); err != nil {
			return nil, fmt.Errorf("position totals: %w", err)
		}
		out[symbol] = t
	}
	return out, rows.Err()
}

func (r positions) Save(ctx context.Context, p domain.Position) (domain.Position, error) {
	if p.ID == "" {
		p.ID = uuid.Must(uuid.NewV7()).String()
	}
	row := r.q.QueryRow(ctx, `INSERT INTO positions (`+positionColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 1, $13, $14, $15, $16, $17, $18)
		ON CONFLICT (user_id, symbol, position_side) DO UPDATE SET quantity = $5, entry_cost = $6, margin = $7, margin_mode = $8,
			leverage = $9, realized_pnl = $10, funding = $11, fees = $12, version = positions.version + 1, opened_at = $13,
			updated_at = $14, liquidating = $15, liquidation_attempts = $16, liquidation_at = $17, warned_at = $18
		RETURNING `+positionColumns,
		p.ID, p.UserID, p.Symbol, p.Side, p.Qty, p.EntryCost, p.Margin, p.MarginMode, p.Leverage, p.RealizedPnL, p.Funding, p.Fees,
		nullTime(p.OpenedAt), p.UpdatedAt, p.Liquidating, p.LiquidationAttempts, nullTime(p.LiquidationAt), nullTime(p.WarnedAt))
	return scanPosition(row)
}

type fills repos

const fillColumns = `trade_id, side, order_id, user_id, symbol, position_side, maker, price, quantity, closed_quantity, fee,
	fee_waived, realized_pnl, insurance, liquidation, sequence, executed_at, settled`

func (r fills) Has(ctx context.Context, tradeID string, side domain.Side) (bool, error) {
	var ok bool
	err := r.q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM fills WHERE trade_id = $1 AND side = $2)`, tradeID, side).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("look up fill: %w", err)
	}
	return ok, nil
}

func (r fills) Insert(ctx context.Context, f domain.Fill) error {
	_, err := r.q.Exec(ctx, `INSERT INTO fills (`+fillColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
		$13, $14, $15, $16, $17, $18)`, f.TradeID, f.Side, f.OrderID, f.UserID, f.Symbol, f.PositionSide, f.Maker, f.Price, f.Qty,
		f.ClosedQty, f.Fee, f.FeeWaived, f.RealizedPnL, f.Insurance, f.Liquidation, f.Seq, f.ExecutedAt, f.Settled)
	if err != nil {
		return fmt.Errorf("insert fill: %w", err)
	}
	return nil
}

func (r fills) SetSettled(ctx context.Context, tradeID string, side domain.Side) error {
	if _, err := r.q.Exec(ctx, `UPDATE fills SET settled = true WHERE trade_id = $1 AND side = $2`, tradeID, side); err != nil {
		return fmt.Errorf("mark fill settled: %w", err)
	}
	return nil
}

func (r fills) OfUser(ctx context.Context, userID, symbol, before string, limit int) ([]domain.Fill, error) {
	var beforeTrade, beforeSide any // NULL: the first page
	if before != "" {
		t, s, ok := strings.Cut(before, ":")
		if _, err := uuid.Parse(t); !ok || err != nil {
			return nil, fmt.Errorf("bad fill cursor %q", before)
		}
		beforeTrade, beforeSide = t, s
	}
	rows, err := r.q.Query(ctx, `SELECT `+fillColumns+` FROM fills WHERE user_id = $1 AND ($2 = '' OR symbol = $2)
		AND ($3::uuid IS NULL OR (executed_at, trade_id, side) < (SELECT executed_at, trade_id, side FROM fills
			WHERE trade_id = $3::uuid AND side = $4::text))
		ORDER BY executed_at DESC, trade_id DESC, side DESC LIMIT $5`, userID, symbol, beforeTrade, beforeSide, limit)
	if err != nil {
		return nil, fmt.Errorf("query fills: %w", err)
	}
	defer rows.Close()
	var out []domain.Fill
	for rows.Next() {
		var f domain.Fill
		if err := rows.Scan(&f.TradeID, &f.Side, &f.OrderID, &f.UserID, &f.Symbol, &f.PositionSide, &f.Maker, &f.Price, &f.Qty,
			&f.ClosedQty, &f.Fee, &f.FeeWaived, &f.RealizedPnL, &f.Insurance, &f.Liquidation, &f.Seq, &f.ExecutedAt, &f.Settled); err != nil {
			return nil, fmt.Errorf("scan fill: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (r fills) LastPrice(ctx context.Context, symbol string) (decimal.Decimal, error) {
	var p decimal.Decimal
	err := r.q.QueryRow(ctx, `SELECT price FROM fills WHERE symbol = $1 AND NOT liquidation ORDER BY executed_at DESC LIMIT 1`, symbol).Scan(&p)
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.Zero, nil
	}
	if err != nil {
		return decimal.Zero, fmt.Errorf("last fill price: %w", err)
	}
	return p, nil
}

type pending repos

func (r pending) Insert(ctx context.Context, p ports.PendingSettlement) error {
	req, err := json.Marshal(p.Request)
	if err != nil {
		return err
	}
	var position, trade, side any
	if p.PositionID != "" {
		position = p.PositionID
	}
	if p.TradeID != "" {
		trade, side = p.TradeID, string(p.Side)
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO pending_settlements (idem_key, user_id, request, position_id, freeze_move, trade_id,
		side, last_error) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (idem_key) DO NOTHING`,
		p.IdemKey, p.UserID, req, position, p.FreezeMove, trade, side, p.LastError); err != nil {
		return fmt.Errorf("park settlement: %w", err)
	}
	return nil
}

func (r pending) Due(ctx context.Context, limit int) ([]ports.PendingSettlement, error) {
	rows, err := r.q.Query(ctx, `SELECT idem_key, user_id, request, coalesce(position_id::text, ''), freeze_move,
		coalesce(trade_id::text, ''), coalesce(side, ''), attempts, last_error, created_at
		FROM pending_settlements ORDER BY created_at LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("load pending settlements: %w", err)
	}
	defer rows.Close()
	var out []ports.PendingSettlement
	for rows.Next() {
		var p ports.PendingSettlement
		var req []byte
		if err := rows.Scan(&p.IdemKey, &p.UserID, &req, &p.PositionID, &p.FreezeMove, &p.TradeID, &p.Side, &p.Attempts,
			&p.LastError, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan pending settlement: %w", err)
		}
		if err := json.Unmarshal(req, &p.Request); err != nil {
			return nil, fmt.Errorf("pending settlement %s: %w", p.IdemKey, err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r pending) Failed(ctx context.Context, key, reason string) error {
	if _, err := r.q.Exec(ctx, `UPDATE pending_settlements SET attempts = attempts + 1, last_error = $2, updated_at = now()
		WHERE idem_key = $1`, key, reason); err != nil {
		return fmt.Errorf("update pending settlement: %w", err)
	}
	return nil
}

func (r pending) Delete(ctx context.Context, key string) error {
	if _, err := r.q.Exec(ctx, `DELETE FROM pending_settlements WHERE idem_key = $1`, key); err != nil {
		return fmt.Errorf("delete pending settlement: %w", err)
	}
	return nil
}

func (r pending) Count(ctx context.Context) (int, error) {
	var n int
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM pending_settlements`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count pending settlements: %w", err)
	}
	return n, nil
}

func (r pending) CountOf(ctx context.Context, userID string) (int, error) {
	var n int
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM pending_settlements WHERE user_id = $1`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count the user's pending settlements: %w", err)
	}
	return n, nil
}

func (r repos) CrossLiquidations() ports.CrossLiquidationRepo { return crossLiquidations(r) }

type crossLiquidations repos

const crossLiquidationColumns = `liquidation_id, user_id, asset, started_at, equity, balance, flows, status, fee, done_at`

func (r crossLiquidations) query(ctx context.Context, sql string, args ...any) ([]domain.CrossLiquidation, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("load cross liquidations: %w", err)
	}
	defer rows.Close()
	var out []domain.CrossLiquidation
	for rows.Next() {
		var l domain.CrossLiquidation
		var done *time.Time
		if err := rows.Scan(&l.ID, &l.UserID, &l.Asset, &l.StartedAt, &l.Equity, &l.Balance, &l.Flows, &l.Status, &l.Fee,
			&done); err != nil {
			return nil, fmt.Errorf("scan cross liquidation: %w", err)
		}
		if done != nil {
			l.DoneAt = *done
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r crossLiquidations) Open(ctx context.Context, userID, asset string) (*domain.CrossLiquidation, error) {
	list, err := r.query(ctx, `SELECT `+crossLiquidationColumns+` FROM cross_liquidations
		WHERE user_id = $1 AND asset = $2 AND status = 'OPEN'`, userID, asset)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

func (r crossLiquidations) Insert(ctx context.Context, l domain.CrossLiquidation) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO cross_liquidations (liquidation_id, user_id, asset, started_at, equity, balance, flows,
		status) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, l.ID, l.UserID, l.Asset, l.StartedAt, l.Equity, l.Balance, l.Flows,
		l.Status); err != nil {
		return fmt.Errorf("insert cross liquidation: %w", err)
	}
	return nil
}

func (r crossLiquidations) AddFlow(ctx context.Context, id string, flow decimal.Decimal) error {
	if _, err := r.q.Exec(ctx, `UPDATE cross_liquidations SET flows = flows + $2 WHERE liquidation_id = $1 AND status = 'OPEN'`,
		id, flow); err != nil {
		return fmt.Errorf("add to cross liquidation: %w", err)
	}
	return nil
}

func (r crossLiquidations) Update(ctx context.Context, l domain.CrossLiquidation) error {
	if _, err := r.q.Exec(ctx, `UPDATE cross_liquidations SET status = $2, fee = $3, done_at = $4 WHERE liquidation_id = $1`,
		l.ID, l.Status, l.Fee, nullTime(l.DoneAt)); err != nil {
		return fmt.Errorf("update cross liquidation: %w", err)
	}
	return nil
}

func (r crossLiquidations) AllOpen(ctx context.Context) ([]domain.CrossLiquidation, error) {
	return r.query(ctx, `SELECT `+crossLiquidationColumns+` FROM cross_liquidations WHERE status = 'OPEN' ORDER BY started_at`)
}

type contracts repos

func (r contracts) Get(ctx context.Context, symbol string) (*ports.ContractState, error) {
	var s ports.ContractState
	err := r.q.QueryRow(ctx, `SELECT symbol, reduce_only, reason, since, lifted_by FROM contract_states WHERE symbol = $1`, symbol).
		Scan(&s.Symbol, &s.ReduceOnly, &s.Reason, &s.Since, &s.LiftedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load contract state: %w", err)
	}
	return &s, nil
}

func (r contracts) Degrade(ctx context.Context, symbol, reason string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO contract_states (symbol, reduce_only, reason, since) VALUES ($1, true, $2, $3)
		ON CONFLICT (symbol) DO UPDATE SET reduce_only = true, reason = $2, since = $3, lifted_by = '', lifted_at = NULL,
			updated_at = now()
		WHERE NOT contract_states.reduce_only`, symbol, reason, at)
	if err != nil {
		return false, fmt.Errorf("degrade contract: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r contracts) Lift(ctx context.Context, symbol, by string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE contract_states SET reduce_only = false, lifted_by = $2, lifted_at = $3, updated_at = now()
		WHERE symbol = $1 AND reduce_only`, symbol, by, at)
	if err != nil {
		return false, fmt.Errorf("lift reduce-only: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r contracts) All(ctx context.Context) ([]ports.ContractState, error) {
	rows, err := r.q.Query(ctx, `SELECT symbol, reduce_only, reason, since, lifted_by FROM contract_states ORDER BY symbol`)
	if err != nil {
		return nil, fmt.Errorf("load contract states: %w", err)
	}
	defer rows.Close()
	var out []ports.ContractState
	for rows.Next() {
		var s ports.ContractState
		if err := rows.Scan(&s.Symbol, &s.ReduceOnly, &s.Reason, &s.Since, &s.LiftedBy); err != nil {
			return nil, fmt.Errorf("scan contract state: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

type runs repos

func (r runs) Record(ctx context.Context, started time.Time, check string, mismatches int, details []byte) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO reconciliation_runs (started_at, check_name, mismatches, details) VALUES ($1, $2, $3, $4)`,
		started, check, mismatches, details); err != nil {
		return fmt.Errorf("record reconciliation: %w", err)
	}
	return nil
}

type funding repos

func (r repos) Funding() ports.FundingRepo { return funding(r) }

func (r funding) Round(ctx context.Context, symbol string, at time.Time) (*domain.FundingRound, error) {
	var fr domain.FundingRound
	var rate, mark decimal.NullDecimal
	err := r.q.QueryRow(ctx, `SELECT symbol, funding_time, status, funding_rate, mark_price, positions FROM funding_rounds
		WHERE symbol = $1 AND funding_time = $2`, symbol, at).Scan(&fr.Symbol, &fr.FundingTime, &fr.Status, &rate, &mark, &fr.Positions)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load funding round: %w", err)
	}
	fr.Rate, fr.Mark, fr.FundingTime = rate.Decimal, mark.Decimal, fr.FundingTime.UTC()
	return &fr, nil
}

func (r funding) Snapshot(ctx context.Context, fr domain.FundingRound, payments []domain.FundingPayment) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO funding_rounds (symbol, funding_time, status, positions) VALUES ($1, $2, $3, $4)`,
		fr.Symbol, fr.FundingTime, domain.FundingSnapshot, len(payments)); err != nil {
		return fmt.Errorf("insert funding round: %w", err)
	}
	batch := &pgx.Batch{}
	for _, p := range payments {
		batch.Queue(`INSERT INTO funding_payments (symbol, funding_time, position_id, user_id, position_side, quantity, margin_mode)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, p.Symbol, p.FundingTime, p.PositionID, p.UserID, p.Side, p.Qty, p.MarginMode)
	}
	if err := r.q.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("insert funding payments: %w", err)
	}
	return nil
}

func (r funding) Waiting(ctx context.Context) ([]domain.FundingRound, error) {
	rows, err := r.q.Query(ctx, `SELECT symbol, funding_time, status, funding_rate, mark_price, positions FROM funding_rounds
		WHERE status = 'SNAPSHOT' ORDER BY funding_time, symbol`)
	if err != nil {
		return nil, fmt.Errorf("load funding rounds: %w", err)
	}
	defer rows.Close()
	var out []domain.FundingRound
	for rows.Next() {
		var fr domain.FundingRound
		var rate, mark decimal.NullDecimal
		if err := rows.Scan(&fr.Symbol, &fr.FundingTime, &fr.Status, &rate, &mark, &fr.Positions); err != nil {
			return nil, fmt.Errorf("scan funding round: %w", err)
		}
		fr.Rate, fr.Mark, fr.FundingTime = rate.Decimal, mark.Decimal, fr.FundingTime.UTC()
		out = append(out, fr)
	}
	return out, rows.Err()
}

func (r funding) SetRate(ctx context.Context, symbol string, at time.Time, rate, mark decimal.Decimal) error {
	if _, err := r.q.Exec(ctx, `UPDATE funding_rounds SET funding_rate = $3, mark_price = $4 WHERE symbol = $1 AND funding_time = $2`,
		symbol, at, rate, mark); err != nil {
		return fmt.Errorf("set funding rate: %w", err)
	}
	return nil
}

const paymentColumns = `p.symbol, p.funding_time, p.position_id, p.user_id, p.position_side, p.quantity, p.margin_mode,
	coalesce(p.amount, 0), coalesce(p.insurance, 0), p.settled_at IS NOT NULL, coalesce(p.settled_at, 'epoch'),
	coalesce(r.funding_rate, 0), coalesce(r.mark_price, 0)`

func (r funding) payments(ctx context.Context, sql string, args ...any) ([]domain.FundingPayment, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("load funding payments: %w", err)
	}
	defer rows.Close()
	var out []domain.FundingPayment
	for rows.Next() {
		var p domain.FundingPayment
		if err := rows.Scan(&p.Symbol, &p.FundingTime, &p.PositionID, &p.UserID, &p.Side, &p.Qty, &p.MarginMode, &p.Amount,
			&p.Insurance, &p.Settled, &p.SettledAt, &p.Rate, &p.Mark); err != nil {
			return nil, fmt.Errorf("scan funding payment: %w", err)
		}
		p.FundingTime = p.FundingTime.UTC()
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r funding) Unsettled(ctx context.Context, symbol string, at time.Time) ([]domain.FundingPayment, error) {
	return r.payments(ctx, `SELECT `+paymentColumns+` FROM funding_payments p JOIN funding_rounds r USING (symbol, funding_time)
		WHERE p.symbol = $1 AND p.funding_time = $2 AND p.settled_at IS NULL ORDER BY p.position_id`, symbol, at)
}

func (r funding) Settle(ctx context.Context, p domain.FundingPayment) error {
	if _, err := r.q.Exec(ctx, `UPDATE funding_payments SET amount = $4, insurance = $5, settled_at = now()
		WHERE symbol = $1 AND funding_time = $2 AND position_id = $3`, p.Symbol, p.FundingTime, p.PositionID, p.Amount, p.Insurance); err != nil {
		return fmt.Errorf("settle funding payment: %w", err)
	}
	return nil
}

func (r funding) Finish(ctx context.Context, symbol string, at time.Time, status string) error {
	if _, err := r.q.Exec(ctx, `UPDATE funding_rounds SET status = $3, settled_at = now() WHERE symbol = $1 AND funding_time = $2`,
		symbol, at, status); err != nil {
		return fmt.Errorf("finish funding round: %w", err)
	}
	return nil
}

func (r funding) OfUser(ctx context.Context, userID, symbol, before string, limit int) ([]domain.FundingPayment, error) {
	var beforeTime, beforeID any // NULL: the first page
	if before != "" {
		secs, id, ok := strings.Cut(before, ":")
		n, err := strconv.ParseInt(secs, 10, 64)
		if _, perr := uuid.Parse(id); !ok || err != nil || perr != nil {
			return nil, fmt.Errorf("bad funding cursor %q", before)
		}
		beforeTime, beforeID = time.Unix(n, 0).UTC(), id
	}
	return r.payments(ctx, `SELECT `+paymentColumns+` FROM funding_payments p JOIN funding_rounds r USING (symbol, funding_time)
		WHERE p.user_id = $1 AND ($2 = '' OR p.symbol = $2) AND p.settled_at IS NOT NULL
			AND ($3::uuid IS NULL OR (p.funding_time, p.position_id) < ($4::timestamptz, $3::uuid))
		ORDER BY p.funding_time DESC, p.position_id DESC LIMIT $5`, userID, symbol, beforeID, beforeTime, limit)
}

type cross repos

func (r repos) Cross() ports.CrossRepo { return cross(r) }

func (r cross) WarnedAt(ctx context.Context, userID, asset string) (time.Time, error) {
	var at *time.Time
	err := r.q.QueryRow(ctx, `SELECT warned_at FROM cross_accounts WHERE user_id = $1 AND asset = $2`, userID, asset).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && at == nil) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("load cross account: %w", err)
	}
	return *at, nil
}

func (r cross) Warned(ctx context.Context) (map[ports.CrossAccount]time.Time, error) {
	rows, err := r.q.Query(ctx, `SELECT user_id::text, asset, warned_at FROM cross_accounts WHERE warned_at IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("list warned cross accounts: %w", err)
	}
	defer rows.Close()
	out := map[ports.CrossAccount]time.Time{}
	for rows.Next() {
		var a ports.CrossAccount
		var at time.Time
		if err := rows.Scan(&a.UserID, &a.Asset, &at); err != nil {
			return nil, fmt.Errorf("scan warned cross account: %w", err)
		}
		out[a] = at
	}
	return out, rows.Err()
}

func (r cross) SetWarnedAt(ctx context.Context, userID, asset string, at time.Time) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO cross_accounts (user_id, asset, warned_at) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, asset) DO UPDATE SET warned_at = $3, updated_at = now()`, userID, asset, nullTime(at)); err != nil {
		return fmt.Errorf("save cross account: %w", err)
	}
	return nil
}

type conditionals repos

func (r repos) Conditionals() ports.ConditionalRepo { return conditionals(r) }

const conditionalColumns = `conditional_id, user_id, symbol, position_side, side, kind, trigger_price, trigger_by, order_type,
	coalesce(price, 0), coalesce(quantity, 0), status, reason, coalesce(order_id::text, ''), created_at, updated_at`

func scanConditional(row pgx.Row) (domain.Conditional, error) {
	var c domain.Conditional
	err := row.Scan(&c.ID, &c.UserID, &c.Symbol, &c.PositionSide, &c.Side, &c.Kind, &c.TriggerPrice, &c.TriggerBy, &c.OrderType,
		&c.Price, &c.Qty, &c.Status, &c.Reason, &c.OrderID, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Conditional{}, domain.ErrOrderNotFound
	}
	if err != nil {
		return domain.Conditional{}, fmt.Errorf("scan conditional order: %w", err)
	}
	return c, nil
}

func nullDecimal(d decimal.Decimal) decimal.NullDecimal {
	return decimal.NullDecimal{Decimal: d, Valid: !d.IsZero()}
}

func (r conditionals) Insert(ctx context.Context, c domain.Conditional) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO conditional_orders (conditional_id, user_id, symbol, position_side, side, kind,
		trigger_price, trigger_by, order_type, price, quantity, status, reason, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		c.ID, c.UserID, c.Symbol, c.PositionSide, c.Side, c.Kind, c.TriggerPrice, c.TriggerBy, c.OrderType, nullDecimal(c.Price),
		nullDecimal(c.Qty), c.Status, c.Reason, c.CreatedAt, c.UpdatedAt); err != nil {
		return fmt.Errorf("insert conditional order: %w", err)
	}
	return nil
}

func (r conditionals) Get(ctx context.Context, id string) (domain.Conditional, error) {
	if _, err := uuid.Parse(id); err != nil {
		return domain.Conditional{}, domain.ErrOrderNotFound
	}
	return scanConditional(r.q.QueryRow(ctx, `SELECT `+conditionalColumns+` FROM conditional_orders WHERE conditional_id = $1`, id))
}

func (r conditionals) Update(ctx context.Context, c domain.Conditional) error {
	var order any
	if c.OrderID != "" {
		order = c.OrderID
	}
	if _, err := r.q.Exec(ctx, `UPDATE conditional_orders SET status = $2, reason = $3, order_id = $4, updated_at = $5
		WHERE conditional_id = $1`, c.ID, c.Status, c.Reason, order, c.UpdatedAt); err != nil {
		return fmt.Errorf("update conditional order: %w", err)
	}
	return nil
}

func (r conditionals) query(ctx context.Context, sql string, args ...any) ([]domain.Conditional, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("load conditional orders: %w", err)
	}
	defer rows.Close()
	var out []domain.Conditional
	for rows.Next() {
		c, err := scanConditional(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r conditionals) Active(ctx context.Context, symbol string) ([]domain.Conditional, error) {
	return r.query(ctx, `SELECT `+conditionalColumns+` FROM conditional_orders WHERE status = 'ACTIVE' AND ($1 = '' OR symbol = $1)
		ORDER BY conditional_id`, symbol)
}

func (r conditionals) ActiveOn(ctx context.Context, symbols []string) ([]domain.Conditional, error) {
	return r.query(ctx, `SELECT `+conditionalColumns+` FROM conditional_orders WHERE status = 'ACTIVE' AND symbol = ANY($1)
		ORDER BY conditional_id`, symbols)
}

func (r conditionals) OfUser(ctx context.Context, userID, symbol, status, before string, limit int) ([]domain.Conditional, error) {
	var cursor any // NULL: the first page
	if before != "" {
		cursor = before
	}
	return r.query(ctx, `SELECT `+conditionalColumns+` FROM conditional_orders WHERE user_id = $1 AND ($2 = '' OR symbol = $2)
		AND ($3 = '' OR status = $3) AND ($4::uuid IS NULL OR conditional_id < $4::uuid)
		ORDER BY conditional_id DESC LIMIT $5`, userID, symbol, status, cursor, limit)
}
