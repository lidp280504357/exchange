package binance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketdata/ports"
)

// The futures statistics and liquidations (design 2026-10-06 §3.3):
// USDⓈ-M contracts on fapi and fstream, COIN-M ones on dapi and dstream,
// whose statistics are by pair.

// Request pacing of the futures statistics, apart from Source's queue: a
// futures/data endpoint allows 1000 requests per 5 minutes per IP (not
// said to be shared between endpoints), the funding rates 500 together
// with fundingInfo, which the marks read too; openInterest and
// exchangeInfo weigh 1 of the 2400 a minute.
const (
	statsGap        = 375 * time.Millisecond // 800 per 5 minutes an endpoint
	fundingGap      = 1500 * time.Millisecond
	openInterestGap = 100 * time.Millisecond
	infoGap         = time.Second
	// statsLimit is the most points a futures/data request returns,
	// fundingLimit a fundingRate request.
	statsLimit   = 500
	fundingLimit = 1000
	// statsKept is how far back the source keeps the statistics.
	statsKept = 30 * 24 * time.Hour
)

// Futures reads the reference market's futures statistics, open interest
// and liquidation orders. It implements ports.FuturesSource.
type Futures struct {
	usdm, coinm endpoints
	client      *http.Client
	// idle ends a liquidation stream silent for this long (ports.ErrQuiet):
	// Binance pings every few minutes, answered by the library, and sends
	// orders only when something is liquidated.
	idle time.Duration
	now  func() time.Time
	pace pacer
}

type endpoints struct{ rest, stream string }

// NewFutures returns the futures data of the USDⓈ-M endpoints (e.g.
// https://fapi.binance.com, wss://fstream.binance.com) and the COIN-M ones
// (https://dapi.binance.com, wss://dstream.binance.com).
func NewFutures(usdmREST, usdmStream, coinmREST, coinmStream string, client *http.Client) *Futures {
	trim := func(s string) string { return strings.TrimRight(s, "/") }
	return &Futures{
		usdm: endpoints{trim(usdmREST), trim(usdmStream)}, coinm: endpoints{trim(coinmREST), trim(coinmStream)},
		client: client, idle: 15 * time.Minute, now: time.Now,
		pace: pacer{next: map[string]time.Time{}, hold: map[string]time.Time{}, now: time.Now},
	}
}

func (f *Futures) at(coinMargined bool) endpoints {
	if coinMargined {
		return f.coinm
	}
	return f.usdm
}

// pacer spaces the requests of each lane (a host's endpoint) at least its
// gap apart, and holds every lane of a host while the host asked to back
// off.
type pacer struct {
	mu   sync.Mutex
	next map[string]time.Time
	hold map[string]time.Time
	now  func() time.Time
	// unpaced drops the gaps (tests); holds still apply.
	unpaced bool
}

