package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketdata/application"
	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/marketdata/ports"
)

type statsFake struct {
	markets  map[string]ports.FuturesMarket
	interest map[string]application.OpenInterest
	asked    []string
	// unknown: the reference market's list is not read yet.
	unknown bool
}

func (f *statsFake) Series(_ context.Context, symbol, metric, period string, limit int) ([]ports.FuturesStat, error) {
	f.asked = append(f.asked, strings.Join([]string{symbol, metric, period}, " "))
	if symbol == "ASTRA-USDT-PERP" {
		return nil, application.ErrNoFuturesData
	}
	at := time.Date(2026, 10, 6, 11, 10, 0, 0, time.UTC)
	if metric == ports.MetricFunding {
		return []ports.FuturesStat{{
			Symbol: symbol, Metric: metric, At: time.UnixMilli(1791244800003).UTC(),
			Values: map[string]decimal.Decimal{"funding_rate": decimal.RequireFromString("-0.00002485")},
		}}, nil
	}
	return []ports.FuturesStat{{
		Symbol: symbol, Metric: metric, Period: period, At: at,
		Values: map[string]decimal.Decimal{"open_interest": decimal.RequireFromString("94731.046")},
	}}[:min(limit, 1)], nil
}

func (f *statsFake) Liquidations(_ context.Context, symbol string, limit int) ([]ports.Liquidation, error) {
	f.asked = append(f.asked, symbol+" liquidations")
	if symbol == "ASTRA-USDT-PERP" {
		return nil, application.ErrNoFuturesData
	}
	d := decimal.RequireFromString
	return []ports.Liquidation{{
		Symbol: symbol, PositionSide: "LONG", Price: d("9910"), AvgPrice: d("9912"), Quantity: d("0.014"), ValueUSD: d("138.768"),
		At: time.UnixMilli(1568014460893).UTC(),
	}}[:min(limit, 1)], nil
}

func (f *statsFake) Market(symbol string) (ports.FuturesMarket, bool, bool) {
	m, ok := f.markets[symbol]
	return m, !f.unknown, ok
}

func (f *statsFake) OpenInterestNow(symbol string) (application.OpenInterest, bool) {
	oi, ok := f.interest[symbol]
	return oi, ok
}

type marksFake map[string]application.MarkPrice

func (m marksFake) Latest(symbol string) (application.MarkPrice, bool) {
	p, ok := m[symbol]
	return p, ok
}

type tickersFake []domain.Ticker

func (t tickersFake) All(context.Context) ([]domain.Ticker, error) { return t, nil }

type contractsFake []ports.FuturesContract

func (c contractsFake) FuturesContracts(context.Context) ([]ports.FuturesContract, error) {
	return c, nil
}

func futuresRouter() (*chi.Mux, *statsFake) {
	d := decimal.RequireFromString
	btc := ports.FuturesMarket{Symbol: "BTC-USDT-PERP", Remote: "BTCUSDT", Pair: "BTCUSDT"}
	coin := ports.FuturesMarket{Symbol: "BTC-USD-PERP", CoinMargined: true, Remote: "BTCUSD_PERP", Pair: "BTCUSD", ContractSize: d("100")}
	stats := &statsFake{
		markets: map[string]ports.FuturesMarket{btc.Symbol: btc, coin.Symbol: coin},
		interest: map[string]application.OpenInterest{
			btc.Symbol:  {Market: btc, Quantity: d("94791.893")},
			coin.Symbol: {Market: coin, Quantity: d("12668208")},
		},
	}
	next := time.Date(2026, 10, 6, 16, 0, 0, 0, time.UTC)
	h := &FuturesData{
		Stats: stats,
		Marks: marksFake{
			btc.Symbol:        {Symbol: btc.Symbol, Mark: d("86000.5"), Index: d("86020"), FundingRate: d("0.0001"), NextFunding: next},
			"ASTRA-USDT-PERP": {Symbol: "ASTRA-USDT-PERP", Mark: d("0.25"), Index: d("0.25"), FundingRate: d("0"), NextFunding: next},
		},
		Tickers: tickersFake{{Symbol: btc.Symbol, Open: d("85000"), Change: d("0.01176"), QuoteVolume: d("123456789.5")}},
		Contracts: contractsFake{
			{Symbol: "BTC-USDT-PERP", ReferenceSymbol: "BTCUSDT"},
			{Symbol: "ASTRA-USDT-PERP"},
			{Symbol: "BTC-USD-PERP", CoinMargined: true, ReferenceSymbol: "BTCUSD_PERP", ContractSize: d("100")},
		},
	}
	r := chi.NewRouter()
	h.Routes(r)
	return r, stats
}

func get(r http.Handler, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil))
	return rec
}

