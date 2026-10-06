// Package binance reads Binance's public market data (data-api.binance.vision
// over REST, data-stream.binance.vision over WebSocket for spot; the
// USDⓈ-M and COIN-M futures endpoints for perpetual contracts, coin-M
// design §3.2) as a reference source: 1m candles and rolling 24-hour
// tickers for the reference feed, candles of any interval for reference
// K-lines, books, trades and mark prices. Everything it returns is
// converted to the platform's symbols and units (ADR-0010, ADR-0014).
// Binance's terms forbid using the data for a trading service without a
// license (requirements §11.9, ADR-0004): test environments only, behind
// the market.reference_feed flag.
package binance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/marketdata/ports"
)

// Source implements ports.ReferenceSource and ports.ReferenceHistory.
type Source struct {
	rest   string
	stream string
	// futuresREST and futuresStream are the USDⓈ-M futures endpoints of the
	// contracts' books and trades (WithFutures).
	futuresREST   string
	futuresStream string
	// coinREST and coinStream are the COIN-M futures endpoints
	// (WithCoinFutures).
	coinREST   string
	coinStream string
	client     *http.Client
	// gap spaces REST requests: Binance allows 6000 request weight a
	// minute per IP; a klines call weighs 2.
	gap time.Duration
	// idle ends a stream that sent nothing for this long: tickers arrive
	// every second, and a connection the network silently dropped would
	// otherwise block until TCP keepalive gives up.
	idle time.Duration

	// fastDepth are the base assets whose perpetuals follow depth updates
	// every 100 ms (depthStream).
	fastDepth map[string]bool

	// turn lets one caller at a time wait for its turn; last is when the
	// latest request went out, or when requests may resume after Binance
	// asked to back off. weights is each REST host's request weight
	// (weight.go).
	turn    chan struct{}
	mu      sync.Mutex
	last    time.Time
	weights map[string]*hostWeight
}

// New returns a source on the REST and stream base URLs, e.g.
// https://data-api.binance.vision and wss://data-stream.binance.vision.
func New(rest, stream string, client *http.Client) *Source {
	return &Source{
		rest: strings.TrimRight(rest, "/"), stream: strings.TrimRight(stream, "/"), client: client, gap: 200 * time.Millisecond,
		turn: make(chan struct{}, 1), weights: map[string]*hostWeight{}, fastDepth: map[string]bool{"BTC": true, "ETH": true},
		idle: 30 * time.Second,
	}
}

// WithFastDepth sets the base assets whose perpetuals' books follow depth
// updates every 100 ms (BTC and ETH by default); the others' come every
// 500 ms.
func (s *Source) WithFastDepth(bases []string) *Source {
	s.fastDepth = map[string]bool{}
	for _, b := range bases {
		if b = strings.ToUpper(strings.TrimSpace(b)); b != "" {
			s.fastDepth[b] = true
		}
	}
	return s
}

// Name is the source's name.
func (s *Source) Name() string { return "binance" }

// wait keeps REST requests at least gap apart. Callers queue for the
// turn; one that gives up (its context ends) leaves the queue without
// taking a slot, so abandoned chart requests do not delay the rest.
func (s *Source) wait(ctx context.Context) error {
	select {
	case s.turn <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.turn }()
	s.mu.Lock()
	next := s.last.Add(s.gap)
	s.mu.Unlock()
	if d := time.Until(next); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	s.mu.Lock()
	s.last = time.Now()
	s.mu.Unlock()
	return nil
}

// backOff holds every request until Binance's Retry-After has passed
// (HTTP 429 over the rate limit, 418 once banned for ignoring it).
func (s *Source) backOff(resp *http.Response) {
	secs, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil || secs <= 0 {
		secs = 60
	}
	s.mu.Lock()
	if until := time.Now().Add(time.Duration(secs)*time.Second - s.gap); until.After(s.last) {
		s.last = until
	}
	s.mu.Unlock()
}

// getAt fetches a REST path of base into out: within the host's request
// weight (weigh), then in turn with the requests to every base.
func (s *Source) getAt(ctx context.Context, what, base, path string, q url.Values, out any) error {
	if err := s.weigh(ctx, base, requestWeight(base == s.rest, path, q)); err != nil {
		return err
	}
	if err := s.wait(ctx); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("binance %s: %w", what, err)
	}
	defer func() { _ = resp.Body.Close() }()
	s.observeWeight(base, resp.Header.Get("X-MBX-USED-WEIGHT-1M"))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusTooManyRequests, http.StatusTeapot:
		s.backOff(resp)
		return fmt.Errorf("binance %s: HTTP %d, backing off", what, resp.StatusCode)
	default:
		return fmt.Errorf("binance %s: HTTP %d", what, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("binance %s: %w", what, err)
	}
	return nil
}

