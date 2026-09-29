package analytics

import (
	"encoding/base64"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	derivativesv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/derivatives/v1"
	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	orderv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/order/v1"
	tradev1 "github.com/lidp280504357/exchange/api/gen/go/exchange/trade/v1"
	walletv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/wallet/v1"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
)

func delivery(t *testing.T, topic string, msg proto.Message, at time.Time) kafka.Delivery {
	t.Helper()
	p, err := anypb.New(msg)
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Delivery{Topic: topic, Envelope: &eventv1.Envelope{EventId: uuid.NewString(), OccurredAt: timestamppb.New(at), Payload: p}}
}

func TestProjectTradesAndOrders(t *testing.T) {
	at := time.Date(2026, 9, 29, 3, 0, 10, 0, time.UTC)
	order, user, other := uuid.NewString(), uuid.NewString(), uuid.NewString()
	var m readModels
	for _, d := range []kafka.Delivery{
		delivery(t, event.TopicOrder, &orderv1.OrderAccepted{
			Order: &orderv1.Order{
				OrderId: order, UserId: user, Symbol: "ETH-BTC", Side: orderv1.Side_SIDE_BUY, Type: orderv1.OrderType_ORDER_TYPE_LIMIT,
				TimeInForce: orderv1.TimeInForce_TIME_IN_FORCE_GTC, Price: "0.03", Quantity: "2",
			},
			FrozenAsset: "BTC", FrozenAmount: "0.06",
		}, at),
		delivery(t, event.TopicOrder, &orderv1.OrderPartiallyFilled{
			OrderId: order, UserId: user, Symbol: "ETH-BTC", Sequence: 7, TradeId: "t1", FilledQuantity: "1", FilledQuote: "0.03",
		}, at.Add(time.Second)),
		delivery(t, event.TopicTrade, &tradev1.TradeExecuted{
			TradeId: uuid.NewString(), Symbol: "ETH-BTC", BaseAsset: "ETH", QuoteAsset: "BTC", Sequence: 7, Price: "0.03", Quantity: "1",
			QuoteQuantity: "0.03", TakerSide: orderv1.Side_SIDE_SELL, BuyerOrderId: order, BuyerUserId: user, SellerOrderId: uuid.NewString(),
			SellerUserId: other, BuyerIsMaker: true, BuyerFee: "0.001", SellerFee: "0.00003", TradeNumber: 12,
		}, at.Add(time.Second)),
		delivery(t, event.TopicTrade, &tradev1.TradeExecuted{
			TradeId: uuid.NewString(), Symbol: "ETH-BTC", Sequence: 9, Price: "0.031", Quantity: "1", QuoteQuantity: "0.031",
			BuyerOrderId: order, BuyerUserId: user, SellerOrderId: uuid.NewString(), SellerUserId: other,
		}, at.Add(3*time.Minute)),
		delivery(t, event.TopicOrder, &orderv1.OrderCanceled{OrderId: order, UserId: user, Symbol: "ETH-BTC", Sequence: 11, Reason: "USER"}, at),
		// Not a read model event.
		delivery(t, event.TopicOrder, &walletv1.DepositAddressAssigned{UserId: user}, at),
	} {
		if err := m.add(d); err != nil {
			t.Fatal(err)
		}
	}
	if len(m.orders) != 1 || len(m.updates) != 3 || len(m.trades) != 2 {
		t.Fatalf("%d orders, %d updates, %d trades", len(m.orders), len(m.updates), len(m.trades))
	}
	o := m.orders[0]
	if o[4] != "BUY" || o[5] != "LIMIT" || o[6] != "GTC" || !o[7].(*decimal.Decimal).Equal(decimal.RequireFromString("0.03")) ||
		o[9].(*decimal.Decimal) != nil || o[10] != "BTC" {
		t.Fatalf("order row %v", o)
	}
	if u := m.updates[0]; u[4] != statusNew || u[3] != int64(0) {
		t.Fatalf("accepted row %v", u)
	}
	if u := m.updates[1]; u[4] != statusPartiallyFilled || u[3] != int64(7) || !u[5].(decimal.Decimal).Equal(decimal.NewFromInt(1)) || u[7] != "t1" {
		t.Fatalf("fill row %v", u)
	}
	if u := m.updates[2]; u[4] != statusCanceled || u[8] != "USER" {
		t.Fatalf("cancel row %v", u)
	}
	if tr := m.trades[0]; tr[9] != "SELL" || tr[4] != uint64(12) || tr[14] != true {
		t.Fatalf("trade row %v", tr)
	}
	s := m.touched["ETH-BTC"]
	if !s.from.Equal(at.Add(time.Second)) || !s.to.Equal(at.Add(3*time.Minute)) {
		t.Fatalf("touched %+v", s)
	}
}

