package analytics

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	derivativesv1 "github.com/skill/exchange/api/gen/go/exchange/derivatives/v1"
	eventv1 "github.com/skill/exchange/api/gen/go/exchange/event/v1"
	marginv1 "github.com/skill/exchange/api/gen/go/exchange/margin/v1"
	marketv1 "github.com/skill/exchange/api/gen/go/exchange/market/v1"
	orderv1 "github.com/skill/exchange/api/gen/go/exchange/order/v1"
	tradev1 "github.com/skill/exchange/api/gen/go/exchange/trade/v1"
	walletv1 "github.com/skill/exchange/api/gen/go/exchange/wallet/v1"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/kafka"
)

// Read models of trading and the wallet (implementation plan §6.3 task
// 12; tables in migrations/clickhouse/00004_read_models.sql): typed rows
// projected from order.events, trade.events and wallet.*.events, and
// one-minute candles recomputed from trades. Contract orders and trades
// join them; positions, fills, funding and liquidations of contracts
// have tables of their own (§7.3 task 10, 00005_derivatives_read_models.sql),
// and so have margin trading's interest and liquidations (margin design
// 2026-10-06 §5.3, 00009_margin_read_models.sql).

// ReadModelTopics feed the read models (the flat minutes too: kept in
// events, a backfill writes them again).
var ReadModelTopics = []string{
	event.TopicOrder, event.TopicTrade, event.TopicWalletDeposit, event.TopicWalletWithdrawal,
	event.TopicDerivOrder, event.TopicDerivTrade, event.TopicDerivPosition, event.TopicDerivLiquidation,
	event.TopicMarketCandleFlats, event.TopicMargin,
}

const (
	insertTrades = `INSERT INTO trades (trade_id, symbol, base_asset, quote_asset, trade_number, sequence, price, quantity,
		quote_quantity, taker_side, buyer_order_id, buyer_user_id, seller_order_id, seller_user_id, buyer_is_maker, buyer_fee,
		seller_fee, house_side, executed_at)`
	insertOrders = `INSERT INTO orders (order_id, client_order_id, user_id, symbol, side, type, time_in_force, price, quantity,
		quote_amount, frozen_asset, frozen_amount, accepted_at)`
	insertOrderUpdates = `INSERT INTO order_updates (order_id, user_id, symbol, sequence, status, filled_quantity, filled_quote,
		trade_id, reason, event_id, occurred_at)`
	insertDeposits = `INSERT INTO wallet_deposits (deposit_id, user_id, asset, network, kind, address, tx_hash, log_index,
		block_number, amount, status, unclaimed, reason, confirmations, required_confirmations, journal_id, updated_at, version)`
	insertWithdrawals = `INSERT INTO wallet_withdrawals (withdrawal_id, user_id, asset, network, address, amount, fee, status,
		internal, tx_hash, confirmations, required_confirmations, risk_reasons, reject_reason, updated_at, version)`
	insertPositions = `INSERT INTO derivatives_positions (position_id, user_id, symbol, position_side, quantity, entry_price,
		entry_cost, margin, margin_mode, leverage, realized_pnl, funding, updated_at, version)`
	insertDerivFills = `INSERT INTO derivatives_fills (trade_id, order_id, user_id, symbol, side, position_side, maker, price,
		quantity, notional, closed_quantity, fee, realized_pnl, liquidation, executed_at)`
	insertFunding = `INSERT INTO derivatives_funding (position_id, user_id, symbol, position_side, margin_mode, funding_time,
		funding_rate, mark_price, amount, settled_at)`
	insertLiquidations = `INSERT INTO derivatives_liquidations (event_id, kind, user_id, symbol, position_side, cross_margin, adl,
		trade_id, price, quantity, realized_pnl, insurance_paid, mark_price, bankruptcy_price, margin_balance, maintenance_margin,
		occurred_at)`
	insertFlats          = `INSERT INTO candles_1m (symbol, open_time, open, high, low, close, volume, quote_volume, trades, updated_at)`
	insertMarginInterest = `INSERT INTO margin_interest (interest_id, user_id, account_type, symbol, asset, principal, interest_model,
		hourly_rate, interest, interest_owed, hour, journal_id)`
	insertMarginLiquidations = `INSERT INTO margin_liquidations (liquidation_id, user_id, account_type, symbol, margin_level,
		total_asset, total_liability, started_at, repaid, fee, insurance_covered, remaining, completed_at)`
	// refreshCandles rewrites the one-minute candles of a symbol between
	// two minutes from all its trades there.
	refreshCandles = `INSERT INTO candles_1m (symbol, open_time, open, high, low, close, volume, quote_volume, trades, updated_at)
		SELECT symbol, toStartOfMinute(executed_at) AS minute, argMin(price, sequence), max(price), min(price),
			argMax(price, sequence), sum(quantity), sum(quote_quantity), toUInt32(count()), now64(3)
		FROM trades FINAL
		WHERE symbol = ? AND executed_at >= toDateTime64(?, 3, 'UTC') AND executed_at < toDateTime64(?, 3, 'UTC')
		GROUP BY symbol, minute`
)

