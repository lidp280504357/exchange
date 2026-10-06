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

var (
	markBTC    = ports.Reference{Symbol: "BTC-USDT-PERP", Remote: "BTCUSDT", Multiplier: decimal.NewFromInt(1), Market: ports.MarketUSDM}
	markETH    = ports.Reference{Symbol: "ETH-USDT-PERP", Remote: "ETHUSDT", Multiplier: decimal.NewFromInt(1), Market: ports.MarketUSDM}
	markBTCUSD = ports.Reference{
		Symbol: "BTC-USD-PERP", Remote: "BTCUSD_PERP", Multiplier: decimal.NewFromInt(1), Market: ports.MarketCoinM, ContractSize: decimal.NewFromInt(100),
	}
)

// Mark price updates as Binance sends them: the mark "p" and the
// estimated settlement price "P" (and "e"/"E") differ only in case; a
// delivery contract's rate is "".
func TestMarkStreamReadsMarkPrices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// USDⓈ-M serves mark prices under /market only; COIN-M under /stream.
		if r.URL.Path == "/market/stream" && r.URL.Query().Get("streams") == "btcusdt@markPrice@1s/ethusdt@markPrice@1s" {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer func() { _ = conn.CloseNow() }()
			_ = conn.Write(r.Context(), websocket.MessageText, []byte(
				`{"stream":"ethusdt@markPrice@1s","data":{"e":"markPriceUpdate","E":1791289863000,"s":"ETHUSDT","p":"2706.47","ap":"2706.47","P":"2710.1","i":"2707.01","r":"-0.00000999","T":1791302400000,"st":1}}`))
			_ = conn.Close(websocket.StatusGoingAway, "done")
			return
		}
		if r.URL.Path != "/stream" || r.URL.Query().Get("streams") != "btcusd_perp@markPrice@1s" {
			http.Error(w, "bad streams", http.StatusBadRequest)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for _, msg := range []string{
			`{"stream":"btcusd_perp@markPrice@1s","data":{"e":"markPriceUpdate","E":1791273601000,"s":"BTCUSD_PERP","p":"85503.55145282","P":"85510.10000000","i":"85500.12345678","r":"0.00005307","T":1791302400000}}`,
			`{"result":null,"id":1}`,
			`{"stream":"btcusd_perp@markPrice@1s","data":{"e":"markPriceUpdate","E":1791273602000,"s":"BTCUSD_PERP","p":"bad","i":"1","r":"0.0001","T":1}}`,
			`{"stream":"btcusd_perp@markPrice@1s","data":{"e":"markPriceUpdate","E":1791273603000,"s":"BTCUSD_PERP","p":"85504","P":"0","i":"85501","r":"","T":0}}`,
			`{"stream":"ethusd_perp@markPrice@1s","data":{"e":"markPriceUpdate","E":1791273604000,"s":"ETHUSD_PERP","p":"2706","i":"2705","r":"0.0001","T":1791302400000}}`,
		} {
			_ = conn.Write(r.Context(), websocket.MessageText, []byte(msg))
		}
		_ = conn.Close(websocket.StatusGoingAway, "24 hours are up")
	}))
	defer srv.Close()
	s := New("", "", srv.Client()).WithCoinFutures(srv.URL, "ws"+strings.TrimPrefix(srv.URL, "http"))
	var got []domain.ReferenceMark
	err := s.MarkStream(context.Background(), []ports.Reference{markBTCUSD}, func(m domain.ReferenceMark) { got = append(got, m) })
	if err == nil {
		t.Fatal("a closed stream is an error for the caller to reconnect")
	}
	if len(got) != 2 {
		t.Fatalf("marks %+v", got)
	}
	m := got[0]
	if m.Symbol != "BTC-USD-PERP" || m.Mark.String() != "85503.55145282" || m.Index.String() != "85500.12345678" || !m.HasRate ||
		m.FundingRate.String() != "0.00005307" || !m.NextFunding.Equal(time.UnixMilli(1791302400000).UTC()) ||
		!m.At.Equal(time.UnixMilli(1791273601000).UTC()) {
		t.Fatalf("mark %+v", m)
	}
	if m := got[1]; m.HasRate || !m.NextFunding.IsZero() || m.Mark.String() != "85504" {
		t.Fatalf("no rate: %+v", m)
	}
	if err := New("", "", srv.Client()).MarkStream(context.Background(), []ports.Reference{markBTCUSD}, nil); err == nil {
		t.Fatal("COIN-M without its endpoints")
	}

	usdm := New("", "", srv.Client()).WithFutures(srv.URL, "ws"+strings.TrimPrefix(srv.URL, "http"))
	got = nil
	err = usdm.MarkStream(context.Background(), []ports.Reference{markBTC, markETH}, func(m domain.ReferenceMark) { got = append(got, m) })
	if err == nil || len(got) != 1 || got[0].Symbol != "ETH-USDT-PERP" || got[0].Mark.String() != "2706.47" || got[0].FundingRate.String() != "-0.00000999" {
		t.Fatalf("USDⓈ-M under /market: %+v %v", got, err)
	}
}

