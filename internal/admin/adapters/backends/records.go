package backends

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/admin/ports"
)

// Records implements ports.Records on the ClickHouse read models: orders
// (orders_current), trades, deposits (wallet_deposits) and the overview's
// sums. They lag the services by a few seconds.
type Records struct{ Conn driver.Conn }

// botList is the bots' user IDs as an array argument: never empty, as an
// empty array literal has no element type.
func botList(bots []string) []string { return append([]string{""}, bots...) }

func decimalPtr(d *decimal.Decimal) *string {
	if d == nil {
		return nil
	}
	s := d.String()
	return &s
}

// orderKeyLead is how much earlier than its first update an order's ID
// may have been made: created_key, the time in the ID, is orders_state's
// key, and an order's created_at is its first update, minutes after the
// ID at most (review BK).
const orderKeyLead = time.Hour

// Orders returns a page of spot orders, newest first. The created_at range
// and a page's cursor bound created_key too, so a read takes the parts of
// that time alone instead of every order.
func (r Records) Orders(ctx context.Context, q ports.OrderQuery) ([]ports.Order, string, error) {
	pc, err := pageOf(q.Cursor, q.Limit)
	if err != nil {
		return nil, "", err
	}
	from, to := timeRange(q.From, q.To)
	rows, err := r.Conn.Query(ctx, `SELECT toString(order_id), client_order_id, toString(user_id), symbol, side, type, time_in_force,
		price, quantity, quote_amount, status, filled_quantity, filled_quote, reason, created_at, updated_at FROM orders_current
		WHERE (? = '' OR toString(user_id) = ?) AND (? = '' OR symbol = ?) AND (? = '' OR status = ?) AND (? = '' OR side = ?)
		AND (? = '' OR toString(order_id) = ?) AND (? = '' OR has(?, toString(user_id)) = (? = 'bots'))
		AND created_at >= `+ms+` AND created_at < `+ms+` AND created_key >= `+ms+` AND created_key < `+ms+`
		AND (NOT ? OR ((created_at, toString(order_id)) < (`+ms+`, ?) AND created_key <= `+ms+`))
		ORDER BY created_at DESC, toString(order_id) DESC LIMIT ?`,
		q.UserID, q.UserID, q.Symbol, q.Symbol, q.Status, q.Status, q.Side, q.Side, q.OrderID, q.OrderID, q.Accounts, botList(q.Bots), q.Accounts,
		from, to, from-orderKeyLead.Milliseconds(), to, pc.on, pc.at.UnixMilli(), pc.id, pc.at.UnixMilli(), pc.limit+1)
	if err != nil {
		return nil, "", unavailable(err)
	}
	defer func() { _ = rows.Close() }()
	out := []ports.Order{}
	for rows.Next() {
		var o ports.Order
		var price, qty, quote *decimal.Decimal
		var filled, filledQuote decimal.Decimal
		if err := rows.Scan(&o.OrderID, &o.ClientOrderID, &o.UserID, &o.Symbol, &o.Side, &o.Type, &o.TimeInForce, &price, &qty, &quote,
			&o.Status, &filled, &filledQuote, &o.Reason, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, "", unavailable(err)
		}
		o.Price, o.Quantity, o.QuoteAmount = decimalPtr(price), decimalPtr(qty), decimalPtr(quote)
		o.FilledQuantity, o.FilledQuote = filled.String(), filledQuote.String()
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, "", unavailable(err)
	}
	next := pc.next(len(out), func(i int) (time.Time, string) { return out[i].CreatedAt, out[i].OrderID })
	return out[:min(len(out), pc.limit)], next, nil
}

// OpenOrders counts the orders resting on a pair or contract: new, open
// or partly filled (the read model has spot and contract orders alike).
func (r Records) OpenOrders(ctx context.Context, symbol string) (int, error) {
	var n uint64
	if err := r.Conn.QueryRow(ctx, `SELECT count() FROM orders_current WHERE symbol = ? AND status IN ('NEW', 'OPEN', 'PARTIALLY_FILLED')`,
		symbol).Scan(&n); err != nil {
		return 0, unavailable(err)
	}
	return int(n), nil
}

