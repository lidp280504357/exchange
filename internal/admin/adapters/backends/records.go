package backends

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
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

// idSet is accounts as the right side of IN: never empty (an empty array
// literal has no element type), led by a value that is no user ID, nor
// the empty one of a deposit nobody has claimed.
func idSet(ids []string) []string { return append([]string{"-"}, ids...) }

// kindArgs are a kind filter's arguments (L1) for the condition
// `(NOT ? OR id IN ?) AND (NOT ? OR id NOT IN ?)`: keep the Only
// accounts, leave out the Except ones. IN makes a set of them once; has()
// would go through the array for each row (on the test server's 1.2
// million orders and 1,710 accounts of the other kinds, 3.7 s against
// 0.7 s).
func kindArgs(f ports.KindFilter) []any {
	return []any{f.Only != nil, idSet(f.Only), f.Except != nil, idSet(f.Except)}
}

// The other kinds of ports.ActivityKinds (L0).
const (
	kindBot    = "BOT"
	kindTest   = "TEST"
	kindSystem = "SYSTEM"
)

// kindCond is a kind filter's condition on a column of account IDs (L1)
// with its arguments: the Only accounts kept, the Except ones left out.
func kindCond(col string, f ports.KindFilter) (string, []any) {
	c := "toString(" + col + ")"
	return "(NOT ? OR " + c + " IN ?) AND (NOT ? OR " + c + " NOT IN ?)", kindArgs(f)
}

// kindsCond is a kind filter's condition on a trade's two sides: kept when
// one of them is of the kinds (an Only side, or one not left out).
func kindsCond(a, b string, f ports.KindFilter) (string, []any) {
	a, b = "toString("+a+")", "toString("+b+")"
	return "(NOT ? OR " + a + " IN ? OR " + b + " IN ?) AND (NOT ? OR NOT (" + a + " IN ? AND " + b + " IN ?))",
		[]any{f.Only != nil, idSet(f.Only), idSet(f.Only), f.Except != nil, idSet(f.Except), idSet(f.Except)}
}

// kindQuerySize is how long a query with a kind filter may be: the
// accounts go in as text, about 40 bytes each, and ClickHouse parses 256
// KiB at most by default - 6,500 accounts, half that for a trade's two
// sides.
const kindQuerySize = 16 << 20

// kindCtx is the context of a query that carries a kind filter's accounts,
// however many.
func kindCtx(ctx context.Context, f ports.KindFilter) context.Context {
	if !f.On() {
		return ctx
	}
	return longQuery(ctx)
}

// longQuery is the context of a query that carries accounts as text.
func longQuery(ctx context.Context) context.Context {
	return clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{"max_query_size": kindQuerySize}))
}

func decimalPtr(d *decimal.Decimal) *string {
	if d == nil {
		return nil
	}
	s := d.String()
	return &s
}

