package binance

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketdata/ports"
)

var (
	btcPerp  = ports.FuturesMarket{Symbol: "BTC-USDT-PERP", Remote: "BTCUSDT", Pair: "BTCUSDT"}
	btcCoinM = ports.FuturesMarket{Symbol: "BTC-USD-PERP", CoinMargined: true, Remote: "BTCUSD_PERP", Pair: "BTCUSD", ContractSize: decimal.NewFromInt(100)}
)

// futuresServer answers each path with its body and records the queries.
func futuresServer(t *testing.T, bodies map[string]string) (*Futures, *[]url.URL) {
	t.Helper()
	var seen []url.URL
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, *r.URL)
		body, ok := bodies[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":-1121,"msg":"Invalid symbol."}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	f := NewFutures(srv.URL, "", srv.URL, "", srv.Client())
	f.pace.unpaced = true
	return f, &seen
}

func TestFuturesStatsUSDM(t *testing.T) {
	now := time.UnixMilli(1791285054000).UTC()
	f, seen := futuresServer(t, map[string]string{
		// As Binance answered on the test server, 2026-10-06.
		"/futures/data/openInterestHist": `[{"symbol":"BTCUSDT","sumOpenInterest":"94731.04600000","sumOpenInterestValue":"8157280897.95540000","CMCCirculatingSupply":"20094037.00000000","timestamp":1791284400000},` +
			`{"symbol":"BTCUSDT","sumOpenInterest":"94570.93300000","sumOpenInterestValue":"8133186783.42422100","CMCCirculatingSupply":"20094037.00000000","timestamp":1791284100000}]`,
		"/futures/data/takerlongshortRatio":       `[{"buySellRatio":"0.9752","sellVol":"157.7690","buyVol":"153.8640","timestamp":1791283500000}]`,
		"/futures/data/topLongShortPositionRatio": `[{"symbol":"BTCUSDT","longAccount":"0.6276","longShortRatio":"1.6855","shortAccount":"0.3724","timestamp":1791280800000}]`,
		"/futures/data/basis":                     `[{"indexPrice":"86189.99021739","contractType":"PERPETUAL","basisRate":"-0.0006","futuresPrice":"86137.10","annualizedBasisRate":"","basis":"-52.89021739","pair":"BTCUSDT","timestamp":1791283500000}]`,
		"/fapi/v1/fundingRate":                    `[{"symbol":"BTCUSDT","fundingTime":1791244800003,"fundingRate":"-0.00002485","markPrice":"85718.55362319","rateType":"Regular"}]`,
	})
	f.now = func() time.Time { return now }
	ctx := context.Background()

	got, err := f.Stats(ctx, btcPerp, ports.MetricOpenInterest, "5m", time.Time{}, 600)
	if err != nil {
		t.Fatal(err)
	}
	q := (*seen)[0].Query()
	if q.Get("symbol") != "BTCUSDT" || q.Get("period") != "5m" || q.Get("limit") != "500" || q.Has("startTime") || q.Has("pair") {
		t.Fatalf("the latest points are asked by symbol, at most 500: %v", q)
	}
	if len(got) != 2 || !got[0].At.Equal(time.UnixMilli(1791284100000)) || got[1].Symbol != "BTC-USDT-PERP" ||
		got[1].Metric != ports.MetricOpenInterest || got[1].Period != "5m" || len(got[1].Values) != 2 ||
		got[1].Values["open_interest"].String() != "94731.046" || got[1].Values["open_interest_value"].String() != "8157280897.9554" {
		t.Fatalf("open interest, oldest first: %+v", got)
	}

	// After a stored point: the page after it, limit periods long.
	after := time.UnixMilli(1791284100000).UTC()
	if got, err = f.Stats(ctx, btcPerp, ports.MetricOpenInterest, "5m", after, 100); err != nil {
		t.Fatal(err)
	}
	q = (*seen)[1].Query()
	if q.Get("startTime") != "1791284100001" || q.Get("endTime") != strconv.FormatInt(after.Add(500*time.Minute).UnixMilli(), 10) || q.Get("limit") != "100" {
		t.Fatalf("the page after a point: %v", q)
	}
	if len(got) != 1 || !got[0].At.Equal(time.UnixMilli(1791284400000)) {
		t.Fatalf("only the points after it: %+v", got)
	}
	// After a point older than the source keeps: the latest instead.
	if _, err = f.Stats(ctx, btcPerp, ports.MetricOpenInterest, "1d", now.Add(-31*24*time.Hour), 30); err != nil {
		t.Fatal(err)
	}
	if q = (*seen)[2].Query(); q.Has("startTime") || q.Get("period") != "1d" {
		t.Fatalf("a point older than 30 days: %v", q)
	}

	taker, err := f.Stats(ctx, btcPerp, ports.MetricTakerRatio, "5m", time.Time{}, 30)
	if err != nil || len(taker) != 1 || taker[0].Values["buy_sell_ratio"].String() != "0.9752" || taker[0].Values["buy_vol"].String() != "153.864" ||
		taker[0].Values["sell_vol"].String() != "157.769" {
		t.Fatalf("taker volumes: %+v, %v", taker, err)
	}
	ratio, err := f.Stats(ctx, btcPerp, ports.MetricTopLongShortPosition, "1h", time.Time{}, 30)
	if err != nil || len(ratio) != 1 || ratio[0].Values["long"].String() != "0.6276" || ratio[0].Values["short"].String() != "0.3724" ||
		ratio[0].Values["long_short_ratio"].String() != "1.6855" {
		t.Fatalf("the top traders' position ratio: %+v, %v", ratio, err)
	}
	basis, err := f.Stats(ctx, btcPerp, ports.MetricBasis, "5m", time.Time{}, 30)
	if q = (*seen)[len(*seen)-1].Query(); q.Get("pair") != "BTCUSDT" || q.Get("contractType") != "PERPETUAL" || q.Has("symbol") {
		t.Fatalf("the basis is asked by pair: %v", q)
	}
	if err != nil || len(basis) != 1 || len(basis[0].Values) != 4 || basis[0].Values["basis_rate"].String() != "-0.0006" ||
		basis[0].Values["futures_price"].String() != "86137.1" {
		t.Fatalf("the basis, without the empty annualized rate: %+v, %v", basis, err)
	}

	funding, err := f.Stats(ctx, btcPerp, ports.MetricFunding, "", time.Time{}, 5000)
	if err != nil {
		t.Fatal(err)
	}
	q = (*seen)[len(*seen)-1].Query()
	if q.Get("startTime") != strconv.FormatInt(now.Add(-30*24*time.Hour).UnixMilli(), 10) || q.Get("limit") != "1000" || q.Has("period") {
		t.Fatalf("funding rates from 30 days back: %v", q)
	}
	if len(funding) != 1 || funding[0].Period != "" || !funding[0].At.Equal(time.UnixMilli(1791244800003)) ||
		funding[0].Values["funding_rate"].String() != "-0.00002485" || funding[0].Values["mark_price"].String() != "85718.55362319" {
		t.Fatalf("funding: %+v", funding)
	}
	if _, err := f.Stats(ctx, btcPerp, ports.MetricFunding, "", time.UnixMilli(1791244800003), 100); err != nil {
		t.Fatal(err)
	}
	if q = (*seen)[len(*seen)-1].Query(); q.Get("startTime") != "1791244800004" {
		t.Fatalf("funding rates after a stored one: %v", q)
	}

	if _, err := f.Stats(ctx, btcPerp, ports.MetricOpenInterest, "30m", time.Time{}, 30); err == nil {
		t.Fatal("a period the panel does not have was asked for")
	}
	unknown := ports.FuturesMarket{Symbol: "NOPE-USDT-PERP", Remote: "NOPEUSDT", Pair: "NOPEUSDT"}
	if _, err := f.Stats(ctx, unknown, ports.MetricLongShortAccount, "5m", time.Time{}, 30); err == nil || !strings.Contains(err.Error(), "code -1121") {
		t.Fatalf("Binance's error: %v", err)
	}
}