func TestFuturesDataSeries(t *testing.T) {
	r, stats := futuresRouter()
	rec := get(r, "/v1/market/btc-usdt-perp/futures-data?metric=open_interest&period=1h&limit=10")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "public, max-age=30" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var body struct {
		Symbol, Metric string
		Period         *string
		Points         []struct {
			Time   string
			Values map[string]string
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Symbol != "BTC-USDT-PERP" || body.Metric != "open_interest" || body.Period == nil || *body.Period != "1h" ||
		len(body.Points) != 1 || body.Points[0].Time != "2026-10-06T11:10:00Z" || body.Points[0].Values["open_interest"] != "94731.046" {
		t.Fatalf("series: %s", rec.Body)
	}
	// The period defaults to 5m; funding has none.
	get(r, "/v1/market/BTC-USDT-PERP/futures-data?metric=basis")
	rec = get(r, "/v1/market/BTC-USDT-PERP/futures-data?metric=funding")
	if !strings.Contains(rec.Body.String(), `"period":null`) || !strings.Contains(rec.Body.String(), `"time":"2026-10-06T00:00:00.003Z"`) {
		t.Fatalf("funding: %s", rec.Body)
	}
	// A coin-margined contract is listed too.
	if rec := get(r, "/v1/market/BTC-USD-PERP/futures-data?metric=open_interest"); rec.Code != http.StatusOK {
		t.Fatalf("a coin-margined contract: %d %s", rec.Code, rec.Body)
	}
	if got := stats.asked; len(got) != 4 || got[1] != "BTC-USDT-PERP basis 5m" {
		t.Fatalf("asked %v", got)
	}
	if rec := get(r, "/v1/market/ETH-USDT-PERP/futures-data?metric=basis"); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "COMMON_NOT_FOUND") {
		t.Fatalf("a contract that is not listed: %d %s", rec.Code, rec.Body)
	}
	if rec := get(r, "/v1/market/ASTRA-USDT-PERP/futures-data?metric=basis"); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "MARKET_NO_FUTURES_DATA") {
		t.Fatalf("the platform coin's perpetual: %d %s", rec.Code, rec.Body)
	}
}

func TestFuturesDataOverview(t *testing.T) {
	r, _ := futuresRouter()
	rec := get(r, "/v1/market/futures/overview")
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var body struct {
		Contracts []overviewJSON `json:"contracts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	by := map[string]overviewJSON{}
	for _, c := range body.Contracts {
		by[c.Symbol] = c
	}
	if len(body.Contracts) != 3 || body.Contracts[0].Symbol != "ASTRA-USDT-PERP" {
		t.Fatalf("every contract, by symbol: %s", rec.Body)
	}
	str := func(p *string) string {
		if p == nil {
			return "null"
		}
		return *p
	}
	btc := by["BTC-USDT-PERP"]
	if str(btc.MarkPrice) != "86000.5" || str(btc.FundingRate) != "0.0001" || str(btc.NextFundingTime) != "2026-10-06T16:00:00Z" ||
		str(btc.OpenInterest) != "94791.893" || str(btc.OpenInterestValue) != "8152150193.95" || str(btc.Change) != "0.01176" ||
		str(btc.QuoteVolume) != "123456789.5" || !btc.FuturesData {
		t.Fatalf("BTC-USDT-PERP: %+v", btc)
	}
	// COIN-M: contracts at 100 USD; no mark or ticker yet.
	coin := by["BTC-USD-PERP"]
	if str(coin.OpenInterest) != "12668208" || str(coin.OpenInterestValue) != "1266820800" || coin.MarkPrice != nil || coin.Change != nil || !coin.FuturesData {
		t.Fatalf("BTC-USD-PERP: %+v", coin)
	}
	astra := by["ASTRA-USDT-PERP"]
	if astra.FuturesData || astra.OpenInterest != nil || str(astra.MarkPrice) != "0.25" || str(astra.FundingRate) != "0" {
		t.Fatalf("the platform coin's perpetual: %+v", astra)
	}
}

// Before the reference market's list is read (reading off since the
// start), the contracts with a reference market have the stored data that
// /futures-data answers (review EK).
func TestFuturesDataOverviewBeforeTheList(t *testing.T) {
	r, stats := futuresRouter()
	stats.unknown, stats.markets = true, nil
	var body struct {
		Contracts []overviewJSON `json:"contracts"`
	}
	if err := json.Unmarshal(get(r, "/v1/market/futures/overview").Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	data := map[string]bool{}
	for _, c := range body.Contracts {
		data[c.Symbol] = c.FuturesData
	}
	if !data["BTC-USDT-PERP"] || !data["BTC-USD-PERP"] || data["ASTRA-USDT-PERP"] || len(data) != 3 {
		t.Fatalf("futures data before the list %v", data)
	}
}

func TestFuturesDataLiquidations(t *testing.T) {
	r, stats := futuresRouter()
	rec := get(r, "/v1/market/BTC-USDT-PERP/liquidations")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "public, max-age=5" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	want := `{"liquidations":[{"symbol":"BTC-USDT-PERP","position_side":"LONG","price":"9910","average_price":"9912",` +
		`"quantity":"0.014","value_usd":"138.768","traded_at":"2019-09-09T07:34:20.893Z"}],"symbol":"BTC-USDT-PERP"}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("liquidations:\n%s\nwant\n%s", got, want)
	}
	if rec := get(r, "/v1/market/ASTRA-USDT-PERP/liquidations"); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "MARKET_NO_FUTURES_DATA") {
		t.Fatalf("the platform coin's perpetual: %d %s", rec.Code, rec.Body)
	}
	if rec := get(r, "/v1/market/ETH-USDT-PERP/liquidations"); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "COMMON_NOT_FOUND") {
		t.Fatalf("a contract that is not listed: %d %s", rec.Code, rec.Body)
	}
	if len(stats.asked) != 2 {
		t.Fatalf("asked %v", stats.asked)
	}
}