// Trades returns a page of spot trades, newest first.
func (r Records) Trades(ctx context.Context, q ports.TradeQuery) ([]ports.Trade, string, error) {
	pc, err := pageOf(q.Cursor, q.Limit)
	if err != nil {
		return nil, "", err
	}
	from, to := timeRange(q.From, q.To)
	rows, err := r.Conn.Query(ctx, `SELECT toString(trade_id), symbol, trade_number, price, quantity, quote_quantity, taker_side,
		toString(buyer_user_id), toString(buyer_order_id), toString(seller_user_id), toString(seller_order_id), buyer_is_maker,
		buyer_fee, seller_fee, executed_at, house_side FROM trades FINAL
		WHERE (? = '' OR symbol = ?) AND (? = '' OR toString(buyer_user_id) = ? OR toString(seller_user_id) = ?)
		AND (? = '' OR (has(?, toString(buyer_user_id)) AND has(?, toString(seller_user_id))) = (? = 'bots'))
		AND executed_at >= `+ms+` AND executed_at < `+ms+` AND (NOT ? OR (executed_at, toString(trade_id)) < (`+ms+`, ?))
		ORDER BY executed_at DESC, toString(trade_id) DESC LIMIT ?`,
		q.Symbol, q.Symbol, q.UserID, q.UserID, q.UserID, q.Accounts, botList(q.Bots), botList(q.Bots), q.Accounts,
		from, to, pc.on, pc.at.UnixMilli(), pc.id, pc.limit+1)
	if err != nil {
		return nil, "", unavailable(err)
	}
	defer func() { _ = rows.Close() }()
	out := []ports.Trade{}
	for rows.Next() {
		var t ports.Trade
		var v [5]decimal.Decimal
		if err := rows.Scan(&t.TradeID, &t.Symbol, &t.TradeNumber, &v[0], &v[1], &v[2], &t.TakerSide, &t.BuyerUserID, &t.BuyerOrderID,
			&t.SellerUserID, &t.SellerOrderID, &t.BuyerIsMaker, &v[3], &v[4], &t.ExecutedAt, &t.HouseSide); err != nil {
			return nil, "", unavailable(err)
		}
		t.Price, t.Quantity, t.QuoteQuantity, t.BuyerFee, t.SellerFee = v[0].String(), v[1].String(), v[2].String(), v[3].String(), v[4].String()
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, "", unavailable(err)
	}
	next := pc.next(len(out), func(i int) (time.Time, string) { return out[i].ExecutedAt, out[i].TradeID })
	return out[:min(len(out), pc.limit)], next, nil
}

// Deposits returns a page of deposits, newest first by their ID (UUIDv7,
// so by when they were detected: the state's time moves as they confirm).
func (r Records) Deposits(ctx context.Context, q ports.DepositQuery) ([]ports.Deposit, string, error) {
	pc, err := pageOf(q.Cursor, q.Limit)
	if err != nil {
		return nil, "", err
	}
	rows, err := r.Conn.Query(ctx, `SELECT toString(deposit_id), user_id, asset, network, kind, address, tx_hash, amount, status, unclaimed,
		reason, confirmations, required_confirmations, updated_at FROM wallet_deposits FINAL
		WHERE (? = '' OR user_id = ?) AND (? = '' OR asset = ?) AND (? = '' OR network = ?) AND (? = '' OR status = ?)
		AND (? = '' OR lower(tx_hash) = lower(?)) AND (NOT ? OR toString(deposit_id) < ?)
		ORDER BY toString(deposit_id) DESC LIMIT ?`,
		q.UserID, q.UserID, q.Asset, q.Asset, q.Network, q.Network, q.Status, q.Status, q.TxHash, q.TxHash, pc.on, pc.id, pc.limit+1)
	if err != nil {
		return nil, "", unavailable(err)
	}
	defer func() { _ = rows.Close() }()
	out := []ports.Deposit{}
	for rows.Next() {
		var d ports.Deposit
		var amount decimal.Decimal
		if err := rows.Scan(&d.DepositID, &d.UserID, &d.Asset, &d.Network, &d.Kind, &d.Address, &d.TxHash, &amount, &d.Status, &d.Unclaimed,
			&d.Reason, &d.Confirmations, &d.RequiredConfirmations, &d.UpdatedAt); err != nil {
			return nil, "", unavailable(err)
		}
		d.Amount = amount.String()
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, "", unavailable(err)
	}
	next := pc.next(len(out), func(i int) (time.Time, string) { return time.Time{}, out[i].DepositID })
	return out[:min(len(out), pc.limit)], next, nil
}