func TestFuturesStatsCoinM(t *testing.T) {
	f, seen := futuresServer(t, map[string]string{
		"/futures/data/takerBuySellVol":           `[{"takerBuyVolValue":"0.1317","contractType":"PERPETUAL","takerSellVol":"13037","takerSellVolValue":"0.1513","takerBuyVol":"11344","pair":"BTCUSD","timestamp":1791283800000}]`,
		"/futures/data/topLongShortPositionRatio": `[{"shortPosition":"0.2946","longShortRatio":"2.3949","longPosition":"0.7054","pair":"BTCUSD","timestamp":1791284100000}]`,
		"/futures/data/openInterestHist":          `[{"contractType":"PERPETUAL","sumOpenInterest":"2909783.00000000","sumOpenInterestValue":"3377.42432722","pair":"BTCUSD","timestamp":1791284100000}]`,
		"/dapi/v1/fundingRate":                    `[{"symbol":"BTCUSD_PERP","fundingTime":1791273600001,"fundingRate":"0.00005307","markPrice":"85503.55145282","rateType":"Regular"}]`,
	})
	ctx := context.Background()
	taker, err := f.Stats(ctx, btcCoinM, ports.MetricTakerRatio, "5m", time.Time{}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if q := (*seen)[0].Query(); q.Get("pair") != "BTCUSD" || q.Get("contractType") != "PERPETUAL" {
		t.Fatalf("COIN-M taker volumes by pair: %v", q)
	}
	// The ratio from the volumes in contracts, as USDⓈ-M's (4 places).
	if len(taker) != 1 || taker[0].Values["buy_vol"].String() != "11344" || taker[0].Values["sell_vol"].String() != "13037" ||
		taker[0].Values["buy_sell_ratio"].String() != "0.8701" || len(taker[0].Values) != 3 {
		t.Fatalf("COIN-M taker volumes: %+v", taker)
	}
	ratio, err := f.Stats(ctx, btcCoinM, ports.MetricTopLongShortPosition, "5m", time.Time{}, 30)
	if err != nil || len(ratio) != 1 || ratio[0].Values["long"].String() != "0.7054" || ratio[0].Values["short"].String() != "0.2946" {
		t.Fatalf("COIN-M names the position ratio's sides by position: %+v, %v", ratio, err)
	}
	if q := (*seen)[1].Query(); q.Get("pair") != "BTCUSD" || q.Has("contractType") || q.Has("symbol") {
		t.Fatalf("COIN-M ratios by pair alone: %v", q)
	}
	oi, err := f.Stats(ctx, btcCoinM, ports.MetricOpenInterest, "5m", time.Time{}, 30)
	// Binance values it in BTC (3377.42432722); the panel in USD, the
	// contracts' face value.
	if err != nil || len(oi) != 1 || oi[0].Values["open_interest"].String() != "2909783" || oi[0].Values["open_interest_value"].String() != "290978300" {
		t.Fatalf("COIN-M open interest in contracts and USD: %+v, %v", oi, err)
	}
	funding, err := f.Stats(ctx, btcCoinM, ports.MetricFunding, "", time.Time{}, 100)
	if err != nil || len(funding) != 1 || funding[0].Values["funding_rate"].String() != "0.00005307" {
		t.Fatalf("COIN-M funding: %+v, %v", funding, err)
	}
	if q := (*seen)[3].Query(); (*seen)[3].Path != "/dapi/v1/fundingRate" || q.Get("symbol") != "BTCUSD_PERP" {
		t.Fatalf("COIN-M funding by the contract's symbol: %v %v", (*seen)[3].Path, q)
	}
}

func TestFuturesPerpetualsAndOpenInterest(t *testing.T) {
	f, seen := futuresServer(t, map[string]string{
		"/fapi/v1/exchangeInfo": `{"symbols":[{"symbol":"BTCUSDT","pair":"BTCUSDT","contractType":"PERPETUAL","status":"TRADING"},` +
			`{"symbol":"BTCUSDT_261225","pair":"BTCUSDT","contractType":"CURRENT_QUARTER","status":"TRADING"},` +
			`{"symbol":"OLDUSDT","pair":"OLDUSDT","contractType":"PERPETUAL","status":"SETTLING"}]}`,
		"/dapi/v1/exchangeInfo": `{"symbols":[{"symbol":"BTCUSD_PERP","pair":"BTCUSD","contractType":"PERPETUAL","contractStatus":"TRADING","contractSize":100},` +
			`{"symbol":"ETHUSD_PERP","pair":"ETHUSD","contractType":"PERPETUAL","contractStatus":"TRADING","contractSize":10},` +
			`{"symbol":"BTCUSD_261225","pair":"BTCUSD","contractType":"CURRENT_QUARTER","contractStatus":"TRADING","contractSize":100}]}`,
		"/fapi/v1/openInterest": `{"symbol":"BTCUSDT","openInterest":"94791.893","time":1791284697161}`,
		"/dapi/v1/openInterest": `{"symbol":"BTCUSD_PERP","pair":"BTCUSD","openInterest":"12668208","contractType":"PERPETUAL","time":1791284692616}`,
	})
	ctx := context.Background()
	usdm, err := f.Perpetuals(ctx, false)
	if err != nil || len(usdm) != 1 || usdm[0].Remote != "BTCUSDT" || usdm[0].Pair != "BTCUSDT" || !usdm[0].ContractSize.IsZero() {
		t.Fatalf("USDⓈ-M perpetuals in trading: %+v, %v", usdm, err)
	}
	coinm, err := f.Perpetuals(ctx, true)
	if err != nil || len(coinm) != 2 || coinm[0].Remote != "BTCUSD_PERP" || coinm[0].Pair != "BTCUSD" || coinm[0].ContractSize.String() != "100" ||
		coinm[1].ContractSize.String() != "10" {
		t.Fatalf("COIN-M perpetuals with their face values: %+v, %v", coinm, err)
	}
	oi, at, err := f.OpenInterest(ctx, btcPerp)
	if err != nil || oi.String() != "94791.893" || !at.Equal(time.UnixMilli(1791284697161)) {
		t.Fatalf("open interest: %v %v %v", oi, at, err)
	}
	oi, _, err = f.OpenInterest(ctx, btcCoinM)
	if err != nil || oi.String() != "12668208" {
		t.Fatalf("COIN-M open interest: %v %v", oi, err)
	}
	if last := (*seen)[len(*seen)-1]; last.Path != "/dapi/v1/openInterest" || last.Query().Get("symbol") != "BTCUSD_PERP" {
		t.Fatalf("COIN-M open interest by the contract's symbol: %v", last)
	}
}

func TestFuturesBackOff(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"symbol":"BTCUSDT","openInterest":"1","time":1}`))
	}))
	defer srv.Close()
	f := NewFutures(srv.URL, "", "http://coinm.invalid", "", srv.Client())
	f.pace.unpaced = true
	if _, _, err := f.OpenInterest(context.Background(), btcPerp); err == nil || !strings.Contains(err.Error(), "backing off") {
		t.Fatalf("over the limit: %v", err)
	}
	// The host's every lane waits for Retry-After.
	start := time.Now()
	_, _ = f.Perpetuals(context.Background(), false)
	if waited := time.Since(start); waited < 900*time.Millisecond {
		t.Fatalf("the next request went out %s after a 429 asking for a second", waited)
	}
	// The other host is not held.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := f.pace.wait(ctx, "http://coinm.invalid", "http://coinm.invalid/dapi/v1/openInterest", 0); err != nil {
		t.Fatalf("the COIN-M host waited for the USDⓈ-M one's back-off: %v", err)
	}
}

func TestFuturesRefusalsHoldTheHost(t *testing.T) {
	for _, tc := range []struct {
		status int
		held   time.Duration
	}{{http.StatusForbidden, time.Minute}, {http.StatusUnavailableForLegalReasons, 10 * time.Minute}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.status) }))
		f := NewFutures(srv.URL, "", srv.URL, "", srv.Client())
		now := time.Date(2026, 10, 6, 13, 0, 0, 0, time.UTC)
		f.pace.now = func() time.Time { return now }
		if _, _, err := f.OpenInterest(context.Background(), btcPerp); err == nil || !strings.Contains(err.Error(), "backing off") {
			t.Fatalf("HTTP %d: %v", tc.status, err)
		}
		if got := f.pace.hold[srv.URL]; !got.Equal(now.Add(tc.held)) {
			t.Fatalf("HTTP %d holds the host until %v, want %s on", tc.status, got, tc.held)
		}
		srv.Close()
	}
}

// The six futures/data endpoints of a host share one budget: Binance does
// not say whether its limit is per endpoint (review EK ③).
func TestFuturesStatsShareAHostBudget(t *testing.T) {
	f, seen := futuresServer(t, map[string]string{
		"/futures/data/openInterestHist":            `[]`,
		"/futures/data/globalLongShortAccountRatio": `[]`,
		"/futures/data/basis":                       `[]`,
		"/fapi/v1/openInterest":                     `{"symbol":"BTCUSDT","openInterest":"1","time":1}`,
	})
	f.pace.unpaced = false
	ctx := context.Background()
	start := time.Now()
	for _, metric := range []string{ports.MetricOpenInterest, ports.MetricLongShortAccount, ports.MetricBasis} {
		if _, err := f.Stats(ctx, btcPerp, metric, "5m", time.Time{}, 30); err != nil {
			t.Fatal(err)
		}
	}
	if took := time.Since(start); took < 2*statsGap-50*time.Millisecond {
		t.Fatalf("three endpoints' requests took %s, want two gaps of %s", took, statsGap)
	}
	// Another lane is not held by them.
	start = time.Now()
	if _, _, err := f.OpenInterest(ctx, btcPerp); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > statsGap/2 {
		t.Fatalf("the open interest waited %s behind the statistics", took)
	}
	if len(*seen) != 4 {
		t.Fatalf("%d requests", len(*seen))
	}
}

func TestForcedOrders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/market/ws/!forceOrder@arr" {
			http.Error(w, "bad stream", http.StatusBadRequest)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for _, msg := range []string{
			// The documented examples, USDⓈ-M (with the fields Binance
			// sends since, 2026-10-06) and COIN-M.
			`{"e":"forceOrder","E":1568014460893,"o":{"s":"BTCUSDT","S":"SELL","o":"LIMIT","f":"IOC","q":"0.014","p":"9910","ap":"9910","X":"FILLED","l":"0.014","z":"0.014","T":1568014460893,"ps":"BTCUSDT","st":1}}`,
			`{"e":"forceOrder","E":1591154240950,"o":{"s":"BTCUSD_PERP","ps":"BTCUSD","S":"BUY","o":"LIMIT","f":"IOC","q":"3","p":"9425.5","ap":"9496.5","X":"FILLED","l":"1","z":"3","T":1591154240949}}`,
			`{"e":"forceOrder","o":{"s":"ETHUSDT","S":"SELL","p":"bad","ap":"1","z":"1","T":1}}`,
			`{"e":"forceOrder","o":{"s":"ETHUSDT","S":"SELL","p":"1","ap":"1","z":"0","T":1}}`,
			`{"result":null,"id":1}`,
		} {
			_ = conn.Write(r.Context(), websocket.MessageText, []byte(msg))
		}
		_ = conn.Close(websocket.StatusGoingAway, "24 hours are up")
	}))
	defer srv.Close()
	ws := "ws" + strings.TrimPrefix(srv.URL, "http")
	f := NewFutures("", ws, "", ws, srv.Client())
	var got []ports.ForcedOrder
	err := f.ForcedOrders(context.Background(), false, func(o ports.ForcedOrder) { got = append(got, o) })
	if err == nil || errors.Is(err, ports.ErrQuiet) {
		t.Fatalf("a closed stream is an error for the caller to reconnect: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("the two good orders, got %+v", got)
	}
	if o := got[0]; o.Remote != "BTCUSDT" || o.Side != "SELL" || o.Price.String() != "9910" || o.Filled.String() != "0.014" ||
		!o.At.Equal(time.UnixMilli(1568014460893)) {
		t.Fatalf("USDⓈ-M order: %+v", o)
	}
	if o := got[1]; o.Remote != "BTCUSD_PERP" || o.Side != "BUY" || o.AvgPrice.String() != "9496.5" || o.Filled.String() != "3" {
		t.Fatalf("COIN-M order, filled in contracts: %+v", o)
	}

	quiet := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		<-r.Context().Done()
	}))
	defer quiet.Close()
	f = NewFutures("", "", "", "ws"+strings.TrimPrefix(quiet.URL, "http"), quiet.Client())
	f.idle = 200 * time.Millisecond
	if err := f.ForcedOrders(context.Background(), true, func(ports.ForcedOrder) {}); !errors.Is(err, ports.ErrQuiet) {
		t.Fatalf("a quiet stream: %v", err)
	}
}