func TestProjectWallet(t *testing.T) {
	at := time.Date(2026, 9, 29, 3, 0, 0, 0, time.UTC)
	dep, wd := uuid.NewString(), uuid.NewString()
	var m readModels
	for _, d := range []kafka.Delivery{
		delivery(t, event.TopicWalletDeposit, &walletv1.DepositCredited{
			Deposit:   &walletv1.Deposit{DepositId: dep, UserId: "u1", Asset: "ETH", Amount: "0.0012", Status: "CREDITED", LogIndex: -1},
			JournalId: "j1",
		}, at),
		delivery(t, event.TopicWalletWithdrawal, &walletv1.WithdrawalRequested{
			Withdrawal: &walletv1.Withdrawal{WithdrawalId: wd, Asset: "ETH", Amount: "0.0011", Fee: "0.0002", Status: "REQUESTED"},
		}, at),
		delivery(t, event.TopicWalletWithdrawal, &walletv1.WithdrawalRiskScored{
			Withdrawal: &walletv1.Withdrawal{
				WithdrawalId: wd, Asset: "ETH", Amount: "0.0011", Fee: "0.0002", Status: "PENDING_REVIEW", RiskReasons: []string{"NEW_ACCOUNT"},
			},
			Score: 100,
		}, at),
	} {
		if err := m.add(d); err != nil {
			t.Fatal(err)
		}
	}
	if len(m.deposits) != 1 || len(m.withdrawals) != 2 {
		t.Fatalf("%d deposits, %d withdrawals", len(m.deposits), len(m.withdrawals))
	}
	if d := m.deposits[0]; d[15] != "j1" || d[10] != "CREDITED" || d[7] != int64(-1) {
		t.Fatalf("deposit row %v", d)
	}
	// Same millisecond: the risk score's status must win.
	if m.withdrawals[1][15].(uint64) <= m.withdrawals[0][15].(uint64) {
		t.Fatalf("versions %v then %v", m.withdrawals[0][15], m.withdrawals[1][15])
	}
	if r := m.withdrawals[0][12].([]string); r == nil || len(r) != 0 {
		t.Fatalf("empty risk reasons must be an empty array, got %#v", r)
	}
}