// Order statuses as the trading service names them.
const (
	statusNew             = "NEW"
	statusOpen            = "OPEN"
	statusPartiallyFilled = "PARTIALLY_FILLED"
	statusFilled          = "FILLED"
	statusCanceled        = "CANCELED"
	statusRejected        = "REJECTED"
)

// depositRank and withdrawalRank order statuses reported in the same
// millisecond (a withdrawal's request and its risk score are one
// transaction), so the later status wins.
var (
	depositRank = map[string]uint64{
		"DETECTED": 1, "ORPHANED": 2, "CONFIRMING": 3, "CONFIRMED": 4, "CREDITED": 5, "REJECTED": 5,
	}
	withdrawalRank = map[string]uint64{
		"REQUESTED": 1, "PENDING_REVIEW": 2, "APPROVED": 3, "SIGNING": 4, "SUBMITTED": 4, "BROADCAST": 5, "CONFIRMING": 6,
		"CONFIRMED": 7, "REJECTED": 7, "CANCELED": 7, "FAILED": 7,
	}
)

// version orders the snapshots of a deposit or withdrawal: the event time
// in milliseconds, then the status's progress.
func version(at time.Time, rank uint64) uint64 {
	return uint64(at.UnixMilli())*16 + rank //nolint:gosec // event times are after 1970
}

// errMalformed marks an event whose fields do not fit its table.
var errMalformed = errors.New("malformed event")

func id(s string) (uuid.UUID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: id %q", errMalformed, s)
	}
	return u, nil
}

// orderID parses a trade side's order ID: HOUSE's side has none (ADR-0015)
// and gets the nil UUID.
func orderID(s string) (uuid.UUID, error) {
	if s == "" {
		return uuid.Nil, nil
	}
	return id(s)
}

// amount parses a decimal string; empty is zero.
func amount(s string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, nil
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%w: amount %q", errMalformed, s)
	}
	return d, nil
}

// optional parses a decimal string; empty is NULL.
func optional(s string) (*decimal.Decimal, error) {
	if s == "" {
		return nil, nil
	}
	d, err := amount(s)
	return &d, err
}

// enum names a proto enum value without its prefix: SIDE_BUY is BUY.
func enum(name, prefix string) string { return strings.TrimPrefix(name, prefix) }

// span is the time range of a symbol's trades in a batch.
type span struct{ from, to time.Time }

// readModels holds the rows of one batch.
type readModels struct {
	trades, orders, updates, deposits, withdrawals [][]any
	positions, fills, funding, liquidations        [][]any
	flats, marginInterest, marginLiquidations      [][]any
	touched                                        map[string]span
}

