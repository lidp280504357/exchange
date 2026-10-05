package analytics_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	derivativesv1 "github.com/skill/exchange/api/gen/go/exchange/derivatives/v1"
	ledgerv1 "github.com/skill/exchange/api/gen/go/exchange/ledger/v1"
	marginv1 "github.com/skill/exchange/api/gen/go/exchange/margin/v1"
	orderv1 "github.com/skill/exchange/api/gen/go/exchange/order/v1"
	tradev1 "github.com/skill/exchange/api/gen/go/exchange/trade/v1"
	walletv1 "github.com/skill/exchange/api/gen/go/exchange/wallet/v1"
	"github.com/skill/exchange/internal/analytics"
	"github.com/skill/exchange/internal/platform/chx"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/kafka"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/migrations"
)

func TestReadModels(t *testing.T) {
	ctx := context.Background()
	chCfg := testenv.ClickHouse(t)
	conn, err := chx.Open(ctx, chCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sqlDB := chx.OpenDB(chCfg)
	defer sqlDB.Close()
	if err := migrate.UpClickHouse(ctx, sqlDB, migrations.ClickHouse(), discard); err != nil {
		t.Fatalf("clickhouse migrations: %v", err)
	}
	in := analytics.NewIngestor(conn, discard, prometheus.NewRegistry())

	f := event.NewFactory("test", "t")
	at := time.Date(2026, 9, 29, 3, 0, 10, 0, time.UTC)
	var batch []kafka.Delivery
	add := func(topic string, msg proto.Message, when time.Time) {
		t.Helper()
		env, err := f.New(ctx, msg, "x", "x", event.WithOccurredAt(when))
		if err != nil {
			t.Fatal(err)
		}
		batch = append(batch, kafka.Delivery{Topic: topic, Envelope: env})
	}
	// Order IDs are UUIDv7, as the trading service makes them: orders_state
	// is keyed and partitioned by their time.
	orderID := func() string { return uuid.Must(uuid.NewV7()).String() }
	buy, sell, buyer, seller := orderID(), orderID(), uuid.NewString(), uuid.NewString()
	add(event.TopicOrder, &orderv1.OrderAccepted{Order: &orderv1.Order{
		OrderId: buy, UserId: buyer, Symbol: "ETH-BTC", Side: orderv1.Side_SIDE_BUY, Type: orderv1.OrderType_ORDER_TYPE_LIMIT,
		TimeInForce: orderv1.TimeInForce_TIME_IN_FORCE_GTC, Price: "0.03", Quantity: "2",
	}, FrozenAsset: "BTC", FrozenAmount: "0.06"}, at)
	add(event.TopicOrder, &orderv1.OrderOpened{OrderId: buy, UserId: buyer, Symbol: "ETH-BTC", Sequence: 5}, at)
	add(event.TopicOrder, &orderv1.OrderAccepted{Order: &orderv1.Order{
		OrderId: sell, UserId: seller, Symbol: "ETH-BTC", Side: orderv1.Side_SIDE_SELL, Type: orderv1.OrderType_ORDER_TYPE_MARKET,
		TimeInForce: orderv1.TimeInForce_TIME_IN_FORCE_IOC, Quantity: "1",
	}, FrozenAsset: "ETH", FrozenAmount: "1"}, at.Add(time.Second))
	for i, price := range []string{"0.03", "0.032", "0.029"} {
		when := at.Add(time.Duration(i*25) * time.Second) // 03:00:10, 03:00:35, 03:01:00
		add(event.TopicTrade, &tradev1.TradeExecuted{
			TradeId: uuid.NewString(), Symbol: "ETH-BTC", BaseAsset: "ETH", QuoteAsset: "BTC", Sequence: int64(10 + i), Price: price,
			Quantity: "1", QuoteQuantity: price, TakerSide: orderv1.Side_SIDE_SELL, BuyerOrderId: buy, BuyerUserId: buyer,
			SellerOrderId: sell, SellerUserId: seller, BuyerIsMaker: true, BuyerFee: "0.001", SellerFee: "0.00003",
			TradeNumber: uint64(i + 1), //nolint:gosec // small
		}, when)
	}
	add(event.TopicOrder, &orderv1.OrderPartiallyFilled{
		OrderId: buy, UserId: buyer, Symbol: "ETH-BTC", Sequence: 12, FilledQuantity: "1", FilledQuote: "0.03",
	}, at.Add(time.Minute))
	add(event.TopicOrder, &orderv1.OrderFilled{
		OrderId: sell, UserId: seller, Symbol: "ETH-BTC", Sequence: 12, FilledQuantity: "1", FilledQuote: "0.03",
	}, at.Add(time.Minute))
	rejected := orderID()
	add(event.TopicOrder, &orderv1.OrderRejected{
		OrderId: rejected, UserId: buyer, Symbol: "ETH-BTC", ReasonCode: "LEDGER_INSUFFICIENT_BALANCE",
	}, at.Add(2*time.Minute))
	dep := uuid.NewString()
	add(event.TopicWalletDeposit, &walletv1.DepositDetected{Deposit: &walletv1.Deposit{
		DepositId: dep, UserId: buyer, Asset: "ETH", Network: "ETH-SEPOLIA", Amount: "0.0012", Status: "DETECTED", LogIndex: -1,
	}}, at)
	add(event.TopicWalletDeposit, &walletv1.DepositCredited{Deposit: &walletv1.Deposit{
		DepositId: dep, UserId: buyer, Asset: "ETH", Network: "ETH-SEPOLIA", Amount: "0.0012", Status: "CREDITED", LogIndex: -1,
		Confirmations: 12, RequiredConfirmations: 12,
	}, JournalId: "j1"}, at.Add(2*time.Minute))
	wd := uuid.NewString()
	for _, status := range []string{"REQUESTED", "PENDING_REVIEW"} { // one transaction, one millisecond
		w := &walletv1.Withdrawal{WithdrawalId: wd, UserId: buyer, Asset: "ETH", Amount: "0.0011", Fee: "0.0002", Status: status}
		if status == "REQUESTED" {
			add(event.TopicWalletWithdrawal, &walletv1.WithdrawalRequested{Withdrawal: w}, at)
		} else {
			add(event.TopicWalletWithdrawal, &walletv1.WithdrawalRiskScored{Withdrawal: w, Score: 100, ApprovalsRequired: 1}, at)
		}
	}
	// Contracts: a trade, the seller's settled fill and position, a
	// funding payment and a liquidation step.
	position := uuid.NewString()
	snapshot := &derivativesv1.Position{
		PositionId: position, UserId: seller, Symbol: "BTC-USDT-PERP", PositionSide: "BOTH", Quantity: "-0.5", EntryPrice: "60000",
		EntryCost: "30000", Margin: "600", MarginMode: "ISOLATED", Leverage: 50, Version: 2,
	}
	add(event.TopicDerivTrade, &tradev1.TradeExecuted{
		TradeId: uuid.NewString(), Symbol: "BTC-USDT-PERP", BaseAsset: "BTC", QuoteAsset: "USDT", Sequence: 1, Price: "60000",
		Quantity: "0.5", QuoteQuantity: "30000", BuyerOrderId: uuid.NewString(), BuyerUserId: buyer, SellerOrderId: uuid.NewString(),
		SellerUserId: seller, TradeNumber: 1,
	}, at)
	add(event.TopicDerivPosition, &derivativesv1.FillSettled{
		TradeId: uuid.NewString(), OrderId: uuid.NewString(), UserId: seller, Symbol: "BTC-USDT-PERP", Side: "SELL", PositionSide: "BOTH",
		Price: "60000", Quantity: "0.5", Fee: "15", RealizedPnl: "0", ExecutedAt: timestamppb.New(at),
	}, at)
	add(event.TopicDerivPosition, &derivativesv1.PositionOpened{Position: snapshot}, at)
	later := proto.Clone(snapshot).(*derivativesv1.Position)
	later.Funding, later.Version = "-3", 3
	add(event.TopicDerivPosition, &derivativesv1.FundingPaid{
		Position: later, FundingTime: timestamppb.New(at.Truncate(time.Hour)), FundingRate: "0.0001", MarkPrice: "60000", Amount: "-3",
	}, at.Add(time.Minute))
	add(event.TopicDerivLiquidation, &derivativesv1.LiquidationFilled{
		UserId: seller, Symbol: "BTC-USDT-PERP", PositionSide: "BOTH", TradeId: uuid.NewString(), Price: "61300", Quantity: "0.5",
		RealizedPnl: "-650", InsurancePaid: "50",
	}, at.Add(2*time.Minute))
	// A redelivered copy of the whole batch collapses.
	if err := in.Store(ctx, append(batch, batch...)); err != nil {
		t.Fatalf("Store: %v", err)
	}

	check := func(what string) {
		t.Helper()
		var trades uint64
		var volume decimal.Decimal
		if err := conn.QueryRow(ctx, `SELECT count(), sum(quantity) FROM trades FINAL WHERE symbol = 'ETH-BTC'`).Scan(&trades, &volume); err != nil {
			t.Fatal(err)
		}
		if trades != 3 || !volume.Equal(decimal.NewFromInt(3)) {
			t.Fatalf("%s: %d trades, volume %s", what, trades, volume)
		}
		type candle struct {
			open, high, low, close, volume decimal.Decimal
			n                              uint32
		}
		var c0, c1 candle
		q := `SELECT open, high, low, close, volume, trades FROM candles_1m FINAL WHERE symbol = 'ETH-BTC' ORDER BY open_time`
		rows, err := conn.Query(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		var got []candle
		for rows.Next() {
			var c candle
			if err := rows.Scan(&c.open, &c.high, &c.low, &c.close, &c.volume, &c.n); err != nil {
				t.Fatal(err)
			}
			got = append(got, c)
		}
		_ = rows.Close()
		if len(got) != 2 {
			t.Fatalf("%s: %d candles, want 2", what, len(got))
		}
		c0, c1 = got[0], got[1]
		if !c0.open.Equal(decimal.RequireFromString("0.03")) || !c0.high.Equal(decimal.RequireFromString("0.032")) ||
			!c0.close.Equal(decimal.RequireFromString("0.032")) || c0.n != 2 || !c1.open.Equal(decimal.RequireFromString("0.029")) || c1.n != 1 {
			t.Fatalf("%s: candles %+v %+v", what, c0, c1)
		}
		var hourly uint64
		if err := conn.QueryRow(ctx, `SELECT trades FROM candles(symbol = 'ETH-BTC', seconds = 3600)`).Scan(&hourly); err != nil || hourly != 3 {
			t.Fatalf("%s: hourly candle %d trades, %v", what, hourly, err)
		}
		var status, side, reason string
		var filled decimal.Decimal
		var created, updated time.Time
		if err := conn.QueryRow(ctx, `SELECT status, side, filled_quantity, created_at, updated_at FROM orders_current WHERE order_id = ?`, buy).
			Scan(&status, &side, &filled, &created, &updated); err != nil || status != "PARTIALLY_FILLED" || side != "BUY" ||
			!filled.Equal(decimal.NewFromInt(1)) || !created.Equal(at) || !updated.Equal(at.Add(time.Minute)) {
			t.Fatalf("%s: buy order %s %s %s, %s to %s (%v)", what, status, side, filled, created, updated, err)
		}
		var keyed uint8
		if err := conn.QueryRow(ctx, `SELECT created_key = UUIDv7ToDateTime(order_id, 'UTC') AND created_key > '2026-01-01'
			FROM orders_current WHERE order_id = ?`, buy).Scan(&keyed); err != nil || keyed != 1 {
			t.Fatalf("%s: the buy order's created_key is not its ID's time (%v)", what, err)
		}
		if err := conn.QueryRow(ctx, `SELECT status, reason FROM orders_current WHERE order_id = ?`, rejected).Scan(&status, &reason); err != nil ||
			status != "REJECTED" || reason != "LEDGER_INSUFFICIENT_BALANCE" {
			t.Fatalf("%s: rejected order %s %s (%v)", what, status, reason, err)
		}
		var orders uint64
		if err := conn.QueryRow(ctx, `SELECT count() FROM orders_current`).Scan(&orders); err != nil || orders != 3 {
			t.Fatalf("%s: %d orders (%v)", what, orders, err)
		}
		var journal string
		if err := conn.QueryRow(ctx, `SELECT status, journal_id FROM wallet_deposits FINAL WHERE deposit_id = ?`, dep).Scan(&status, &journal); err != nil ||
			status != "CREDITED" || journal != "j1" {
			t.Fatalf("%s: deposit %s %s (%v)", what, status, journal, err)
		}
		if err := conn.QueryRow(ctx, `SELECT status FROM wallet_withdrawals FINAL WHERE withdrawal_id = ?`, wd).Scan(&status); err != nil ||
			status != "PENDING_REVIEW" {
			t.Fatalf("%s: withdrawal %s (%v)", what, status, err)
		}
		var perpTrades, fills, liquidations uint64
		var notional, funding, insurance decimal.Decimal
		if err := conn.QueryRow(ctx, `SELECT
				(SELECT count() FROM trades FINAL WHERE symbol = 'BTC-USDT-PERP'),
				(SELECT count() FROM derivatives_fills FINAL), (SELECT sum(notional) FROM derivatives_fills FINAL),
				(SELECT sum(amount) FROM derivatives_funding FINAL),
				(SELECT count() FROM derivatives_liquidations FINAL), (SELECT sum(insurance_paid) FROM derivatives_liquidations FINAL)`).
			Scan(&perpTrades, &fills, &notional, &funding, &liquidations, &insurance); err != nil {
			t.Fatal(err)
		}
		if perpTrades != 1 || fills != 1 || !notional.Equal(decimal.NewFromInt(30000)) || !funding.Equal(decimal.NewFromInt(-3)) ||
			liquidations != 1 || !insurance.Equal(decimal.NewFromInt(50)) {
			t.Fatalf("%s: contracts %d trades, %d fills of %s, funding %s, %d liquidations with %s insurance", what, perpTrades, fills,
				notional, funding, liquidations, insurance)
		}
		var funded decimal.Decimal
		var version uint64
		if err := conn.QueryRow(ctx, `SELECT funding, version FROM derivatives_positions FINAL WHERE position_id = ?`, position).
			Scan(&funded, &version); err != nil || version != 3 || !funded.Equal(decimal.NewFromInt(-3)) {
			t.Fatalf("%s: position version %d funding %s (%v)", what, version, funded, err)
		}
	}
	check("live")

	// Late and repeated: the order's open again, and a fill numbered below
	// the latest that arrives after it (occurred between the two). Neither
	// moves the order's state or times.
	batch = nil
	add(event.TopicOrder, &orderv1.OrderOpened{OrderId: buy, UserId: buyer, Symbol: "ETH-BTC", Sequence: 5}, at)
	add(event.TopicOrder, &orderv1.OrderPartiallyFilled{
		OrderId: buy, UserId: buyer, Symbol: "ETH-BTC", Sequence: 11, FilledQuantity: "0.5", FilledQuote: "0.015",
	}, at.Add(30*time.Second))
	if err := in.Store(ctx, batch); err != nil {
		t.Fatalf("Store: %v", err)
	}
	check("late and repeated")

	// The backfill rebuilds the read models from events, once.
	for _, table := range []string{
		"trades", "orders", "order_updates", "orders_state", "wallet_deposits", "wallet_withdrawals", "candles_1m", "derivatives_positions",
		"derivatives_fills", "derivatives_funding", "derivatives_liquidations",
	} {
		if err := conn.Exec(ctx, "TRUNCATE TABLE "+table); err != nil {
			t.Fatal(err)
		}
	}
	if err := in.BackfillReadModels(ctx, 4); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	check("backfilled")
	if err := conn.Exec(ctx, "TRUNCATE TABLE trades"); err != nil {
		t.Fatal(err)
	}
	if err := in.BackfillReadModels(ctx, 4); err != nil {
		t.Fatal(err)
	}
	var n uint64
	if err := conn.QueryRow(ctx, `SELECT count() FROM trades`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a second backfill ran: %d trades (%v)", n, err)
	}
}

func TestMarginReadModels(t *testing.T) {
	ctx := context.Background()
	chCfg := testenv.ClickHouse(t)
	conn, err := chx.Open(ctx, chCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sqlDB := chx.OpenDB(chCfg)
	defer sqlDB.Close()
	if err := migrate.UpClickHouse(ctx, sqlDB, migrations.ClickHouse(), discard); err != nil {
		t.Fatalf("clickhouse migrations: %v", err)
	}
	in := analytics.NewIngestor(conn, discard, prometheus.NewRegistry())
	f := event.NewFactory("test", "t")
	at := time.Date(2026, 10, 6, 5, 0, 0, 0, time.UTC)
	var batch []kafka.Delivery
	add := func(topic string, msg proto.Message) {
		t.Helper()
		env, err := f.New(ctx, msg, "user", "u", event.WithOccurredAt(at))
		if err != nil {
			t.Fatal(err)
		}
		batch = append(batch, kafka.Delivery{Topic: topic, Envelope: env})
	}
	user, liquidation, interest, journal := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	add(event.TopicMargin, &marginv1.MarginInterestAccrued{
		InterestId: interest, UserId: user, AccountType: "MARGIN_CROSS", Asset: "USDT", Principal: "150", InterestModel: "FIXED",
		HourlyRate: "0.00001", Interest: "0.0015", InterestOwed: "0.003", Hour: timestamppb.New(at), JournalId: uuid.NewString(),
	})
	// The end before the start: each sets its own columns.
	add(event.TopicMargin, &marginv1.MarginLiquidationCompleted{
		LiquidationId: liquidation, UserId: user, AccountType: "MARGIN_ISOLATED", Symbol: "BTC-USDT",
		Repaid: []*marginv1.AssetAmount{{Asset: "USDT", Amount: "100.1"}}, Fee: "2.1", InsuranceCovered: "0",
		CompletedAt: timestamppb.New(at.Add(time.Second)),
	})
	add(event.TopicMargin, &marginv1.MarginLiquidationStarted{
		LiquidationId: liquidation, UserId: user, AccountType: "MARGIN_ISOLATED", Symbol: "BTC-USDT", MarginLevel: "1.049",
		TotalAsset: "105", TotalLiability: "100.1", StartedAt: timestamppb.New(at),
	})
	add(event.TopicLedger, &ledgerv1.EntryPosted{JournalId: journal, Seq: 1, EntryType: "MARGIN_BORROW", Lines: []*ledgerv1.EntryLine{{
		AccountId: uuid.NewString(), OwnerType: "USER", OwnerId: user, AccountType: "MARGIN_ISOLATED", Scope: "BTC-USDT", Asset: "USDT",
		Amount: "100", BalanceKind: "AVAILABLE", AvailableAfter: "100", FrozenAfter: "0",
	}}})
	// Redelivered copies change nothing.
	if err := in.Store(ctx, append(batch, batch...)); err != nil {
		t.Fatalf("Store: %v", err)
	}
	var charges uint64
	if err := conn.QueryRow(ctx, `SELECT count() FROM margin_interest FINAL WHERE interest_id = ?`, interest).Scan(&charges); err != nil || charges != 1 {
		t.Fatalf("interest rows: %d (%v)", charges, err)
	}
	var level, fee *decimal.Decimal
	var repaid *string
	var done bool
	if err := conn.QueryRow(ctx, `SELECT margin_level, fee, repaid, completed_at IS NOT NULL FROM margin_liquidations FINAL WHERE liquidation_id = ?`,
		liquidation).Scan(&level, &fee, &repaid, &done); err != nil {
		t.Fatal(err)
	}
	if level == nil || level.String() != "1.049" || fee == nil || fee.String() != "2.1" || repaid == nil ||
		*repaid != `[{"asset":"USDT","amount":"100.1"}]` || !done {
		t.Fatalf("liquidation: level %v fee %v repaid %v done %v", level, fee, repaid, done)
	}
	var scope string
	if err := conn.QueryRow(ctx, `SELECT scope FROM ledger_entries FINAL WHERE journal_id = ?`, journal).Scan(&scope); err != nil || scope != "BTC-USDT" {
		t.Fatalf("ledger line scope %q (%v)", scope, err)
	}
}