// Backfill pages through /api/v3/klines from from up to to.
func (s *Source) Backfill(ctx context.Context, ref ports.Reference, from, to time.Time) ([]domain.Candle, error) {
	var out []domain.Candle
	for start := from; start.Before(to); {
		q := url.Values{
			"symbol": {ref.Remote}, "interval": {"1m"}, "startTime": {strconv.FormatInt(start.UnixMilli(), 10)},
			"endTime": {strconv.FormatInt(to.UnixMilli()-1, 10)}, "limit": {"1000"},
		}
		page, err := s.klines(ctx, ref, domain.Minute1, q)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		out = append(out, page...)
		start = out[len(out)-1].OpenTime.Add(time.Minute)
		if len(page) < 1000 {
			break
		}
	}
	return out, nil
}

// Klines returns the latest limit (at most 1000) candles of an interval
// opening before to (up to now when zero), oldest first; the open candle
// is the last. Binance names its intervals as the platform does and aligns
// them the same way (UTC days, weeks from Monday).
func (s *Source) Klines(ctx context.Context, ref ports.Reference, interval domain.Interval, to time.Time, limit int) ([]domain.Candle, error) {
	q := url.Values{"symbol": {ref.Remote}, "interval": {string(interval)}, "limit": {strconv.Itoa(max(1, min(limit, 1000)))}}
	if !to.IsZero() {
		// Binance's endTime includes a candle opening at it.
		q.Set("endTime", strconv.FormatInt(to.UnixMilli()-1, 10))
	}
	return s.klines(ctx, ref, interval, q)
}

func (s *Source) klines(ctx context.Context, ref ports.Reference, interval domain.Interval, q url.Values) ([]domain.Candle, error) {
	rest, prefix, _, err := s.endpoints(ref.Market)
	if err != nil {
		return nil, err
	}
	var rows [][]any
	if err := s.getAt(ctx, "klines", rest, prefix+"/klines", q, &rows); err != nil {
		return nil, err
	}
	conv := newConverter(ref)
	out := make([]domain.Candle, 0, len(rows))
	for _, row := range rows {
		c, err := fromRow(ref.Symbol, interval, row)
		if err != nil {
			return nil, err
		}
		out = append(out, conv.candle(c))
	}
	return out, nil
}

// fromRow reads [openTime, open, high, low, close, volume, closeTime,
// quoteVolume, trades, ...] (COIN-M's eighth is the base asset's volume;
// the converter replaces it).
func fromRow(symbol string, interval domain.Interval, row []any) (domain.Candle, error) {
	if len(row) < 9 {
		return domain.Candle{}, errors.New("binance klines: short row")
	}
	openMS, ok1 := row[0].(float64)
	trades, ok2 := row[8].(float64)
	if !ok1 || !ok2 {
		return domain.Candle{}, errors.New("binance klines: bad row")
	}
	var nums [6]decimal.Decimal
	for i, idx := range []int{1, 2, 3, 4, 5, 7} {
		s, ok := row[idx].(string)
		d, err := decimal.NewFromString(s)
		if !ok || err != nil {
			return domain.Candle{}, fmt.Errorf("binance klines: bad amount %v", row[idx])
		}
		nums[i] = d
	}
	return domain.Candle{
		Symbol: symbol, Interval: interval, OpenTime: time.UnixMilli(int64(openMS)).UTC(),
		Open: nums[0], High: nums[1], Low: nums[2], Close: nums[3], Volume: nums[4], QuoteVolume: nums[5], Trades: int64(trades),
	}, nil
}

// tickerRow is a /ticker/24hr answer: spot's, or a futures market's
// (without the best bid and ask; COIN-M's volume is contracts and it has
// the base asset's volume instead of the quote's).
type tickerRow struct {
	Symbol      string `json:"symbol"`
	LastPrice   string `json:"lastPrice"`
	OpenPrice   string `json:"openPrice"`
	HighPrice   string `json:"highPrice"`
	LowPrice    string `json:"lowPrice"`
	Volume      string `json:"volume"`
	QuoteVolume string `json:"quoteVolume"`
	BidPrice    string `json:"bidPrice"`
	AskPrice    string `json:"askPrice"`
	Count       int64  `json:"count"`
	CloseTime   int64  `json:"closeTime"`
}