// add projects one delivery; it reports malformed events, which are
// skipped. Events the read models do not use are ignored.
func (m *readModels) add(d kafka.Delivery) error {
	switch d.Topic {
	case event.TopicTrade, event.TopicOrder, event.TopicWalletDeposit, event.TopicWalletWithdrawal, event.TopicDerivOrder,
		event.TopicDerivTrade, event.TopicDerivPosition, event.TopicDerivLiquidation, event.TopicMarketCandleFlats, event.TopicMargin:
	default:
		return nil
	}
	env := d.Envelope
	msg, err := env.GetPayload().UnmarshalNew()
	if err != nil {
		return fmt.Errorf("%w: payload: %w", errMalformed, err)
	}
	at := env.GetOccurredAt().AsTime()
	if d.Topic == event.TopicDerivPosition || d.Topic == event.TopicDerivLiquidation {
		return m.addDerivatives(msg, env.GetEventId(), at)
	}
	if d.Topic == event.TopicMargin {
		return m.addMargin(msg)
	}
	switch e := msg.(type) {
	case *tradev1.TradeExecuted:
		return m.addTrade(e, at)
	case *marketv1.CandleClosed:
		return m.addFlat(e.GetCandle())
	case *orderv1.OrderAccepted, *orderv1.OrderRejected, *orderv1.OrderOpened, *orderv1.OrderPartiallyFilled, *orderv1.OrderFilled,
		*orderv1.OrderCanceled:
		return m.addOrderEvent(e, env.GetEventId(), at)
	case interface{ GetDeposit() *walletv1.Deposit }:
		journal := ""
		if c, ok := e.(*walletv1.DepositCredited); ok {
			journal = c.GetJournalId()
		}
		return m.addDeposit(e.GetDeposit(), journal, at)
	case interface{ GetWithdrawal() *walletv1.Withdrawal }:
		return m.addWithdrawal(e.GetWithdrawal(), at)
	}
	return nil
}

// addFlat stores a minute market-data-service made flat (no trade in it,
// coordinator 2026-10-04) as its candles_1m row, updated at the minute's
// start: a candle computed from trades of that minute (one an engine whose
// clock was behind stamped in it) is always newer and replaces it.
func (m *readModels) addFlat(c *marketv1.Candle) error {
	if c.GetInterval() != "1m" || c.GetTradeCount() != 0 || c.GetSymbol() == "" || c.GetOpenTime() == nil {
		return fmt.Errorf("%w: not a flat minute: %s %s", errMalformed, c.GetSymbol(), c.GetInterval())
	}
	var prices [4]decimal.Decimal
	for i, s := range []string{c.GetOpen(), c.GetHigh(), c.GetLow(), c.GetClose()} {
		p, err := amount(s)
		if err != nil {
			return err
		}
		prices[i] = p
	}
	open := c.GetOpenTime().AsTime().UTC()
	m.flats = append(m.flats, []any{
		c.GetSymbol(), open, prices[0], prices[1], prices[2], prices[3], decimal.Zero, decimal.Zero, uint32(0), open,
	})
	return nil
}

// houseSide is the side HOUSE took in a trade, "" between users.
func houseSide(t *tradev1.TradeExecuted) string {
	if t.GetHouseSide() == orderv1.Side_SIDE_UNSPECIFIED {
		return ""
	}
	return enum(t.GetHouseSide().String(), "SIDE_")
}

func (m *readModels) addTrade(t *tradev1.TradeExecuted, at time.Time) error {
	var errs []error
	check := func(err error) { errs = append(errs, err) }
	tradeID, err := id(t.GetTradeId())
	check(err)
	buyerOrder, err := orderID(t.GetBuyerOrderId())
	check(err)
	buyer, err := id(t.GetBuyerUserId())
	check(err)
	sellerOrder, err := orderID(t.GetSellerOrderId())
	check(err)
	seller, err := id(t.GetSellerUserId())
	check(err)
	price, err := amount(t.GetPrice())
	check(err)
	qty, err := amount(t.GetQuantity())
	check(err)
	quote, err := amount(t.GetQuoteQuantity())
	check(err)
	buyerFee, err := amount(t.GetBuyerFee())
	check(err)
	sellerFee, err := amount(t.GetSellerFee())
	check(err)
	if err := errors.Join(errs...); err != nil {
		return err
	}
	m.trades = append(m.trades, []any{
		tradeID, t.GetSymbol(), t.GetBaseAsset(), t.GetQuoteAsset(), t.GetTradeNumber(), t.GetSequence(), price, qty, quote,
		enum(t.GetTakerSide().String(), "SIDE_"), buyerOrder, buyer, sellerOrder, seller, t.GetBuyerIsMaker(), buyerFee, sellerFee,
		houseSide(t), at,
	})
	if m.touched == nil {
		m.touched = map[string]span{}
	}
	s, ok := m.touched[t.GetSymbol()]
	if !ok || at.Before(s.from) {
		s.from = at
	}
	if !ok || at.After(s.to) {
		s.to = at
	}
	m.touched[t.GetSymbol()] = s
	return nil
}

