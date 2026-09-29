package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	derivativesv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/derivatives/v1"
	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	orderv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/order/v1"
	tradev1 "github.com/lidp280504357/exchange/api/gen/go/exchange/trade/v1"
	walletv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/wallet/v1"
	"github.com/lidp280504357/exchange/internal/platform/authtoken"
	"github.com/lidp280504357/exchange/internal/platform/event"
)

type wsClient struct {
	t  *testing.T
	ws *websocket.Conn
}

func dial(t *testing.T, url string) *wsClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	t.Cleanup(func() { _ = ws.CloseNow() })
	return &wsClient{t: t, ws: ws}
}

func (c *wsClient) send(msg string) {
	c.t.Helper()
	if err := c.ws.Write(context.Background(), websocket.MessageText, []byte(msg)); err != nil {
		c.t.Fatal(err)
	}
}

// next reads messages until one that is not a ping.
func (c *wsClient) next() map[string]any {
	c.t.Helper()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, data, err := c.ws.Read(ctx)
		cancel()
		if err != nil {
			c.t.Fatalf("read: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			c.t.Fatal(err)
		}
		if m["op"] != "ping" {
			return m
		}
	}
}

func TestWebSocketPrivateChannels(t *testing.T) {
	f := newAuthFixture(t)
	authn := &Authenticator{
		Verifier: authtoken.NewVerifier(func(context.Context) (authtoken.JWKS, error) { return f.signer.JWKS(), nil }),
		State:    func(context.Context, string, string) (bool, int64, error) { return false, 0, nil },
		Log:      slog.New(slog.DiscardHandler),
		Now:      time.Now,
	}
	hub := NewHub(authn, nil, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	go func() { _ = hub.Run() }()
	t.Cleanup(func() { _ = hub.Stop(context.Background()) })
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	f.now = time.Now()
	token := f.token(t, "s-1", authtoken.ScopeFull)

	c := dial(t, url)
	c.send(`{"op":"subscribe","args":["balances"]}`)
	if m := c.next(); m["ok"] == true || m["code"] != "COMMON_UNAUTHORIZED" {
		t.Fatalf("subscribe before auth: %v", m)
	}
	c.send(`{"op":"auth","token":"garbage"}`)
	if m := c.next(); m["ok"] == true {
		t.Fatalf("bad token: %v", m)
	}
	c.send(`{"op":"auth","token":"` + token + `"}`)
	if m := c.next(); m["ok"] != true || m["user_id"] != "u-1" {
		t.Fatalf("auth: %v", m)
	}
	c.send(`{"op":"subscribe","args":["candles:BTC-USDT:2d"]}`)
	if m := c.next(); m["ok"] == true {
		t.Fatalf("an unknown interval: %v", m)
	}
	c.send(`{"op":"subscribe","args":["balances","notifications"]}`)
	if m := c.next(); m["ok"] != true {
		t.Fatalf("subscribe: %v", m)
	}

	hub.Publish("u-1", "balances", map[string]string{"asset": "USDT", "available": "1"})
	hub.Publish("u-2", "balances", map[string]string{"asset": "USDT"}) // someone else
	hub.Publish("u-1", "notifications", map[string]string{"type": "WELCOME"})
	if m := c.next(); m["channel"] != "balances" || m["seq"] != float64(1) {
		t.Fatalf("first push: %v", m)
	}
	if m := c.next(); m["channel"] != "notifications" || m["seq"] != float64(2) {
		t.Fatalf("second push: %v", m)
	}
	c.send(`{"op":"ping"}`)
	if m := c.next(); m["op"] != "pong" {
		t.Fatalf("ping: %v", m)
	}
	c.send(`{"op":"subscribe","args":["deposits"]}`)
	if m := c.next(); m["ok"] != true {
		t.Fatalf("subscribe deposits: %v", m)
	}
	env, err := event.NewFactory("test", "t").New(context.Background(), &walletv1.DepositConfirmed{Deposit: &walletv1.Deposit{
		DepositId: "d1", UserId: "u-1", Asset: "ETH", Amount: "0.002", Status: "CONFIRMED", Confirmations: 12, RequiredConfirmations: 12,
	}}, "user", "u-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := WSEvents(hub)(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if m := c.next(); m["channel"] != "deposits" || m["data"].(map[string]any)["status"] != "CONFIRMED" || m["data"].(map[string]any)["reason"] != nil {
		t.Fatalf("deposit push: %v", m)
	}

	// Contract positions and fills.
	c.send(`{"op":"subscribe","args":["positions","fills"]}`)
	if m := c.next(); m["ok"] != true {
		t.Fatalf("subscribe positions: %v", m)
	}
	for _, msg := range []proto.Message{
		&derivativesv1.PositionOpened{TradeId: "t1", Position: &derivativesv1.Position{
			PositionId: "p1", UserId: "u-1", Symbol: "BTC-USDT-PERP", PositionSide: "BOTH", Quantity: "0.1", EntryPrice: "60000",
			Margin: "600", MarginMode: "CROSS", Leverage: 10,
		}},
		&derivativesv1.FillSettled{
			TradeId: "t1", OrderId: "o1", UserId: "u-1", Symbol: "BTC-USDT-PERP", Side: "BUY", PositionSide: "BOTH", Price: "60000",
			Quantity: "0.1", ClosedQuantity: "0", Fee: "3", RealizedPnl: "0", ExecutedAt: timestamppb.Now(),
		},
	} {
		env, err := event.NewFactory("test", "t").New(context.Background(), msg, "user", "u-1")
		if err != nil {
			t.Fatal(err)
		}
		if err := WSEvents(hub)(context.Background(), env); err != nil {
			t.Fatal(err)
		}
	}
	if m := c.next(); m["channel"] != "positions" || m["data"].(map[string]any)["event"] != "OPEN" || m["data"].(map[string]any)["leverage"] != float64(10) {
		t.Fatalf("position push: %v", m)
	}
	if m := c.next(); m["channel"] != "fills" || m["data"].(map[string]any)["fee_asset"] != "USDT" || m["data"].(map[string]any)["quote_quantity"] != nil {
		t.Fatalf("contract fill push: %v", m)
	}

	// A reconnecting client asks for what it missed.
	c2 := dial(t, url)
	c2.send(`{"op":"auth","token":"` + token + `"}`)
	c2.next()
	c2.send(`{"op":"subscribe","args":["notifications"],"last_seq":1}`)
	if m := c2.next(); m["ok"] != true {
		t.Fatalf("resubscribe: %v", m)
	}
	if m := c2.next(); m["channel"] != "notifications" || m["seq"] != float64(2) {
		t.Fatalf("replayed push: %v", m)
	}

	// At most ten connections per user.
	var extra []*wsClient
	for range wsMaxConnsPerUser - 2 {
		x := dial(t, url)
		x.send(`{"op":"auth","token":"` + token + `"}`)
		if m := x.next(); m["ok"] != true {
			t.Fatalf("connection within the cap: %v", m)
		}
		extra = append(extra, x)
	}
	over := dial(t, url)
	over.send(`{"op":"auth","token":"` + token + `"}`)
	if m := over.next(); m["code"] != "COMMON_RATE_LIMITED" {
		t.Fatalf("eleventh connection: %v", m)
	}
	_ = extra
}

func TestWebSocketPublicChannels(t *testing.T) {
	hub := NewHub(nil, nil, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	go func() { _ = hub.Run() }()
	t.Cleanup(func() { _ = hub.Stop(context.Background()) })
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	events := WSEvents(hub)
	emit := func(msg proto.Message) {
		t.Helper()
		env, err := event.NewFactory("test", "t").New(context.Background(), msg, "symbol", "BTC-USDT")
		if err != nil {
			t.Fatal(err)
		}
		if err := events(context.Background(), env); err != nil {
			t.Fatal(err)
		}
	}
	levels := func(pq ...string) []*marketv1.PriceLevel {
		var out []*marketv1.PriceLevel
		for i := 0; i+1 < len(pq); i += 2 {
			out = append(out, &marketv1.PriceLevel{Price: pq[i], Quantity: pq[i+1]})
		}
		return out
	}
	emit(&marketv1.TickerUpdated{Ticker: &marketv1.Ticker{Symbol: "BTC-USDT", Last: "70000", Volume: "1.5", UpdatedAt: timestamppb.Now()}})

	// No sign-in needed: the depth snapshot and the latest ticker come first.
	c := dial(t, url)
	c.send(`{"op":"subscribe","args":["depth:BTC-USDT","ticker:BTC-USDT","trades:BTC-USDT"]}`)
	if m := c.next(); m["ok"] != true {
		t.Fatalf("subscribe: %v", m)
	}
	if m := c.next(); m["channel"] != "depth:BTC-USDT" || m["type"] != "snapshot" || m["seq"] != nil {
		t.Fatalf("empty snapshot: %v", m)
	}
	if m := c.next(); m["channel"] != "ticker:BTC-USDT" || m["data"].(map[string]any)["last"] != "70000" || m["data"].(map[string]any)["open"] != nil {
		t.Fatalf("latest ticker: %v", m)
	}

	emit(&marketv1.DepthSnapshot{Symbol: "BTC-USDT", Sequence: 5, Bids: levels("69900", "1", "69800", "2"), Asks: levels("70100", "0.5")})
	m := c.next()
	if m["type"] != "update" || m["seq"] != float64(1) || m["prev_seq"] != nil {
		t.Fatalf("first update: %v", m)
	}
	// An unchanged snapshot sends nothing; a changed one only the changes,
	// "0" for a level that went away.
	emit(&marketv1.DepthSnapshot{Symbol: "BTC-USDT", Sequence: 6, Bids: levels("69900", "1", "69800", "2"), Asks: levels("70100", "0.5")})
	emit(&marketv1.DepthSnapshot{Symbol: "BTC-USDT", Sequence: 9, Bids: levels("69900", "1.5"), Asks: levels("70100", "0.5")})
	emit(&marketv1.DepthSnapshot{Symbol: "BTC-USDT", Sequence: 7, Bids: levels("1", "1")}) // older: ignored
	m = c.next()
	data, _ := json.Marshal(m["data"])
	if m["seq"] != float64(2) || m["prev_seq"] != float64(1) || string(data) != `{"asks":[],"bids":[["69900","1.5"],["69800","0"]]}` {
		t.Fatalf("second update: %v %s", m, data)
	}

	emit(&tradev1.TradeExecuted{
		TradeId: "t1", TradeNumber: 7, Symbol: "BTC-USDT", BaseAsset: "BTC", QuoteAsset: "USDT", Price: "70100", Quantity: "0.1",
		QuoteQuantity: "7010", TakerSide: orderv1.Side_SIDE_BUY, BuyerUserId: "u-1", SellerUserId: "u-2", BuyerFee: "0.0001", SellerFee: "7.01",
	})
	if m := c.next(); m["channel"] != "trades:BTC-USDT" || m["data"].(map[string]any)["taker_side"] != "BUY" || m["data"].(map[string]any)["trade_number"] != float64(7) {
		t.Fatalf("public trade: %v", m)
	}

	// A late subscriber gets the current book, at the current seq.
	c2 := dial(t, url)
	c2.send(`{"op":"subscribe","args":["depth:BTC-USDT"]}`)
	c2.next()
	m = c2.next()
	data, _ = json.Marshal(m["data"])
	if m["type"] != "snapshot" || m["seq"] != float64(2) || string(data) != `{"asks":[["70100","0.5"]],"bids":[["69900","1.5"]]}` {
		t.Fatalf("late snapshot: %v %s", m, data)
	}
	c2.send(`{"op":"unsubscribe","args":["depth:BTC-USDT"]}`)
	if m := c2.next(); m["op"] != "unsubscribe" {
		t.Fatalf("unsubscribe: %v", m)
	}
	emit(&marketv1.DepthSnapshot{Symbol: "BTC-USDT", Sequence: 10, Bids: levels("69950", "1")})
	if m := c.next(); m["seq"] != float64(3) {
		t.Fatalf("update after the other left: %v", m)
	}
	hub.mu.Lock()
	subs := len(hub.public["depth:BTC-USDT"])
	hub.mu.Unlock()
	if subs != 1 {
		t.Fatalf("%d depth subscribers, want 1", subs)
	}
}

func TestWebSocketContractChannels(t *testing.T) {
	hub := NewHub(nil, nil, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	go func() { _ = hub.Run() }()
	t.Cleanup(func() { _ = hub.Stop(context.Background()) })
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)
	events := WSEvents(hub)
	emit := func(msg proto.Message) {
		t.Helper()
		env, err := event.NewFactory("test", "t").New(context.Background(), msg, "symbol", "BTC-USDT-PERP")
		if err != nil {
			t.Fatal(err)
		}
		if err := events(context.Background(), env); err != nil {
			t.Fatal(err)
		}
	}
	next8 := timestamppb.New(time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC))
	emit(&marketv1.FundingRateUpdated{Symbol: "BTC-USDT-PERP", FundingRate: "0.0001", InterestRate: "0.0001", FundingTime: next8})

	c := dial(t, "ws"+strings.TrimPrefix(srv.URL, "http"))
	c.send(`{"op":"subscribe","args":["mark-price:BTC-USDT"]}`)
	if m := c.next(); m["ok"] == true {
		t.Fatalf("a pair has no mark price: %v", m)
	}
	c.send(`{"op":"subscribe","args":["mark-price:BTC-USDT-PERP","funding:BTC-USDT-PERP","trades:BTC-USDT-PERP"]}`)
	if m := c.next(); m["ok"] != true {
		t.Fatalf("subscribe: %v", m)
	}
	// The running estimate comes first.
	if m := c.next(); m["channel"] != "funding:BTC-USDT-PERP" || m["type"] != "estimate" ||
		m["data"].(map[string]any)["funding_time"] != "2026-09-30T08:00:00Z" || m["data"].(map[string]any)["mark_price"] != nil {
		t.Fatalf("latest estimate: %v", m)
	}
	emit(&marketv1.MarkPriceUpdated{
		Symbol: "BTC-USDT-PERP", MarkPrice: "60012.5", IndexPrice: "60000", Basis: "0.0002", FundingRate: "0.0001",
		NextFundingTime: next8, ComputedAt: timestamppb.Now(),
	})
	if m := c.next(); m["channel"] != "mark-price:BTC-USDT-PERP" || m["data"].(map[string]any)["mark_price"] != "60012.5" ||
		m["data"].(map[string]any)["next_funding_time"] != "2026-09-30T08:00:00Z" {
		t.Fatalf("mark price: %v", m)
	}
	emit(&marketv1.FundingRateUpdated{
		Symbol: "BTC-USDT-PERP", FundingRate: "0.00012", InterestRate: "0.0001", FundingTime: next8, Final: true,
		MarkPrice: "60010", IndexPrice: "60000",
	})
	if m := c.next(); m["type"] != "settled" || m["data"].(map[string]any)["mark_price"] != "60010" {
		t.Fatalf("settled rate: %v", m)
	}
	// A contract's trade is public only: its fills come from
	// derivatives-service.
	c.send(`{"op":"subscribe","args":["fills"]}`)
	emit(&tradev1.TradeExecuted{
		TradeId: "t9", TradeNumber: 3, Symbol: "BTC-USDT-PERP", Price: "60010", Quantity: "0.5", QuoteQuantity: "30005",
		TakerSide: orderv1.Side_SIDE_SELL, BuyerUserId: "u-1", SellerUserId: "u-2", BuyerFee: "0", SellerFee: "0",
	})
	for {
		m := c.next()
		if m["op"] == "subscribe" {
			continue // fills needs a sign-in
		}
		if m["channel"] != "trades:BTC-USDT-PERP" || m["data"].(map[string]any)["taker_side"] != "SELL" {
			t.Fatalf("contract trade: %v", m)
		}
		break
	}
	hub.mu.Lock()
	kept := hub.latest["funding:BTC-USDT-PERP"].Type
	hub.mu.Unlock()
	if kept != "estimate" {
		t.Fatalf("new subscribers would start from %q", kept)
	}
}