// USDⓈ-M answers every symbol's settlements in one request; a full page
// may have left rows out, and COIN-M wants a symbol: one request each.
func TestSettledFunding(t *testing.T) {
	end := time.UnixMilli(1791273600000).UTC()
	var paths []string
	full := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		paths = append(paths, r.URL.Path+"?"+q.Get("symbol"))
		if q.Get("startTime") != "1791273600000" || q.Get("endTime") != "1791273660000" || q.Get("limit") != "1000" {
			http.Error(w, "bad window", http.StatusBadRequest)
			return
		}
		switch {
		case r.URL.Path == "/fapi/v1/fundingRate" && q.Get("symbol") == "" && !full:
			_, _ = w.Write([]byte(`[{"symbol":"0GUSDT","fundingTime":1791273600001,"fundingRate":"0.00005000","markPrice":"0.30690000","rateType":"Regular"},` +
				`{"symbol":"BTCUSDT","fundingTime":1791273600001,"fundingRate":"-0.00001592","markPrice":"85514.00007496","rateType":"Regular"}]`))
		case r.URL.Path == "/fapi/v1/fundingRate" && q.Get("symbol") == "":
			rows := make([]string, fundingPage)
			for i := range rows {
				rows[i] = `{"symbol":"XUSDT","fundingTime":1791273600001,"fundingRate":"0.0001","markPrice":"1"}`
			}
			_, _ = w.Write([]byte("[" + strings.Join(rows, ",") + "]"))
		case r.URL.Path == "/fapi/v1/fundingRate" && q.Get("symbol") == "ETHUSDT":
			_, _ = w.Write([]byte(`[{"symbol":"ETHUSDT","fundingTime":1791273600001,"fundingRate":"-0.00002085","markPrice":"2706.47884924"}]`))
		case r.URL.Path == "/fapi/v1/fundingRate" && q.Get("symbol") == "BTCUSDT":
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/dapi/v1/fundingRate" && q.Get("symbol") == "BTCUSD_PERP":
			_, _ = w.Write([]byte(`[{"symbol":"BTCUSD_PERP","fundingTime":1791273600001,"fundingRate":"0.00005307","markPrice":"85503.55145282"}]`))
		default:
			http.Error(w, `{"code":-1102,"msg":"Mandatory parameter 'symbol' was not sent"}`, http.StatusBadRequest)
		}
	}))
	defer srv.Close()
	s := New("", "", srv.Client()).WithFutures(srv.URL, "wss://unused").WithCoinFutures(srv.URL, "wss://unused")
	s.gap = 0
	ctx := context.Background()
	list, err := s.SettledFunding(ctx, []ports.Reference{markBTC, markETH}, end, end.Add(time.Minute))
	if err != nil || len(list) != 1 || paths[0] != "/fapi/v1/fundingRate?" {
		t.Fatalf("one request for every USDⓈ-M symbol: %+v %v %v", list, err, paths)
	}
	if f := list[0]; f.Symbol != "BTC-USDT-PERP" || f.Rate.String() != "-0.00001592" || f.Mark.String() != "85514.00007496" ||
		!f.FundingTime.Equal(end.Add(time.Millisecond)) {
		t.Fatalf("settled %+v", f)
	}

	paths, full = nil, true
	list, err = s.SettledFunding(ctx, []ports.Reference{markBTC, markETH}, end, end.Add(time.Minute))
	if err != nil || len(list) != 1 || list[0].Symbol != "ETH-USDT-PERP" || len(paths) != 3 ||
		paths[1] != "/fapi/v1/fundingRate?BTCUSDT" || paths[2] != "/fapi/v1/fundingRate?ETHUSDT" {
		t.Fatalf("a full page: one request each: %+v %v %v", list, err, paths)
	}

	paths = nil
	list, err = s.SettledFunding(ctx, []ports.Reference{markBTCUSD}, end, end.Add(time.Minute))
	if err != nil || len(list) != 1 || list[0].Symbol != "BTC-USD-PERP" || list[0].Rate.String() != "0.00005307" ||
		len(paths) != 1 || paths[0] != "/dapi/v1/fundingRate?BTCUSD_PERP" {
		t.Fatalf("COIN-M: %+v %v %v", list, err, paths)
	}
}