// orderUpdate is the state an order event reports.
type orderUpdate struct {
	orderID, userID, symbol, status, tradeID, reason string
	sequence                                         int64
	filled, filledQuote                              string
}

func (m *readModels) addOrderEvent(msg proto.Message, eventID string, at time.Time) error {
	var u orderUpdate
	switch e := msg.(type) {
	case *orderv1.OrderAccepted:
		o := e.GetOrder()
		if err := m.addOrder(o, e, at); err != nil {
			return err
		}
		u = orderUpdate{orderID: o.GetOrderId(), userID: o.GetUserId(), symbol: o.GetSymbol(), status: statusNew}
	case *orderv1.OrderRejected:
		u = orderUpdate{
			orderID: e.GetOrderId(), userID: e.GetUserId(), symbol: e.GetSymbol(), status: statusRejected, sequence: e.GetSequence(),
			reason: e.GetReasonCode(),
		}
	case *orderv1.OrderOpened:
		u = orderUpdate{orderID: e.GetOrderId(), userID: e.GetUserId(), symbol: e.GetSymbol(), status: statusOpen, sequence: e.GetSequence()}
	case *orderv1.OrderPartiallyFilled:
		u = orderUpdate{
			orderID: e.GetOrderId(), userID: e.GetUserId(), symbol: e.GetSymbol(), status: statusPartiallyFilled, sequence: e.GetSequence(),
			tradeID: e.GetTradeId(), filled: e.GetFilledQuantity(), filledQuote: e.GetFilledQuote(),
		}
	case *orderv1.OrderFilled:
		u = orderUpdate{
			orderID: e.GetOrderId(), userID: e.GetUserId(), symbol: e.GetSymbol(), status: statusFilled, sequence: e.GetSequence(),
			tradeID: e.GetTradeId(), filled: e.GetFilledQuantity(), filledQuote: e.GetFilledQuote(),
		}
	case *orderv1.OrderCanceled:
		u = orderUpdate{
			orderID: e.GetOrderId(), userID: e.GetUserId(), symbol: e.GetSymbol(), status: statusCanceled, sequence: e.GetSequence(),
			reason: e.GetReason(), filled: e.GetFilledQuantity(), filledQuote: e.GetFilledQuote(),
		}
	}
	orderID, err := id(u.orderID)
	userID, err2 := id(u.userID)
	eid, err3 := id(eventID)
	filled, err4 := amount(u.filled)
	filledQuote, err5 := amount(u.filledQuote)
	if err := errors.Join(err, err2, err3, err4, err5); err != nil {
		return err
	}
	m.updates = append(m.updates, []any{orderID, userID, u.symbol, u.sequence, u.status, filled, filledQuote, u.tradeID, u.reason, eid, at})
	return nil
}

func (m *readModels) addOrder(o *orderv1.Order, e *orderv1.OrderAccepted, at time.Time) error {
	orderID, err := id(o.GetOrderId())
	userID, err2 := id(o.GetUserId())
	price, err3 := optional(o.GetPrice())
	qty, err4 := optional(o.GetQuantity())
	quote, err5 := optional(o.GetQuoteAmount())
	frozen, err6 := amount(e.GetFrozenAmount())
	if err := errors.Join(err, err2, err3, err4, err5, err6); err != nil {
		return err
	}
	m.orders = append(m.orders, []any{
		orderID, o.GetClientOrderId(), userID, o.GetSymbol(), enum(o.GetSide().String(), "SIDE_"),
		enum(o.GetType().String(), "ORDER_TYPE_"), enum(o.GetTimeInForce().String(), "TIME_IN_FORCE_"), price, qty, quote,
		e.GetFrozenAsset(), frozen, at,
	})
	return nil
}

func (m *readModels) addDeposit(d *walletv1.Deposit, journal string, at time.Time) error {
	depositID, err := id(d.GetDepositId())
	amt, err2 := amount(d.GetAmount())
	if err := errors.Join(err, err2); err != nil {
		return err
	}
	m.deposits = append(m.deposits, []any{
		depositID, d.GetUserId(), d.GetAsset(), d.GetNetwork(), d.GetKind(), d.GetAddress(), d.GetTxHash(), d.GetLogIndex(),
		d.GetBlockNumber(), amt, d.GetStatus(), d.GetUnclaimed(), d.GetReason(), d.GetConfirmations(), d.GetRequiredConfirmations(),
		journal, at, version(at, depositRank[d.GetStatus()]),
	})
	return nil
}