// wait takes the lane's next turn and sleeps until it comes.
func (p *pacer) wait(ctx context.Context, host, lane string, gap time.Duration) error {
	p.mu.Lock()
	if p.unpaced {
		gap = 0
	}
	now := p.now()
	at := now
	if t := p.next[lane]; t.After(at) {
		at = t
	}
	if t := p.hold[host]; t.After(at) {
		at = t
	}
	p.next[lane] = at.Add(gap)
	p.mu.Unlock()
	if d := at.Sub(now); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

// backOff holds the host's requests until its Retry-After has passed
// (HTTP 429 over a limit, 418 once banned for ignoring it).
func (p *pacer) backOff(host string, resp *http.Response) {
	secs, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil || secs <= 0 {
		secs = 60
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if until := p.now().Add(time.Duration(secs) * time.Second); until.After(p.hold[host]) {
		p.hold[host] = until
	}
}

// apiError is Binance's error answer.
type apiError struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

// get fetches a REST path of the USDⓈ-M or COIN-M host into out, at the
// lane's pace.
func (f *Futures) get(ctx context.Context, coinMargined bool, path string, gap time.Duration, q url.Values, out any) error {
	host := f.at(coinMargined).rest
	if err := f.pace.wait(ctx, host, host+path, gap); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, host+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("binance %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusTooManyRequests, http.StatusTeapot:
		f.pace.backOff(host, resp)
		return fmt.Errorf("binance %s: HTTP %d, backing off", path, resp.StatusCode)
	default:
		var e apiError
		if json.NewDecoder(resp.Body).Decode(&e) == nil && e.Code != 0 {
			return fmt.Errorf("binance %s: HTTP %d code %d: %s", path, resp.StatusCode, e.Code, e.Msg)
		}
		return fmt.Errorf("binance %s: HTTP %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("binance %s: %w", path, err)
	}
	return nil
}

type exchangeInfo struct {
	Symbols []struct {
		Symbol       string `json:"symbol"`
		Pair         string `json:"pair"`
		ContractType string `json:"contractType"`
		// USDⓈ-M names the trading state status, COIN-M contractStatus.
		Status         string          `json:"status"`
		ContractStatus string          `json:"contractStatus"`
		ContractSize   decimal.Decimal `json:"contractSize"`
	} `json:"symbols"`
}

// Perpetuals lists the perpetual contracts in trading.
func (f *Futures) Perpetuals(ctx context.Context, coinMargined bool) ([]ports.Perpetual, error) {
	path := "/fapi/v1/exchangeInfo"
	if coinMargined {
		path = "/dapi/v1/exchangeInfo"
	}
	var info exchangeInfo
	if err := f.get(ctx, coinMargined, path, infoGap, url.Values{}, &info); err != nil {
		return nil, err
	}
	out := []ports.Perpetual{}
	for _, s := range info.Symbols {
		status := s.Status
		if coinMargined {
			status = s.ContractStatus
		}
		if s.ContractType != "PERPETUAL" || status != "TRADING" {
			continue
		}
		p := ports.Perpetual{Remote: s.Symbol, Pair: s.Pair}
		if coinMargined {
			p.ContractSize = s.ContractSize
		}
		out = append(out, p)
	}
	return out, nil
}

// statSource is where a metric's points come from and how their fields
// are named there.
type statSource struct {
	path string
	// byPair asks by pair (with contractType PERPETUAL where the source
	// takes one) rather than by symbol.
	byPair, contractType bool
	// fields maps the values' names to the source's ("a|b": either).
	fields map[string]string
	// timeField names the point's time; ratio sets buy_sell_ratio from
	// the volumes (COIN-M's taker volumes come without it).
	timeField string
	ratio     bool
}

var (
	ratioFields = map[string]string{
		"long_short_ratio": "longShortRatio", "long": "longAccount|longPosition", "short": "shortAccount|shortPosition",
	}
	usdmStats = map[string]statSource{
		ports.MetricOpenInterest: {path: "/futures/data/openInterestHist", fields: map[string]string{
			"open_interest": "sumOpenInterest", "open_interest_value": "sumOpenInterestValue",
		}},
		ports.MetricLongShortAccount:     {path: "/futures/data/globalLongShortAccountRatio", fields: ratioFields},
		ports.MetricTopLongShortAccount:  {path: "/futures/data/topLongShortAccountRatio", fields: ratioFields},
		ports.MetricTopLongShortPosition: {path: "/futures/data/topLongShortPositionRatio", fields: ratioFields},
		ports.MetricTakerRatio: {path: "/futures/data/takerlongshortRatio", fields: map[string]string{
			"buy_vol": "buyVol", "sell_vol": "sellVol", "buy_sell_ratio": "buySellRatio",
		}},
		ports.MetricBasis:   {path: "/futures/data/basis", byPair: true, contractType: true, fields: basisFields},
		ports.MetricFunding: {path: "/fapi/v1/fundingRate", timeField: "fundingTime", fields: fundingFields},
	}
	coinmStats = map[string]statSource{
		ports.MetricOpenInterest: {path: "/futures/data/openInterestHist", byPair: true, contractType: true, fields: map[string]string{
			"open_interest": "sumOpenInterest", "open_interest_value": "sumOpenInterestValue",
		}},
		ports.MetricLongShortAccount:     {path: "/futures/data/globalLongShortAccountRatio", byPair: true, fields: ratioFields},
		ports.MetricTopLongShortAccount:  {path: "/futures/data/topLongShortAccountRatio", byPair: true, fields: ratioFields},
		ports.MetricTopLongShortPosition: {path: "/futures/data/topLongShortPositionRatio", byPair: true, fields: ratioFields},
		ports.MetricTakerRatio: {path: "/futures/data/takerBuySellVol", byPair: true, contractType: true, ratio: true, fields: map[string]string{
			"buy_vol": "takerBuyVol", "sell_vol": "takerSellVol",
		}},
		ports.MetricBasis:   {path: "/futures/data/basis", byPair: true, contractType: true, fields: basisFields},
		ports.MetricFunding: {path: "/dapi/v1/fundingRate", timeField: "fundingTime", fields: fundingFields},
	}
	basisFields = map[string]string{
		"basis": "basis", "basis_rate": "basisRate", "futures_price": "futuresPrice", "index_price": "indexPrice",
	}
	fundingFields = map[string]string{"funding_rate": "fundingRate", "mark_price": "markPrice"}
)

// PeriodDuration is how long a statistics period is; false for one the
// source does not have.
func PeriodDuration(period string) (time.Duration, bool) {
	d, ok := map[string]time.Duration{
		"5m": 5 * time.Minute, "15m": 15 * time.Minute, "1h": time.Hour, "4h": 4 * time.Hour, "1d": 24 * time.Hour,
	}[period]
	return d, ok
}

// Stats returns up to limit points of a series after after, oldest
// first; the latest limit when after is zero. A futures/data request with
// a start time still answers the latest points up to its end time, so a
// page after after ends limit periods later.
func (f *Futures) Stats(ctx context.Context, m ports.FuturesMarket, metric, period string, after time.Time, limit int) ([]ports.FuturesStat, error) {
	src, ok := usdmStats[metric]
	if m.CoinMargined {
		src, ok = coinmStats[metric]
	}
	if !ok {
		return nil, fmt.Errorf("binance futures stats: unknown metric %q", metric)
	}
	q := url.Values{}
	switch {
	case src.byPair:
		q.Set("pair", m.Pair)
	default:
		q.Set("symbol", m.Remote)
	}
	if src.contractType {
		q.Set("contractType", "PERPETUAL")
	}
	gap := statsGap
	if metric == ports.MetricFunding {
		gap = fundingGap
		limit = max(1, min(limit, fundingLimit))
		start := f.now().Add(-statsKept)
		if after.After(start) {
			start = after.Add(time.Millisecond)
		}
		q.Set("startTime", strconv.FormatInt(start.UnixMilli(), 10))
	} else {
		d, ok := PeriodDuration(period)
		if !ok {
			return nil, fmt.Errorf("binance futures stats: unknown period %q", period)
		}
		limit = max(1, min(limit, statsLimit))
		q.Set("period", period)
		// Older than the source keeps: the latest points instead.
		if !after.IsZero() && after.After(f.now().Add(-statsKept+d)) {
			q.Set("startTime", strconv.FormatInt(after.Add(time.Millisecond).UnixMilli(), 10))
			q.Set("endTime", strconv.FormatInt(after.Add(time.Duration(limit)*d).UnixMilli(), 10))
		}
	}
	q.Set("limit", strconv.Itoa(limit))
	var rows []map[string]json.RawMessage
	if err := f.get(ctx, m.CoinMargined, src.path, gap, q, &rows); err != nil {
		return nil, err
	}
	out := make([]ports.FuturesStat, 0, len(rows))
	for _, row := range rows {
		s, err := src.point(row)
		if err != nil {
			return nil, fmt.Errorf("binance %s %s: %w", src.path, m.Remote, err)
		}
		if !s.At.After(after) || len(s.Values) == 0 {
			continue
		}
		s.Symbol, s.Metric = m.Symbol, metric
		if metric != ports.MetricFunding {
			s.Period = period
		}
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b ports.FuturesStat) int { return a.At.Compare(b.At) })
	return out, nil
}

// point reads one row: its time, and the values the source gave (a value
// it left out or empty is left out).
func (src statSource) point(row map[string]json.RawMessage) (ports.FuturesStat, error) {
	timeField := src.timeField
	if timeField == "" {
		timeField = "timestamp"
	}
	var ms int64
	if err := json.Unmarshal(row[timeField], &ms); err != nil || ms <= 0 {
		return ports.FuturesStat{}, fmt.Errorf("bad %s %s", timeField, row[timeField])
	}
	s := ports.FuturesStat{At: time.UnixMilli(ms).UTC(), Values: map[string]decimal.Decimal{}}
	for name, names := range src.fields {
		for _, field := range strings.Split(names, "|") {
			raw, ok := row[field]
			if !ok {
				continue
			}
			var text string
			if err := json.Unmarshal(raw, &text); err != nil {
				return ports.FuturesStat{}, fmt.Errorf("bad %s %s", field, raw)
			}
			if text == "" {
				break
			}
			v, err := decimal.NewFromString(text)
			if err != nil {
				return ports.FuturesStat{}, fmt.Errorf("bad %s %q", field, text)
			}
			s.Values[name] = v
			break
		}
	}
	if src.ratio {
		buy, okBuy := s.Values["buy_vol"]
		sell, okSell := s.Values["sell_vol"]
		if okBuy && okSell && sell.IsPositive() {
			s.Values["buy_sell_ratio"] = buy.DivRound(sell, 4)
		}
	}
	return s, nil
}

// OpenInterest returns a contract's open interest now: in the base asset
// for a USDⓈ-M contract, in contracts for a COIN-M one.
func (f *Futures) OpenInterest(ctx context.Context, m ports.FuturesMarket) (decimal.Decimal, time.Time, error) {
	path := "/fapi/v1/openInterest"
	if m.CoinMargined {
		path = "/dapi/v1/openInterest"
	}
	var row struct {
		OpenInterest decimal.Decimal `json:"openInterest"`
		Time         int64           `json:"time"`
	}
	if err := f.get(ctx, m.CoinMargined, path, openInterestGap, url.Values{"symbol": {m.Remote}}, &row); err != nil {
		return decimal.Decimal{}, time.Time{}, err
	}
	return row.OpenInterest, time.UnixMilli(row.Time).UTC(), nil
}

// forceOrderEvent is a liquidation stream message. Keys that differ only
// in case (e/E, s/S) each have their own field: encoding/json matches
// keys case-insensitively, an exact match winning.
type forceOrderEvent struct {
	Event  string `json:"e"`
	EventT int64  `json:"E"`
	Order  struct {
		Symbol   string `json:"s"`
		Side     string `json:"S"`
		Price    string `json:"p"`
		AvgPrice string `json:"ap"`
		// Filled is the quantity filled so far (z), of the order's q.
		Filled string `json:"z"`
		TradeT int64  `json:"T"`
	} `json:"o"`
}

// ForcedOrders follows the !forceOrder@arr stream of the USDⓈ-M or the
// COIN-M market. USDⓈ-M serves it under /market only (2026-10-06: its
// /ws and /stream paths still upgrade but send nothing but depth);
// COIN-M answers on both, with USDⓈ-M contracts in it too, which the
// caller leaves out. Binance closes connections after 24 hours; the
// caller reconnects.
func (f *Futures) ForcedOrders(ctx context.Context, coinMargined bool, on func(ports.ForcedOrder)) error {
	conn, resp, err := websocket.Dial(ctx, f.at(coinMargined).stream+"/market/ws/!forceOrder@arr", &websocket.DialOptions{HTTPClient: f.client})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("binance liquidation stream: %w", err)
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(1 << 16)
	for {
		read, cancel := context.WithTimeout(ctx, f.idle)
		_, data, err := conn.Read(read)
		cancel()
		if err != nil {
			if ctx.Err() == nil && errors.Is(read.Err(), context.DeadlineExceeded) {
				return ports.ErrQuiet
			}
			return fmt.Errorf("binance liquidation stream: %w", err)
		}
		if o, ok := forcedOrder(data); ok {
			on(o)
		}
	}
}

// forcedOrder reads a liquidation message; false for another message or
// one whose amounts do not parse.
func forcedOrder(data []byte) (ports.ForcedOrder, bool) {
	var ev forceOrderEvent
	if err := json.Unmarshal(data, &ev); err != nil || ev.Event != "forceOrder" {
		return ports.ForcedOrder{}, false
	}
	o := ports.ForcedOrder{Remote: ev.Order.Symbol, Side: ev.Order.Side, At: time.UnixMilli(ev.Order.TradeT).UTC()}
	for _, f := range []struct {
		dst *decimal.Decimal
		src string
	}{{&o.Price, ev.Order.Price}, {&o.AvgPrice, ev.Order.AvgPrice}, {&o.Filled, ev.Order.Filled}} {
		d, err := decimal.NewFromString(f.src)
		if err != nil {
			return ports.ForcedOrder{}, false
		}
		*f.dst = d
	}
	if o.Remote == "" || (o.Side != "BUY" && o.Side != "SELL") || !o.Filled.IsPositive() {
		return ports.ForcedOrder{}, false
	}
	return o, true
}
