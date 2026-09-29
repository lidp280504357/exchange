package binance

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketdata/domain"
)

func TestBackfillPages(t *testing.T) {
	start := time.Now().Add(-1500 * time.Minute).Truncate(time.Minute)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v3/klines" || r.URL.Query().Get("symbol") != "BTCUSDT" || r.URL.Query().Get("interval") != "1m" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		from, _ := strconv.ParseInt(r.URL.Query().Get("startTime"), 10, 64)
		var rows []string
		for i := range 1000 {
			open := from + int64(i)*60000
			if open > time.Now().UnixMilli() {
				break
			}
			rows = append(rows, fmt.Sprintf(`[%d,"83900.10000000","83950.00000000","83850.00000000","83920.00000000","1.50000000",%d,"125880.00000000",42,"0","0","0"]`, open, open+59999))
		}
		_, _ = w.Write([]byte("[" + strings.Join(rows, ",") + "]"))
	}))
	defer srv.Close()
	s := New(srv.URL, "", srv.Client())
	s.gap = 0
	candles, err := s.Backfill(context.Background(), "BTC-USDT", start)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(candles) < 1500 || !candles[0].OpenTime.Equal(start.UTC()) || candles[0].Symbol != "BTC-USDT" {
		t.Fatalf("%d calls, %d candles, first %+v", calls, len(candles), candles[0])
	}
	c := candles[0]
	if c.Open.String() != "83900.1" || c.Close.String() != "83920" || c.QuoteVolume.String() != "125880" || c.Trades != 42 || c.Interval != domain.Minute1 {
		t.Fatalf("candle %+v", c)
	}
}

func TestStreamReadsKlines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("streams") != "btcusdt@kline_1m/ethusdt@kline_1m" {
			http.Error(w, "bad streams", http.StatusBadRequest)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for _, msg := range []string{
			// A message as Binance sends it, with keys that differ only in case.
			`{"stream":"btcusdt@kline_1m","data":{"e":"kline","E":1790617150042,"s":"BTCUSDT","k":{"t":1790617140000,"T":1790617199999,"s":"BTCUSDT","i":"1m","f":6719969009,"L":6719969026,"o":"83942.95000000","c":"83931.73000000","h":"83942.95000000","l":"83923.18000000","v":"2.95700000","n":771,"x":false,"q":"248203.83491790","V":"0.71560000","Q":"60063.21030660","B":"0"}}}`,
			`{"result":null,"id":1}`,
			`{"stream":"ethusdt@kline_1m","data":{"e":"kline","s":"ETHUSDT","k":{"t":1790617140000,"s":"ETHUSDT","o":"bad","c":"1","h":"1","l":"1","v":"1","n":1,"q":"1"}}}`,
		} {
			_ = conn.Write(r.Context(), websocket.MessageText, []byte(msg))
		}
		_ = conn.Close(websocket.StatusGoingAway, "24 hours are up")
	}))
	defer srv.Close()
	s := New("", "ws"+strings.TrimPrefix(srv.URL, "http"), srv.Client())
	var got []domain.Candle
	err := s.Stream(context.Background(), []string{"BTC-USDT", "ETH-USDT"}, func(c domain.Candle) { got = append(got, c) })
	if err == nil {
		t.Fatal("a closed stream is an error for the caller to reconnect")
	}
	if len(got) != 1 || got[0].Symbol != "BTC-USDT" || got[0].Close.String() != "83931.73" || got[0].Trades != 771 ||
		!got[0].OpenTime.Equal(time.UnixMilli(1790617140000).UTC()) {
		t.Fatalf("candles %+v", got)
	}
}

func TestStreamEndsWhenSilent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		<-r.Context().Done() // connected, but nothing arrives
	}))
	defer srv.Close()
	s := New("", "ws"+strings.TrimPrefix(srv.URL, "http"), srv.Client())
	s.idle = 200 * time.Millisecond
	start := time.Now()
	err := s.Stream(context.Background(), []string{"BTC-USDT"}, func(domain.Candle) {})
	if err == nil || !strings.Contains(err.Error(), "nothing received") {
		t.Fatalf("got %v", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("a silent stream took %s to end", took)
	}
}

func TestKlinesOfAnyInterval(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		_, _ = w.Write([]byte(`[[1790640000000,"60000","60500","59900","60400","12.5",1790654399999,"753000.1",1234,"0","0","0"]]`))
	}))
	defer srv.Close()
	s := New(srv.URL, "", srv.Client())
	s.gap = 0
	to := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	list, err := s.Klines(context.Background(), "BTC-USDT", domain.Hour4, to, 300)
	if err != nil {
		t.Fatal(err)
	}
	if got.Get("symbol") != "BTCUSDT" || got.Get("interval") != "4h" || got.Get("limit") != "300" || got.Get("endTime") != "1790683200000" {
		t.Fatalf("query %v", got)
	}
	if len(list) != 1 || list[0].Interval != domain.Hour4 || list[0].Symbol != "BTC-USDT" || !list[0].Close.Equal(decimal.RequireFromString("60400")) ||
		list[0].Trades != 1234 {
		t.Fatalf("klines %+v", list)
	}
}

func TestAnAbandonedRequestFreesItsTurn(t *testing.T) {
	s := New("http://unused", "", http.DefaultClient)
	s.gap = 200 * time.Millisecond
	if err := s.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := time.Now()
	// Five callers give up while waiting for their turn.
	for range 5 {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		if err := s.wait(ctx); err == nil {
			t.Fatal("a caller whose context ended got a turn")
		}
		cancel()
	}
	// The next one waits only for the rest of the gap after the first.
	if err := s.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(first); took < 190*time.Millisecond || took > 600*time.Millisecond {
		t.Fatalf("the next request went out %s after the first, want about the gap", took)
	}
}