// Activity sums the overview's figures: the last 24 hours' trades,
// traders and turnover, the deposits and withdrawals waiting, the risk
// events, and per UTC day for the last days the trades and the USDT
// turnover.
func (r Records) Activity(ctx context.Context, days int) (ports.Activity, error) {
	out := ports.Activity{TradesByDay: map[string]uint64{}, TurnoverUSDTByDay: map[string]string{}}
	since := time.Now().Add(-24 * time.Hour).UnixMilli()
	if err := r.Conn.QueryRow(ctx, `SELECT count(), uniqExact(u) FROM (
			SELECT trade_id, buyer_user_id AS u FROM trades FINAL WHERE executed_at >= `+ms+`
			UNION ALL SELECT trade_id, seller_user_id AS u FROM trades FINAL WHERE executed_at >= `+ms+`)`, since, since).
		Scan(&out.Trades24h, &out.ActiveTraders24h); err != nil {
		return out, unavailable(err)
	}
	out.Trades24h /= 2 // each trade counted from both sides
	rows, err := r.Conn.Query(ctx, `SELECT quote_asset, sum(quote_quantity) FROM trades FINAL WHERE executed_at >= `+ms+`
		GROUP BY quote_asset ORDER BY quote_asset`, since)
	if err != nil {
		return out, unavailable(err)
	}
	for rows.Next() {
		var t ports.Turnover
		var amount decimal.Decimal
		if err := rows.Scan(&t.QuoteAsset, &amount); err != nil {
			_ = rows.Close()
			return out, unavailable(err)
		}
		t.Amount = amount.String()
		out.Turnover24h = append(out.Turnover24h, t)
	}
	_ = rows.Close()
	if err := r.Conn.QueryRow(ctx, `SELECT
			(SELECT count() FROM wallet_deposits FINAL WHERE status IN ('DETECTED', 'CONFIRMING')),
			(SELECT count() FROM wallet_withdrawals FINAL WHERE status = 'PENDING_REVIEW'),
			(SELECT count() FROM events WHERE topic = 'risk.events' AND occurred_at >= `+ms+`)`, since).
		Scan(&out.PendingDeposits, &out.PendingWithdraws, &out.RiskEvents24h); err != nil {
		return out, unavailable(err)
	}
	if days <= 0 {
		return out, nil
	}
	daily, err := r.Conn.Query(ctx, `SELECT toString(toDate(executed_at)) AS day, count(), sumIf(quote_quantity, quote_asset = 'USDT')
		FROM trades FINAL WHERE executed_at >= toDateTime64(today() - ?, 3, 'UTC') GROUP BY day`, days-1)
	if err != nil {
		return out, unavailable(err)
	}
	defer func() { _ = daily.Close() }()
	for daily.Next() {
		var day string
		var n uint64
		var usdt decimal.Decimal
		if err := daily.Scan(&day, &n, &usdt); err != nil {
			return out, unavailable(err)
		}
		out.TradesByDay[day], out.TurnoverUSDTByDay[day] = n, usdt.String()
	}
	if err := daily.Err(); err != nil {
		return out, unavailable(err)
	}
	return out, nil
}
