package analytics_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	orderv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/order/v1"
	tradev1 "github.com/lidp280504357/exchange/api/gen/go/exchange/trade/v1"
	walletv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/wallet/v1"
	"github.com/lidp280504357/exchange/internal/analytics"
	"github.com/lidp280504357/exchange/internal/platform/chx"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
	"github.com/lidp280504357/exchange/migrations"
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
	buy, sell, buyer, seller := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
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
	rejected := uuid.NewString()
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
	// A redelivered copy of the whole batch collapses.
	if err := in.Store(ctx, append(batch, batch...)); err != nil {
		t.Fatalf("Store: %v", err)
	}

	check := func(what string) {
		t.Helper()
		var trades uint64
		var volume decimal.Decimal
		if err := conn.QueryRow(ctx, `SELECT count(), sum(quantity) FROM trades FINAL`).Scan(&trades, &volume); err != nil {
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
		if err := conn.QueryRow(ctx, `SELECT status, side, filled_quantity FROM orders_current WHERE order_id = ?`, buy).
			Scan(&status, &side, &filled); err != nil || status != "PARTIALLY_FILLED" || side != "BUY" || !filled.Equal(decimal.NewFromInt(1)) {
			t.Fatalf("%s: buy order %s %s %s (%v)", what, status, side, filled, err)
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
	}
	check("live")

	// The backfill rebuilds the read models from events, once.
	for _, table := range []string{"trades", "orders", "order_updates", "wallet_deposits", "wallet_withdrawals", "candles_1m"} {
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
