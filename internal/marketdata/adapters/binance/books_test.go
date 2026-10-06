package binance

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/marketdata/ports"
)

// Messages as Binance sends them, with keys that differ only in case
// (e/E, m/M, U/u, t/T): each must reach its handler.
func TestBookStreamPassesDepthAndTrades(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("streams") != "btcusdt@depth@100ms/btcusdt@aggTrade/pepeusdt@depth@100ms/pepeusdt@aggTrade" {
			http.Error(w, "bad streams", http.StatusBadRequest)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for _, msg := range []string{
			`{"stream":"btcusdt@depth@100ms","data":{"e":"depthUpdate","E":1790617150042,"s":"BTCUSDT","U":157,"u":160,"b":[["83931.72","1.5"],["83930.00","0.00000000"]],"a":[["83931.73","0.8"]]}}`,
			`{"stream":"btcusdt@aggTrade","data":{"e":"aggTrade","E":1790617150100,"s":"BTCUSDT","a":26129,"p":"83931.73","q":"0.1","f":100,"l":105,"T":1790617150099,"m":true,"M":true}}`,
			`{"stream":"pepeusdt@depth@100ms","data":{"e":"depthUpdate","E":1790617150200,"s":"PEPEUSDT","U":10,"u":12,"b":[["0.00001239","5000000"]],"a":[]}}`,
			`{"stream":"ethusdt@depth@100ms","data":{"e":"depthUpdate","E":1790617150300,"s":"ETHUSDT","U":1,"u":2,"b":[],"a":[]}}`,
		} {
			_ = conn.Write(r.Context(), websocket.MessageText, []byte(msg))
		}
		_ = conn.Close(websocket.StatusGoingAway, "24 hours are up")
	}))
	defer srv.Close()
	s := New("", "ws"+strings.TrimPrefix(srv.URL, "http"), srv.Client())
	type diff struct {
		symbol string
		d      domain.DepthDiff
	}
	var depths []diff
	var trades []domain.Trade
	err := s.BookStream(context.Background(), []ports.Reference{btc, pepe}, ports.BookHandlers{
		Depth: func(symbol string, d domain.DepthDiff) { depths = append(depths, diff{symbol, d}) },
		Trade: func(tr domain.Trade) { trades = append(trades, tr) },
	})
	if err == nil {
		t.Fatal("a closed stream is an error for the caller to reconnect")
	}
	if len(depths) != 2 {
		t.Fatalf("depth updates %+v", depths)
	}
	b := depths[0]
	if b.symbol != "BTC-USDT" || b.d.First != 157 || b.d.Last != 160 || len(b.d.Bids) != 2 || b.d.Bids[0].Price.String() != "83931.72" ||
		!b.d.Bids[1].Quantity.IsZero() || len(b.d.Asks) != 1 || b.d.Asks[0].Quantity.String() != "0.8" {
		t.Fatalf("BTC-USDT update %+v", b)
	}
	// 1000PEPE: prices times 1000, quantities divided by it.
	if p := depths[1]; p.symbol != "1000PEPE-USDT" || p.d.Bids[0].Price.String() != "0.01239" || p.d.Bids[0].Quantity.String() != "5000" {
		t.Fatalf("1000PEPE-USDT update %+v", p)
	}
	if len(trades) != 1 || trades[0].Symbol != "BTC-USDT" || trades[0].Price.String() != "83931.73" || trades[0].TakerSide != "SELL" ||
		trades[0].Number != 26129 {
		t.Fatalf("trades %+v", trades)
	}
}

func TestRecentTradesKeepTheTakersSide(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/aggTrades" || r.URL.Query().Get("symbol") != "BTCUSDT" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// "M" (best price match) is true on every spot trade; the taker's
		// side comes from "m" alone.
		_, _ = w.Write([]byte(`[{"a":1,"p":"83931.73","q":"0.1","f":1,"l":1,"T":1790617150099,"m":false,"M":true},
			{"a":2,"p":"83931.72","q":"0.2","f":2,"l":2,"T":1790617150199,"m":true,"M":true}]`))
	}))
	defer srv.Close()
	s := New(srv.URL, "", srv.Client())
	s.gap = 0
	trades, err := s.RecentTrades(context.Background(), btc, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 2 || trades[0].TakerSide != "BUY" || trades[1].TakerSide != "SELL" {
		t.Fatalf("trades %+v", trades)
	}
}