// orderKeyLead is how much earlier than its first update an order's ID
// may have been made: created_key, the time in the ID, is orders_state's
// key, and an order's created_at is its first update, at most 422 s after
// the ID over the test server's 505,885 orders (one order of 2026-10-02;
// review BQ), within 1 s over the last two days of them. An hour leaves
// room for a service that was slow to publish.
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
	args := []any{q.UserID, q.UserID, q.Symbol, q.Symbol, q.Status, q.Status, q.Side, q.Side, q.OrderID, q.OrderID, q.Accounts, botList(q.Bots), q.Accounts}
	args = append(args, kindArgs(q.ByKind)...)
	args = append(args, from, to, from-orderKeyLead.Milliseconds(), to, pc.on, pc.at.UnixMilli(), pc.id, pc.at.UnixMilli(), pc.limit+1)
	rows, err := r.Conn.Query(kindCtx(ctx, q.ByKind), `SELECT toString(order_id), client_order_id, toString(user_id), symbol, side, type, time_in_force,
		price, quantity, quote_amount, status, filled_quantity, filled_quote, reason, created_at, updated_at FROM orders_current
		WHERE (? = '' OR toString(user_id) = ?) AND (? = '' OR symbol = ?) AND (? = '' OR status = ?) AND (? = '' OR side = ?)
		AND (? = '' OR toString(order_id) = ?) AND (? = '' OR has(?, toString(user_id)) = (? = 'bots'))
		AND (NOT ? OR toString(user_id) IN ?) AND (NOT ? OR toString(user_id) NOT IN ?)
		AND created_at >= `+ms+` AND created_at < `+ms+` AND created_key >= `+ms+` AND created_key < `+ms+`
		AND (NOT ? OR ((created_at, toString(order_id)) < (`+ms+`, ?) AND created_key <= `+ms+`))
		ORDER BY created_at DESC, toString(order_id) DESC LIMIT ?`, args...)
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
	// A kind keeps a trade when one of its sides is of it: a side kept
	// (Only), or a side not left out (Except).
	only, except := q.ByKind.Only, q.ByKind.Except
	rows, err := r.Conn.Query(kindCtx(ctx, q.ByKind), `SELECT toString(trade_id), symbol, trade_number, price, quantity, quote_quantity, taker_side,
		toString(buyer_user_id), toString(buyer_order_id), toString(seller_user_id), toString(seller_order_id), buyer_is_maker,
		buyer_fee, seller_fee, executed_at, house_side, settle_asset FROM trades FINAL
		WHERE (? = '' OR symbol = ?) AND (? = '' OR toString(buyer_user_id) = ? OR toString(seller_user_id) = ?)
		AND (? = '' OR (has(?, toString(buyer_user_id)) AND has(?, toString(seller_user_id))) = (? = 'bots'))
		AND (NOT ? OR toString(buyer_user_id) IN ? OR toString(seller_user_id) IN ?)
		AND (NOT ? OR NOT (toString(buyer_user_id) IN ? AND toString(seller_user_id) IN ?))
		AND executed_at >= `+ms+` AND executed_at < `+ms+` AND (NOT ? OR (executed_at, toString(trade_id)) < (`+ms+`, ?))
		ORDER BY executed_at DESC, toString(trade_id) DESC LIMIT ?`,
		q.Symbol, q.Symbol, q.UserID, q.UserID, q.UserID, q.Accounts, botList(q.Bots), botList(q.Bots), q.Accounts,
		only != nil, idSet(only), idSet(only), except != nil, idSet(except), idSet(except),
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
			&t.SellerUserID, &t.SellerOrderID, &t.BuyerIsMaker, &v[3], &v[4], &t.ExecutedAt, &t.HouseSide, &t.SettleAsset); err != nil {
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
	args := []any{q.UserID, q.UserID, q.Asset, q.Asset, q.Network, q.Network, q.Status, q.Status, q.TxHash, q.TxHash}
	args = append(args, kindArgs(q.ByKind)...)
	args = append(args, pc.on, pc.id, pc.limit+1)
	rows, err := r.Conn.Query(kindCtx(ctx, q.ByKind), `SELECT toString(deposit_id), user_id, asset, network, kind, address, tx_hash, amount, status, unclaimed,
		reason, confirmations, required_confirmations, updated_at FROM wallet_deposits FINAL
		WHERE (? = '' OR user_id = ?) AND (? = '' OR asset = ?) AND (? = '' OR network = ?) AND (? = '' OR status = ?)
		AND (? = '' OR lower(tx_hash) = lower(?)) AND (NOT ? OR user_id IN ?) AND (NOT ? OR user_id NOT IN ?)
		AND (NOT ? OR toString(deposit_id) < ?)
		ORDER BY toString(deposit_id) DESC LIMIT ?`, args...)
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

// Activity sums the overview's figures of the accounts k keeps (L1): the
// last 24 hours' trades (those with a side of theirs), traders and
// turnover, the deposits and withdrawals waiting, the risk events (a
// symbol's too), and per UTC day for the last days the trades and the
// USDT turnover; and apart, the other kinds' trades and traders.
func (r Records) Activity(ctx context.Context, days int, k ports.ActivityKinds) (ports.Activity, error) {
	out := ports.Activity{
		TradesByDay: map[string]uint64{}, TurnoverUSDTByDay: map[string]string{},
		OtherTrades24h: map[string]uint64{}, OtherTraders24h: map[string]uint64{},
	}
	if k.Keep.On() || len(k.Others) > 0 {
		ctx = longQuery(ctx)
	}
	since := time.Now().Add(-24 * time.Hour).UnixMilli()
	kept, keptArgs := kindsCond("b", "s", k.Keep)
	test, bot := idSet(k.Others[kindTest]), idSet(k.Others[kindBot])
	var testTrades, botTrades uint64
	args := append(append([]any{}, keptArgs...), test, test, bot, bot, since)
	if err := r.Conn.QueryRow(ctx, `SELECT countIf(kept), countIf(NOT kept AND test), countIf(NOT kept AND NOT test AND bot) FROM (
			SELECT `+kept+` AS kept, (b IN ? OR s IN ?) AS test, (b IN ? OR s IN ?) AS bot FROM (
				SELECT toString(buyer_user_id) AS b, toString(seller_user_id) AS s FROM trades FINAL WHERE executed_at >= `+ms+`))`, args...).
		Scan(&out.Trades24h, &testTrades, &botTrades); err != nil {
		return out, unavailable(err)
	}
	keptTrader, traderArgs := kindCond("u", k.Keep)
	system := idSet(k.Others[kindSystem])
	var traders [3]uint64
	args = append(append([]any{}, traderArgs...), bot, test, system, since, since)
	if err := r.Conn.QueryRow(ctx, `SELECT uniqExactIf(u, `+keptTrader+`), uniqExactIf(u, u IN ?), uniqExactIf(u, u IN ?), uniqExactIf(u, u IN ?)
		FROM (SELECT toString(buyer_user_id) AS u FROM trades FINAL WHERE executed_at >= `+ms+`
			UNION ALL SELECT toString(seller_user_id) AS u FROM trades FINAL WHERE executed_at >= `+ms+`)`, args...).
		Scan(&out.ActiveTraders24h, &traders[0], &traders[1], &traders[2]); err != nil {
		return out, unavailable(err)
	}
	if len(k.Others) > 0 {
		out.OtherTrades24h[kindTest], out.OtherTrades24h[kindBot] = testTrades, botTrades
		out.OtherTraders24h[kindBot], out.OtherTraders24h[kindTest], out.OtherTraders24h[kindSystem] = traders[0], traders[1], traders[2]
	}
	keptTrade, tradeArgs := kindsCond("buyer_user_id", "seller_user_id", k.Keep)
	rows, err := r.Conn.Query(ctx, `SELECT quote_asset, sum(quote_quantity) FROM trades FINAL WHERE executed_at >= `+ms+` AND `+keptTrade+`
		GROUP BY quote_asset ORDER BY quote_asset`, append([]any{since}, tradeArgs...)...)
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
	deposit, depositArgs := kindCond("user_id", k.Keep)
	withdrawal, withdrawalArgs := kindCond("user_id", k.Keep)
	risk, riskArgs := kindCond("aggregate_id", k.Keep)
	args = append(append(append(depositArgs, withdrawalArgs...), since), riskArgs...)
	if err := r.Conn.QueryRow(ctx, `SELECT
			(SELECT count() FROM wallet_deposits FINAL WHERE status IN ('DETECTED', 'CONFIRMING') AND `+deposit+`),
			(SELECT count() FROM wallet_withdrawals FINAL WHERE status = 'PENDING_REVIEW' AND `+withdrawal+`),
			(SELECT count() FROM events WHERE topic = 'risk.events' AND occurred_at >= `+ms+`
				AND (aggregate_type != 'user' OR `+risk+`))`, args...).
		Scan(&out.PendingDeposits, &out.PendingWithdraws, &out.RiskEvents24h); err != nil {
		return out, unavailable(err)
	}
	if days <= 0 {
		return out, nil
	}
	daily, err := r.Conn.Query(ctx, `SELECT toString(toDate(executed_at)) AS day, count(), sumIf(quote_quantity, quote_asset = 'USDT')
		FROM trades FINAL WHERE executed_at >= toDateTime64(today() - ?, 3, 'UTC') AND `+keptTrade+` GROUP BY day`,
		append([]any{days - 1}, tradeArgs...)...)
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
