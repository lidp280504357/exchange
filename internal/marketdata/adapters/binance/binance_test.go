package binance

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

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
			`{"stream":"btcusdt@kline_1m","data":{"e":"kline","s":"BTCUSDT","k":{"t":1790617140000,"s":"BTCUSDT","o":"83942.95","c":"83931.73","h":"83942.95","l":"83923.18","v":"2.957","n":771,"x":false,"q":"248203.8"}}}`,
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