// Tickers reads the rolling 24-hour tickers of refs, all of one market,
// in one request: spot by the list of symbols, a futures market's every
// symbol (it takes one symbol or all).
func (s *Source) Tickers(ctx context.Context, refs []ports.Reference) ([]domain.Ticker, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	m, err := market(refs)
	if err != nil {
		return nil, err
	}
	rest, prefix, _, err := s.endpoints(m)
	if err != nil {
		return nil, err
	}
	byRemote := make(map[string]ports.Reference, len(refs))
	names := make([]string, 0, len(refs))
	for _, r := range refs {
		byRemote[r.Remote] = r
		names = append(names, r.Remote)
	}
	q := url.Values{}
	if m == ports.MarketSpot {
		list, err := json.Marshal(names)
		if err != nil {
			return nil, err
		}
		q.Set("symbols", string(list))
	}
	var rows []tickerRow
	if err := s.getAt(ctx, "tickers", rest, prefix+"/ticker/24hr", q, &rows); err != nil {
		return nil, err
	}
	out := make([]domain.Ticker, 0, len(refs))
	for _, row := range rows {
		ref, ok := byRemote[row.Symbol]
		if !ok {
			continue
		}
		t, err := ticker(ref, tickerFields{
			last: row.LastPrice, open: row.OpenPrice, high: row.HighPrice, low: row.LowPrice, volume: row.Volume,
			quoteVolume: row.QuoteVolume, bid: row.BidPrice, ask: row.AskPrice,
		}, row.Count, row.CloseTime)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// tickerFields are a Binance ticker's amounts as sent; a futures market
// sends no best bid and ask, COIN-M no quote volume.
type tickerFields struct {
	last, open, high, low, volume, quoteVolume, bid, ask string
}

// ticker builds a platform ticker from Binance's. Missing bid, ask and
// quote volume are zero; a COIN-M contract's quote volume is its volume's
// USD value (contracts times the contract size).
func ticker(ref ports.Reference, f tickerFields, trades, atMS int64) (domain.Ticker, error) {
	var n [8]decimal.Decimal
	for i, s := range []string{f.last, f.open, f.high, f.low, f.volume, f.quoteVolume, f.bid, f.ask} {
		if s == "" && i >= 5 {
			continue
		}
		d, err := decimal.NewFromString(s)
		if err != nil {
			return domain.Ticker{}, fmt.Errorf("binance ticker %s: bad amount %q", ref.Remote, s)
		}
		n[i] = d
	}
	conv := newConverter(ref)
	t := domain.Ticker{
		Symbol: ref.Symbol, Last: conv.price(n[0]), Open: conv.price(n[1]), High: conv.price(n[2]), Low: conv.price(n[3]),
		Volume: conv.quantity(n[4]), QuoteVolume: n[5], Trades: trades, Bid: conv.price(n[6]), Ask: conv.price(n[7]),
		At: time.UnixMilli(atMS).UTC(),
	}
	if ref.Market == ports.MarketCoinM {
		t.QuoteVolume = t.Volume.Mul(ref.ContractSize)
	}
	if t.Open.IsPositive() {
		t.Change = t.Last.Sub(t.Open).DivRound(t.Open, 8)
	}
	return t, nil
}

// converter turns the source's prices and quantities into the platform's.
type converter struct {
	shift int32
	// size is a COIN-M contract's size: its quote volume is the
	// contracts' USD value.
	size decimal.Decimal
}

func newConverter(ref ports.Reference) converter {
	shift := int32(0)
	for m := ref.Multiplier; m.GreaterThan(decimal.NewFromInt(1)); m = m.Shift(-1) {
		shift++
	}
	c := converter{shift: shift}
	if ref.Market == ports.MarketCoinM {
		c.size = ref.ContractSize
	}
	return c
}

func (c converter) price(d decimal.Decimal) decimal.Decimal    { return d.Shift(c.shift) }
func (c converter) quantity(d decimal.Decimal) decimal.Decimal { return d.Shift(-c.shift) }

func (c converter) candle(k domain.Candle) domain.Candle {
	k.Open, k.High, k.Low, k.Close = c.price(k.Open), c.price(k.High), c.price(k.Low), c.price(k.Close)
	k.Volume = c.quantity(k.Volume)
	if c.size.IsPositive() {
		k.QuoteVolume = k.Volume.Mul(c.size)
	}
	return k
}

// streamEvent is a combined-stream message. Binance uses keys that differ
// only in case (t/T, l/L, v/V, q/Q, b/B, ...) and encoding/json matches
// keys case-insensitively, so every one of them has its own field: an
// exact match wins, and the number in "L" no longer lands in the price "l".
type streamEvent struct {
	Data struct {
		Event  string `json:"e"`
		EventT int64  `json:"E"`
		Symbol string `json:"s"`
		// 24hrTicker
		Change     string `json:"p"`
		ChangePct  string `json:"P"`
		Last       string `json:"c"`
		CloseTime  int64  `json:"C"`
		Open       string `json:"o"`
		OpenTime   int64  `json:"O"`
		High       string `json:"h"`
		Low        string `json:"l"`
		LastID     int64  `json:"L"`
		Volume     string `json:"v"`
		QuoteVol   string `json:"q"`
		LastQty    string `json:"Q"`
		Bid        string `json:"b"`
		BidQty     string `json:"B"`
		Ask        string `json:"a"`
		AskQty     string `json:"A"`
		Count      int64  `json:"n"`
		FirstID    int64  `json:"F"`
		WeightedAv string `json:"w"`
		PrevClose  string `json:"x"`
		// kline
		K struct {
			Symbol    string `json:"s"`
			Open      int64  `json:"t"`
			Close     int64  `json:"T"`
			O         string `json:"o"`
			H         string `json:"h"`
			L         string `json:"l"`
			C         string `json:"c"`
			V         string `json:"v"`
			Q         string `json:"q"`
			N         int64  `json:"n"`
			LastTrade int64  `json:"L"`
			TakerBase string `json:"V"`
			TakerQuot string `json:"Q"`
		} `json:"k"`
	} `json:"data"`
}

// Stream follows the combined kline_1m and ticker streams of refs, all of
// one market (a USDⓈ-M contract's under /market). Binance pings every few
// minutes (answered by the library) and closes connections after 24
// hours; a stream silent for idle ends too. The caller reconnects.
func (s *Source) Stream(ctx context.Context, refs []ports.Reference, on ports.StreamHandlers) error {
	m, err := market(refs)
	if err != nil {
		return err
	}
	_, _, stream, err := s.endpoints(m)
	if err != nil {
		return err
	}
	path := allStreams
	if m == ports.MarketUSDM {
		path = marketStreams
	}
	names := make([]string, 0, 2*len(refs))
	byRemote := make(map[string]ports.Reference, len(refs))
	for _, r := range refs {
		lower := strings.ToLower(r.Remote)
		names = append(names, lower+"@kline_1m", lower+"@ticker")
		byRemote[r.Remote] = r
	}
	return s.listen(ctx, "binance stream", stream+path+strings.Join(names, "/"), 1<<16, s.idle, func(data []byte) {
		var ev streamEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return
		}
		switch ev.Data.Event {
		case "kline":
			if ref, ok := byRemote[ev.Data.K.Symbol]; ok && on.Candle != nil {
				if c, ok := klineCandle(ref, ev); ok {
					on.Candle(c)
				}
			}
		case "24hrTicker":
			if ref, ok := byRemote[ev.Data.Symbol]; ok && on.Ticker != nil {
				d := ev.Data
				f := tickerFields{
					last: d.Last, open: d.Open, high: d.High, low: d.Low, volume: d.Volume, quoteVolume: d.QuoteVol, bid: d.Bid, ask: d.Ask,
				}
				if t, err := ticker(ref, f, d.Count, d.EventT); err == nil {
					on.Ticker(t)
				}
			}
		}
	})
}

// Combined stream paths. Binance's USDⓈ-M futures market serves its
// streams by category (found 2026-10-06): the order books under /public,
// trades, mark prices, klines and tickers under /market, and its old
// /stream path the books only; COIN-M futures and spot serve them all
// under /stream.
const (
	allStreams    = "/stream?streams="
	publicStreams = "/public/stream?streams="
	marketStreams = "/market/stream?streams="
)

// listen follows one combined stream connection at url, passing every
// message to handle on its goroutine, until ctx ends, the connection
// fails or stays silent for idle; the caller reconnects.
func (s *Source) listen(ctx context.Context, what, url string, limit int64, idle time.Duration, handle func([]byte)) error {
	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: s.client})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(limit)
	for {
		read, cancel := context.WithTimeout(ctx, idle)
		_, data, err := conn.Read(read)
		cancel()
		if err != nil {
			if ctx.Err() == nil && errors.Is(read.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("%s: nothing received for %s", what, idle)
			}
			return fmt.Errorf("%s: %w", what, err)
		}
		handle(data)
	}
}

// klineCandle reads a kline event; false when an amount does not parse.
func klineCandle(ref ports.Reference, ev streamEvent) (domain.Candle, bool) {
	k := ev.Data.K
	c := domain.Candle{Symbol: ref.Symbol, Interval: domain.Minute1, OpenTime: time.UnixMilli(k.Open).UTC(), Trades: k.N}
	for _, f := range []struct {
		dst *decimal.Decimal
		src string
	}{{&c.Open, k.O}, {&c.High, k.H}, {&c.Low, k.L}, {&c.Close, k.C}, {&c.Volume, k.V}, {&c.QuoteVolume, k.Q}} {
		d, err := decimal.NewFromString(f.src)
		if err != nil {
			return domain.Candle{}, false
		}
		*f.dst = d
	}
	return newConverter(ref).candle(c), true
}