func TestProjectDerivatives(t *testing.T) {
	at := time.Date(2026, 9, 29, 8, 0, 3, 0, time.UTC)
	user, other, position := uuid.NewString(), uuid.NewString(), uuid.NewString()
	snapshot := &derivativesv1.Position{
		PositionId: position, UserId: user, Symbol: "BTC-USDT-PERP", PositionSide: "BOTH", Quantity: "-0.5", EntryPrice: "60000",
		EntryCost: "30000", Margin: "600", MarginMode: "ISOLATED", Leverage: 50, RealizedPnl: "0", Funding: "-1.5", Version: 4,
	}
	var m readModels
	for _, d := range []kafka.Delivery{
		// A contract trade joins the spot trades.
		delivery(t, event.TopicDerivTrade, &tradev1.TradeExecuted{
			TradeId: uuid.NewString(), Symbol: "BTC-USDT-PERP", Sequence: 3, Price: "60000", Quantity: "0.5", QuoteQuantity: "30000",
			BuyerOrderId: uuid.NewString(), BuyerUserId: other, SellerOrderId: uuid.NewString(), SellerUserId: user,
		}, at),
		delivery(t, event.TopicDerivPosition, &derivativesv1.FillSettled{
			TradeId: uuid.NewString(), OrderId: uuid.NewString(), UserId: user, Symbol: "BTC-USDT-PERP", Side: "SELL", PositionSide: "BOTH",
			Price: "60000", Quantity: "0.5", Fee: "15", RealizedPnl: "0", ExecutedAt: timestamppb.New(at),
		}, at),
		delivery(t, event.TopicDerivPosition, &derivativesv1.FundingPaid{
			Position: snapshot, FundingTime: timestamppb.New(at.Truncate(time.Hour)), FundingRate: "0.0001", MarkPrice: "60010", Amount: "-1.5",
		}, at),
		delivery(t, event.TopicDerivLiquidation, &derivativesv1.LiquidationWarning{
			UserId: other, Cross: true, MarginBalance: "140", MaintenanceMargin: "120", At: timestamppb.New(at),
		}, at),
		delivery(t, event.TopicDerivLiquidation, &derivativesv1.LiquidationStarted{
			Position: snapshot, MarkPrice: "61200", BankruptcyPrice: "61200", MarginBalance: "0", MaintenanceMargin: "122.4",
		}, at),
		delivery(t, event.TopicDerivLiquidation, &derivativesv1.LiquidationFilled{
			UserId: user, Symbol: "BTC-USDT-PERP", PositionSide: "BOTH", TradeId: uuid.NewString(), Price: "61300", Quantity: "0.5",
			RealizedPnl: "-650", InsurancePaid: "50",
		}, at),
		delivery(t, event.TopicDerivLiquidation, &derivativesv1.AdlExecuted{
			UserId: other, Symbol: "BTC-USDT-PERP", PositionSide: "BOTH", TradeId: uuid.NewString(), Price: "61200", Quantity: "0.1",
			RealizedPnl: "120",
		}, at),
		// Not a read model event.
		delivery(t, event.TopicDerivPosition, &derivativesv1.LeverageChanged{UserId: user, Symbol: "BTC-USDT-PERP", Leverage: 20}, at),
	} {
		if err := m.add(d); err != nil {
			t.Fatal(err)
		}
	}
	if len(m.trades) != 1 || len(m.fills) != 1 || len(m.funding) != 1 || len(m.positions) != 2 || len(m.liquidations) != 4 {
		t.Fatalf("%d trades, %d fills, %d funding, %d positions, %d liquidations", len(m.trades), len(m.fills), len(m.funding),
			len(m.positions), len(m.liquidations))
	}
	if f := m.fills[0]; !f[9].(decimal.Decimal).Equal(decimal.NewFromInt(30000)) || !f[11].(decimal.Decimal).Equal(decimal.NewFromInt(15)) {
		t.Fatalf("fill row %v", f)
	}
	if p := m.positions[0]; p[13] != uint64(4) || !p[5].(*decimal.Decimal).Equal(decimal.NewFromInt(60000)) || p[9] != int32(50) {
		t.Fatalf("position row %v", p)
	}
	if f := m.funding[0]; !f[8].(decimal.Decimal).Equal(decimal.RequireFromString("-1.5")) || !f[5].(time.Time).Equal(at.Truncate(time.Hour)) {
		t.Fatalf("funding row %v", f)
	}
	kinds := []string{}
	for _, l := range m.liquidations {
		kinds = append(kinds, l[1].(string))
	}
	if fmt.Sprint(kinds) != "[WARNING STARTED FILLED ADL]" {
		t.Fatalf("liquidation kinds %v", kinds)
	}
	if w := m.liquidations[0]; w[3] != "" || w[5] != true || !w[14].(decimal.Decimal).Equal(decimal.NewFromInt(140)) {
		t.Fatalf("warning row %v", w)
	}
	if f := m.liquidations[2]; !f[11].(decimal.Decimal).Equal(decimal.NewFromInt(50)) || f[6] != false {
		t.Fatalf("filled row %v", f)
	}
	if a := m.liquidations[3]; a[6] != true || !a[10].(decimal.Decimal).Equal(decimal.NewFromInt(120)) {
		t.Fatalf("adl row %v", a)
	}
}

func TestProjectRejectsMalformed(t *testing.T) {
	var m readModels
	bad := delivery(t, event.TopicTrade, &tradev1.TradeExecuted{TradeId: "not-a-uuid", Symbol: "ETH-BTC", Price: "0.03"}, time.Now())
	if err := m.add(bad); !errors.Is(err, errMalformed) {
		t.Fatalf("got %v", err)
	}
	price := delivery(t, event.TopicOrder, &orderv1.OrderFilled{OrderId: uuid.NewString(), UserId: uuid.NewString(), FilledQuantity: "1e"}, time.Now())
	if err := m.add(price); !errors.Is(err, errMalformed) {
		t.Fatalf("got %v", err)
	}
	if len(m.trades)+len(m.updates) != 0 {
		t.Fatal("malformed rows were kept")
	}
}

func TestPayloadAny(t *testing.T) {
	msg := &orderv1.OrderOpened{OrderId: "o1", Sequence: 3}
	p, err := anypb.New(msg)
	if err != nil {
		t.Fatal(err)
	}
	for _, stored := range []string{PayloadJSON(p), `{"@type":"` + p.GetTypeUrl() + `","@raw":"` + rawOf(t, msg) + `"}`} {
		back, err := payloadAny(stored)
		if err != nil {
			t.Fatalf("%s: %v", stored, err)
		}
		var got orderv1.OrderOpened
		if err := back.UnmarshalTo(&got); err != nil || got.GetOrderId() != "o1" || got.GetSequence() != 3 {
			t.Fatalf("%s: %v %v", stored, &got, err)
		}
	}
}

func rawOf(t *testing.T, msg proto.Message) string {
	t.Helper()
	b, err := proto.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}