func (m *readModels) addWithdrawal(w *walletv1.Withdrawal, at time.Time) error {
	withdrawalID, err := id(w.GetWithdrawalId())
	amt, err2 := amount(w.GetAmount())
	fee, err3 := amount(w.GetFee())
	if err := errors.Join(err, err2, err3); err != nil {
		return err
	}
	reasons := w.GetRiskReasons()
	if reasons == nil {
		reasons = []string{}
	}
	m.withdrawals = append(m.withdrawals, []any{
		withdrawalID, w.GetUserId(), w.GetAsset(), w.GetNetwork(), w.GetAddress(), amt, fee, w.GetStatus(), w.GetInternal(),
		w.GetTxHash(), w.GetConfirmations(), w.GetRequiredConfirmations(), reasons, w.GetRejectReason(), at,
		version(at, withdrawalRank[w.GetStatus()]),
	})
	return nil
}

// positionEvent is an event that carries a position snapshot.
type positionEvent interface {
	GetPosition() *derivativesv1.Position
}

// addDerivatives projects a position or liquidation event of
// derivatives-service: the position snapshot it carries, and the fill,
// funding payment or liquidation step.
func (m *readModels) addDerivatives(msg proto.Message, eventID string, at time.Time) error {
	if e, ok := msg.(positionEvent); ok && e.GetPosition() != nil {
		if err := m.addPosition(e.GetPosition(), at); err != nil {
			return err
		}
	}
	switch e := msg.(type) {
	case *derivativesv1.FillSettled:
		return m.addDerivFill(e)
	case *derivativesv1.FundingPaid:
		return m.addFunding(e, at)
	case *derivativesv1.LiquidationWarning:
		return m.addLiquidation(liquidationStep{
			kind: "WARNING", userID: e.GetUserId(), symbol: e.GetSymbol(), positionSide: e.GetPositionSide(), cross: e.GetCross(),
			marginBalance: e.GetMarginBalance(), maintenanceMargin: e.GetMaintenanceMargin(),
		}, eventID, at)
	case *derivativesv1.LiquidationStarted:
		p := e.GetPosition()
		return m.addLiquidation(liquidationStep{
			kind: "STARTED", userID: p.GetUserId(), symbol: p.GetSymbol(), positionSide: p.GetPositionSide(), cross: e.GetCross(),
			quantity: p.GetQuantity(), markPrice: e.GetMarkPrice(), bankruptcyPrice: e.GetBankruptcyPrice(),
			marginBalance: e.GetMarginBalance(), maintenanceMargin: e.GetMaintenanceMargin(),
		}, eventID, at)
	case *derivativesv1.LiquidationFilled:
		return m.addLiquidation(liquidationStep{
			kind: "FILLED", userID: e.GetUserId(), symbol: e.GetSymbol(), positionSide: e.GetPositionSide(), adl: e.GetAdl(),
			tradeID: e.GetTradeId(), price: e.GetPrice(), quantity: e.GetQuantity(), realizedPnL: e.GetRealizedPnl(),
			insurancePaid: e.GetInsurancePaid(),
		}, eventID, at)
	case *derivativesv1.AdlExecuted:
		return m.addLiquidation(liquidationStep{
			kind: "ADL", userID: e.GetUserId(), symbol: e.GetSymbol(), positionSide: e.GetPositionSide(), adl: true,
			tradeID: e.GetTradeId(), price: e.GetPrice(), quantity: e.GetQuantity(), realizedPnL: e.GetRealizedPnl(),
		}, eventID, at)
	}
	return nil
}

func (m *readModels) addPosition(p *derivativesv1.Position, at time.Time) error {
	positionID, err := id(p.GetPositionId())
	userID, err2 := id(p.GetUserId())
	qty, err3 := amount(p.GetQuantity())
	entryPrice, err4 := optional(p.GetEntryPrice())
	entryCost, err5 := amount(p.GetEntryCost())
	margin, err6 := amount(p.GetMargin())
	pnl, err7 := amount(p.GetRealizedPnl())
	funding, err8 := amount(p.GetFunding())
	if err := errors.Join(err, err2, err3, err4, err5, err6, err7, err8); err != nil {
		return err
	}
	if p.GetVersion() < 0 {
		return fmt.Errorf("%w: position version %d", errMalformed, p.GetVersion())
	}
	m.positions = append(m.positions, []any{
		positionID, userID, p.GetSymbol(), p.GetPositionSide(), qty, entryPrice, entryCost, margin, p.GetMarginMode(), p.GetLeverage(),
		pnl, funding, at, uint64(p.GetVersion()), //nolint:gosec // checked non-negative above
	})
	return nil
}