// USDⓈ-M futures sends the books under /public and the trades under
// /market: the trades' connection is kept up of its own (a failure is
// reported and retried), and the stream ends with the books' connection.
func TestFuturesBookStreamSplitsBooksAndTrades(t *testing.T) {
	var trades atomic.Int32
	booksDone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		streams := r.URL.Query().Get("streams")
		switch {
		case r.URL.Path == "/public/stream" && streams == "btcusdt@depth@100ms":
		case r.URL.Path == "/market/stream" && streams == "btcusdt@aggTrade":
		default:
			http.Error(w, "bad path "+r.URL.Path+" "+streams, http.StatusBadRequest)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		if r.URL.Path == "/market/stream" {
			n := trades.Add(1)
			msg := fmt.Sprintf(`{"stream":"btcusdt@aggTrade","data":{"e":"aggTrade","E":1791289872650,"a":%d,"s":"BTCUSDT","p":"86117.80","q":"0.008","nq":"0.008","f":1,"l":1,"T":1791289872533,"m":true,"st":1}}`, n)
			_ = conn.Write(r.Context(), websocket.MessageText, []byte(msg))
			if n == 1 {
				_ = conn.Close(websocket.StatusGoingAway, "the first trade connection ends")
				return
			}
			<-r.Context().Done()
			return
		}
		_ = conn.Write(r.Context(), websocket.MessageText,
			[]byte(`{"stream":"btcusdt@depth@100ms","data":{"e":"depthUpdate","E":1791289902050,"T":1791289902049,"s":"BTCUSDT","ps":"BTCUSDT","U":1,"u":2,"pu":0,"b":[["86000.0","1"]],"a":[]}}`))
		select {
		case <-booksDone:
		case <-time.After(5 * time.Second):
		}
		_ = conn.Close(websocket.StatusGoingAway, "the books' connection ends")
	}))
	defer srv.Close()
	s := New("", "", srv.Client()).WithFutures(srv.URL, "ws"+strings.TrimPrefix(srv.URL, "http"))
	perp := ports.Reference{Symbol: "BTC-USDT-PERP", Remote: "BTCUSDT", Multiplier: decimal.NewFromInt(1), Market: ports.MarketUSDM}
	var mu sync.Mutex
	var got []domain.Trade
	var depths int
	var failed []error
	err := s.BookStream(context.Background(), []ports.Reference{perp}, ports.BookHandlers{
		Depth: func(string, domain.DepthDiff) {
			mu.Lock()
			depths++
			mu.Unlock()
		},
		Trade: func(tr domain.Trade) {
			mu.Lock()
			got = append(got, tr)
			if len(got) == 2 {
				close(booksDone)
			}
			mu.Unlock()
		},
		Failed: func(err error) {
			mu.Lock()
			failed = append(failed, err)
			mu.Unlock()
		},
	})
	if err == nil || !strings.Contains(err.Error(), "binance book stream") {
		t.Fatalf("the stream ends with the books' connection: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if depths != 1 || len(got) != 2 || got[0].Symbol != "BTC-USDT-PERP" || got[1].Number != 2 || len(failed) != 1 {
		t.Fatalf("%d depth updates, trades %+v, failures %v", depths, got, failed)
	}
}

// The perpetuals' books follow updates every 100 ms for BTC and ETH and
// every 500 ms for the others (coin-margined design §3.4); spot's all
// every 100 ms.
func TestDepthStreams(t *testing.T) {
	one, ten := decimal.NewFromInt(1), decimal.NewFromInt(10)
	for _, c := range []struct {
		ref  ports.Reference
		want string
	}{
		{ports.Reference{Symbol: "SOL-USDT", Remote: "SOLUSDT", Multiplier: one}, "solusdt@depth@100ms"},
		{ports.Reference{Symbol: "BTC-USDT-PERP", Remote: "BTCUSDT", Multiplier: one, Market: ports.MarketUSDM}, "btcusdt@depth@100ms"},
		{ports.Reference{Symbol: "ETH-USD-PERP", Remote: "ETHUSD_PERP", Multiplier: one, Market: ports.MarketCoinM, ContractSize: ten}, "ethusd_perp@depth@100ms"},
		{ports.Reference{Symbol: "SOL-USDT-PERP", Remote: "SOLUSDT", Multiplier: one, Market: ports.MarketUSDM}, "solusdt@depth@500ms"},
		{ports.Reference{Symbol: "1000PEPE-USDT-PERP", Remote: "1000PEPEUSDT", Multiplier: one, Market: ports.MarketUSDM}, "1000pepeusdt@depth@500ms"},
		{ports.Reference{Symbol: "SOL-USD-PERP", Remote: "SOLUSD_PERP", Multiplier: one, Market: ports.MarketCoinM, ContractSize: ten}, "solusd_perp@depth@500ms"},
	} {
		if got := depthStream(c.ref); got != c.want {
			t.Errorf("%s: %s, want %s", c.ref.Symbol, got, c.want)
		}
	}
}
