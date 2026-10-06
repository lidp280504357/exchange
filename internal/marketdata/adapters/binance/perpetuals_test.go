package binance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/marketdata/ports"
)

// The contracts' own perpetuals (coin-M design §3.2): tickers, candles,
// books and trades on their futures market. A futures ticker has no best
// bid and ask; COIN-M quantities are contracts, whose quote amounts are
// their USD value (contracts × the contract size), not Binance's base
// asset volume.
func TestPerpetualsMarketData(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		paths = append(paths, r.URL.Path+"?"+q.Encode())
		switch r.URL.Path {
		case "/fapi/v1/ticker/24hr":
			_, _ = w.Write([]byte(`[{"symbol":"BTCUSDT","priceChange":"100","lastPrice":"86100.0","openPrice":"86000.0","highPrice":"86500.0",` +
				`"lowPrice":"85500.0","volume":"12000.5","quoteVolume":"1033243000.5","count":2000000,"closeTime":1791289900000},` +
				`{"symbol":"SOLUSDT","lastPrice":"1","openPrice":"1","highPrice":"1","lowPrice":"1","volume":"1","quoteVolume":"1","count":1,"closeTime":1}]`))
		case "/dapi/v1/ticker/24hr":
			_, _ = w.Write([]byte(`[{"symbol":"BTCUSD_PERP","pair":"BTCUSD","lastPrice":"86090.1","openPrice":"86000.0","highPrice":"86400.0",` +
				`"lowPrice":"85600.0","volume":"350000","baseVolume":"406.5","count":90000,"closeTime":1791289900000}]`))
		case "/dapi/v1/klines":
			_, _ = w.Write([]byte(`[[1791289860000,"86000.0","86100.0","85900.0","86050.0","1200",1791289919999,"1.3951",80,"600","0.7","0"]]`))
		case "/dapi/v1/depth":
			_, _ = w.Write([]byte(`{"lastUpdateId":77,"bids":[["86000.0","120"]],"asks":[["86000.1","45"]]}`))
		case "/stream":
			if q.Get("streams") != "btcusd_perp@depth@100ms/btcusd_perp@aggTrade" {
				http.Error(w, "bad streams", http.StatusBadRequest)
				return
			}
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer func() { _ = conn.CloseNow() }()
			for _, msg := range []string{
				`{"stream":"btcusd_perp@depth@100ms","data":{"e":"depthUpdate","E":1791289911656,"T":1791289911646,"s":"BTCUSD_PERP","ps":"BTCUSD","U":78,"u":80,"pu":77,"b":[["86000.0","130"]],"a":[]}}`,
				`{"stream":"btcusd_perp@aggTrade","data":{"e":"aggTrade","E":1791289911700,"a":555,"s":"BTCUSD_PERP","p":"86000.1","q":"7","f":1,"l":2,"T":1791289911699,"m":false}}`,
			} {
				_ = conn.Write(r.Context(), websocket.MessageText, []byte(msg))
			}
			_ = conn.Close(websocket.StatusGoingAway, "done")
		case "/market/stream":
			if q.Get("streams") != "btcusdt@kline_1m/btcusdt@ticker" {
				http.Error(w, "bad streams", http.StatusBadRequest)
				return
			}
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer func() { _ = conn.CloseNow() }()
			_ = conn.Write(r.Context(), websocket.MessageText, []byte(
				`{"stream":"btcusdt@ticker","data":{"e":"24hrTicker","E":1791289912000,"s":"BTCUSDT","p":"100","P":"0.116","w":"86050","c":"86100.0",`+
					`"Q":"0.01","o":"86000.0","h":"86500.0","l":"85500.0","v":"12000.5","q":"1033243000.5","O":1791203512000,"C":1791289912000,"F":1,"L":2,"n":2000000}}`))
			_ = conn.Close(websocket.StatusGoingAway, "done")
		default:
			http.Error(w, "unknown path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()
	ws := "ws" + strings.TrimPrefix(srv.URL, "http")
	s := New("", "", srv.Client()).WithFutures(srv.URL, ws).WithCoinFutures(srv.URL, ws)
	s.gap = 0
	ctx := context.Background()
	usdm := ports.Reference{Symbol: "BTC-USDT-PERP", Remote: "BTCUSDT", Multiplier: decimal.NewFromInt(1), Market: ports.MarketUSDM}
	coinm := ports.Reference{
		Symbol: "BTC-USD-PERP", Remote: "BTCUSD_PERP", Multiplier: decimal.NewFromInt(1), Market: ports.MarketCoinM, ContractSize: decimal.NewFromInt(100),
	}

	tk, err := s.Tickers(ctx, []ports.Reference{usdm})
	if err != nil || len(tk) != 1 || tk[0].Symbol != "BTC-USDT-PERP" || tk[0].Last.String() != "86100" || !tk[0].Bid.IsZero() ||
		tk[0].QuoteVolume.String() != "1033243000.5" || paths[0] != "/fapi/v1/ticker/24hr?" {
		t.Fatalf("USDⓈ-M tickers %+v %v %v", tk, err, paths)
	}
	tk, err = s.Tickers(ctx, []ports.Reference{coinm})
	if err != nil || len(tk) != 1 || tk[0].Volume.String() != "350000" || tk[0].QuoteVolume.String() != "35000000" {
		t.Fatalf("COIN-M tickers: volume in contracts, turnover in USD: %+v %v", tk, err)
	}
	if _, err := s.Tickers(ctx, []ports.Reference{usdm, coinm}); err == nil {
		t.Fatal("two markets in one request")
	}

	k, err := s.Klines(ctx, coinm, domain.Minute1, time.Time{}, 1)
	if err != nil || len(k) != 1 || k[0].Symbol != "BTC-USD-PERP" || k[0].Volume.String() != "1200" || k[0].QuoteVolume.String() != "120000" {
		t.Fatalf("COIN-M klines %+v %v", k, err)
	}

	last, bids, asks, err := s.DepthSnapshot(ctx, coinm)
	if err != nil || last != 77 || bids[0].Quantity.String() != "120" || asks[0].Price.String() != "86000.1" {
		t.Fatalf("COIN-M depth %d %+v %+v %v", last, bids, asks, err)
	}

	var depths int
	var trades []domain.Trade
	err = s.BookStream(ctx, []ports.Reference{coinm}, ports.BookHandlers{
		Depth: func(string, domain.DepthDiff) { depths++ },
		Trade: func(tr domain.Trade) { trades = append(trades, tr) },
	})
	if err == nil || depths != 1 || len(trades) != 1 {
		t.Fatalf("COIN-M books and trades on one connection: %d %+v %v", depths, trades, err)
	}
	if tr := trades[0]; tr.Symbol != "BTC-USD-PERP" || tr.Quantity.String() != "7" || tr.Quote.String() != "700" || tr.TakerSide != "BUY" {
		t.Fatalf("a COIN-M trade's quote amount is its contracts' USD value: %+v", tr)
	}

	var tickers []domain.Ticker
	err = s.Stream(ctx, []ports.Reference{usdm}, ports.StreamHandlers{Ticker: func(t domain.Ticker) { tickers = append(tickers, t) }})
	if err == nil || len(tickers) != 1 || tickers[0].Last.String() != "86100" || !tickers[0].Bid.IsZero() {
		t.Fatalf("a USDⓈ-M ticker under /market: %+v %v", tickers, err)
	}
}