func (m *readModels) addDerivFill(f *derivativesv1.FillSettled) error {
	var errs []error
	check := func(err error) { errs = append(errs, err) }
	tradeID, err := id(f.GetTradeId())
	check(err)
	orderID, err := id(f.GetOrderId())
	check(err)
	userID, err := id(f.GetUserId())
	check(err)
	price, err := amount(f.GetPrice())
	check(err)
	qty, err := amount(f.GetQuantity())
	check(err)
	closed, err := amount(f.GetClosedQuantity())
	check(err)
	fee, err := amount(f.GetFee())
	check(err)
	pnl, err := amount(f.GetRealizedPnl())
	check(err)
	if err := errors.Join(errs...); err != nil {
		return err
	}
	m.fills = append(m.fills, []any{
		tradeID, orderID, userID, f.GetSymbol(), f.GetSide(), f.GetPositionSide(), f.GetMaker(), price, qty, price.Mul(qty), closed, fee,
		pnl, f.GetLiquidation(), f.GetExecutedAt().AsTime(),
	})
	return nil
}

func (m *readModels) addFunding(e *derivativesv1.FundingPaid, at time.Time) error {
	p := e.GetPosition()
	positionID, err := id(p.GetPositionId())
	userID, err2 := id(p.GetUserId())
	rate, err3 := amount(e.GetFundingRate())
	mark, err4 := amount(e.GetMarkPrice())
	amt, err5 := amount(e.GetAmount())
	if err := errors.Join(err, err2, err3, err4, err5); err != nil {
		return err
	}
	m.funding = append(m.funding, []any{
		positionID, userID, p.GetSymbol(), p.GetPositionSide(), p.GetMarginMode(), e.GetFundingTime().AsTime(), rate, mark, amt, at,
	})
	return nil
}

// liquidationStep is a row of derivatives_liquidations; empty amounts are
// 0.
type liquidationStep struct {
	kind, userID, symbol, positionSide, tradeID                  string
	cross, adl                                                   bool
	price, quantity, realizedPnL, insurancePaid                  string
	markPrice, bankruptcyPrice, marginBalance, maintenanceMargin string
}

func (m *readModels) addLiquidation(l liquidationStep, eventID string, at time.Time) error {
	eid, err := id(eventID)
	userID, err2 := id(l.userID)
	errs := []error{err, err2}
	amounts := make([]any, 0, 8)
	for _, s := range []string{
		l.price, l.quantity, l.realizedPnL, l.insurancePaid, l.markPrice, l.bankruptcyPrice, l.marginBalance,
		l.maintenanceMargin,
	} {
		v, err := amount(s)
		errs = append(errs, err)
		amounts = append(amounts, v)
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	row := []any{eid, l.kind, userID, l.symbol, l.positionSide, l.cross, l.adl, l.tradeID}
	m.liquidations = append(m.liquidations, append(append(row, amounts...), at))
	return nil
}

// addMargin projects margin.events: an hour's interest, and the start and
// the end of a liquidation (each sets its own columns of the liquidation's
// row; NULL leaves a column to the other event; the end repeats the
// start's level, values and time, which an end published before it did
// leaves NULL). Borrows and repayments have no read model yet.
func (m *readModels) addMargin(msg proto.Message) error {
	switch e := msg.(type) {
	case *marginv1.MarginInterestAccrued:
		interestID, err := id(e.GetInterestId())
		userID, err2 := id(e.GetUserId())
		principal, err3 := amount(e.GetPrincipal())
		rate, err4 := amount(e.GetHourlyRate())
		interest, err5 := amount(e.GetInterest())
		owed, err6 := amount(e.GetInterestOwed())
		if err := errors.Join(err, err2, err3, err4, err5, err6); err != nil {
			return err
		}
		m.marginInterest = append(m.marginInterest, []any{
			interestID, userID, e.GetAccountType(), e.GetSymbol(), e.GetAsset(), principal, e.GetInterestModel(), rate, interest, owed,
			e.GetHour().AsTime(), e.GetJournalId(),
		})
	case *marginv1.MarginLiquidationStarted:
		liquidationID, err := id(e.GetLiquidationId())
		userID, err2 := id(e.GetUserId())
		level, err3 := optional(e.GetMarginLevel())
		assets, err4 := optional(e.GetTotalAsset())
		liabilities, err5 := optional(e.GetTotalLiability())
		if err := errors.Join(err, err2, err3, err4, err5); err != nil {
			return err
		}
		started := e.GetStartedAt().AsTime()
		m.marginLiquidations = append(m.marginLiquidations, []any{
			liquidationID, &userID, ptr(e.GetAccountType()), ptr(e.GetSymbol()), level, assets, liabilities, &started,
			nil, nil, nil, nil, nil,
		})
	case *marginv1.MarginLiquidationCompleted:
		liquidationID, err := id(e.GetLiquidationId())
		userID, err2 := id(e.GetUserId())
		fee, err3 := optional(e.GetFee())
		insurance, err4 := optional(e.GetInsuranceCovered())
		repaid, err5 := assetAmounts(e.GetRepaid())
		remaining, err6 := assetAmounts(e.GetRemaining())
		level, err7 := optional(e.GetMarginLevel())
		assets, err8 := optional(e.GetTotalAsset())
		liabilities, err9 := optional(e.GetTotalLiability())
		if err := errors.Join(err, err2, err3, err4, err5, err6, err7, err8, err9); err != nil {
			return err
		}
		var started *time.Time
		if e.GetStartedAt() != nil {
			at := e.GetStartedAt().AsTime()
			started = &at
		}
		completed := e.GetCompletedAt().AsTime()
		m.marginLiquidations = append(m.marginLiquidations, []any{
			liquidationID, &userID, ptr(e.GetAccountType()), ptr(e.GetSymbol()), level, assets, liabilities, started,
			&repaid, fee, insurance, &remaining, &completed,
		})
	}
	return nil
}

// ptr is a string column's value; an isolated account's symbol, none for
// the cross account, is still a value (the empty string), not NULL.
func ptr(s string) *string { return &s }

// assetAmounts writes amounts of assets as a JSON array of
// {"asset", "amount"}, checking each amount.
func assetAmounts(list []*marginv1.AssetAmount) (string, error) {
	type item struct {
		Asset  string `json:"asset"`
		Amount string `json:"amount"`
	}
	out := make([]item, 0, len(list))
	for _, a := range list {
		if _, err := amount(a.GetAmount()); err != nil {
			return "", err
		}
		out = append(out, item{Asset: a.GetAsset(), Amount: a.GetAmount()})
	}
	b, err := json.Marshal(out)
	return string(b), err
}

// storeReadModels writes a batch's rows, then recomputes the candles of the
// minutes its trades fell in.
func (in *Ingestor) storeReadModels(ctx context.Context, batch []kafka.Delivery) error {
	var m readModels
	for _, d := range batch {
		if err := m.add(d); err != nil {
			in.rejected.Inc()
			in.log.WarnContext(ctx, "read model row dropped", "topic", d.Topic, "event_id", d.Envelope.GetEventId(), "error", err)
		}
	}
	for _, t := range []struct {
		insert string
		rows   [][]any
	}{
		{insertTrades, m.trades},
		{insertOrders, m.orders},
		{insertOrderUpdates, m.updates},
		{insertDeposits, m.deposits},
		{insertWithdrawals, m.withdrawals},
		{insertPositions, m.positions},
		{insertDerivFills, m.fills},
		{insertFunding, m.funding},
		{insertLiquidations, m.liquidations},
		{insertFlats, m.flats},
		{insertMarginInterest, m.marginInterest},
		{insertMarginLiquidations, m.marginLiquidations},
	} {
		if err := in.insert(ctx, t.insert, t.rows); err != nil {
			return err
		}
	}
	const minute = "2006-01-02 15:04:00.000"
	for symbol, s := range m.touched {
		from, to := s.from.UTC().Truncate(time.Minute), s.to.UTC().Truncate(time.Minute).Add(time.Minute)
		if err := in.conn.Exec(ctx, refreshCandles, symbol, from.Format(minute), to.Format(minute)); err != nil {
			return fmt.Errorf("clickhouse: candles of %s: %w", symbol, err)
		}
	}
	return nil
}

func (in *Ingestor) insert(ctx context.Context, insert string, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	b, err := in.conn.PrepareBatch(ctx, insert)
	if err != nil {
		return fmt.Errorf("clickhouse: prepare read model batch: %w", err)
	}
	for _, r := range rows {
		if err := b.Append(r...); err != nil {
			_ = b.Abort()
			return fmt.Errorf("clickhouse: append read model row: %w", err)
		}
	}
	if err := b.Send(); err != nil {
		return fmt.Errorf("clickhouse: send read model batch: %w", err)
	}
	return nil
}

// backfillName marks the backfill of the read models in
// read_model_backfills; a new read model needs a new name (v2: the
// contract read models).
const backfillName = "read-models-v2"

// BackfillReadModels projects the events stored before the read models
// existed, page by page in event order, once (recorded in
// read_model_backfills). Rows the live consumer writes meanwhile collapse
// with the same rows from here.
func (in *Ingestor) BackfillReadModels(ctx context.Context, pageSize int) error {
	var done uint64
	if err := in.conn.QueryRow(ctx, `SELECT count() FROM read_model_backfills FINAL WHERE name = ?`, backfillName).Scan(&done); err != nil {
		return fmt.Errorf("clickhouse: read backfills: %w", err)
	}
	if done > 0 {
		return nil
	}
	total := 0
	for _, topic := range ReadModelTopics {
		n, err := in.backfill(ctx, topic, pageSize)
		if err != nil {
			return err
		}
		total += n
	}
	if err := in.conn.Exec(ctx, `INSERT INTO read_model_backfills (name, done_at) VALUES (?, now64(3))`, backfillName); err != nil {
		return fmt.Errorf("clickhouse: record backfill: %w", err)
	}
	in.log.InfoContext(ctx, "read models backfilled", "name", backfillName, "events", total)
	return nil
}

func (in *Ingestor) backfill(ctx context.Context, topic string, pageSize int) (int, error) {
	const stamp = "2006-01-02 15:04:05.000"
	after, afterID, total := time.Unix(0, 0).UTC(), uuid.Nil, 0
	for {
		rows, err := in.conn.Query(ctx, `SELECT event_id, occurred_at, payload FROM events FINAL
			WHERE topic = ? AND (occurred_at, event_id) > (toDateTime64(?, 3, 'UTC'), toUUID(?))
			ORDER BY occurred_at, event_id LIMIT ?`, topic, after.Format(stamp), afterID.String(), pageSize)
		if err != nil {
			return total, fmt.Errorf("clickhouse: backfill %s: %w", topic, err)
		}
		var batch []kafka.Delivery
		n := 0
		for rows.Next() {
			var eventID uuid.UUID
			var at time.Time
			var payload string
			if err := rows.Scan(&eventID, &at, &payload); err != nil {
				_ = rows.Close()
				return total, fmt.Errorf("clickhouse: backfill %s: %w", topic, err)
			}
			n++
			after, afterID = at.UTC(), eventID
			p, err := payloadAny(payload)
			if err != nil {
				in.rejected.Inc()
				continue
			}
			batch = append(batch, kafka.Delivery{Topic: topic, Envelope: &eventv1.Envelope{
				EventId: eventID.String(), OccurredAt: timestamppb.New(at), Payload: p,
			}})
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return total, fmt.Errorf("clickhouse: backfill %s: %w", topic, err)
		}
		if err := in.storeReadModels(ctx, batch); err != nil {
			return total, err
		}
		total += n
		if n < pageSize {
			return total, nil
		}
	}
}

// payloadAny turns a stored payload (PayloadJSON) back into the event.
func payloadAny(payload string) (*anypb.Any, error) {
	var raw struct {
		Type string `json:"@type"`
		Raw  string `json:"@raw"`
	}
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return nil, err
	}
	if raw.Raw != "" {
		b, err := base64.StdEncoding.DecodeString(raw.Raw)
		return &anypb.Any{TypeUrl: raw.Type, Value: b}, err
	}
	var a anypb.Any
	if err := protojson.Unmarshal([]byte(payload), &a); err != nil {
		return nil, err
	}
	return &a, nil
}
