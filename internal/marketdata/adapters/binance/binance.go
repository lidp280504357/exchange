// Package binance reads Binance's public market data (data-api.binance.vision
// over REST, data-stream.binance.vision over WebSocket) as a reference
// source: 1m candles and rolling 24-hour tickers for the reference feed,
// candles of any interval for reference K-lines. Everything it returns is
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

	"github.com/lidp280504357/exchange/internal/marketdata/domain"
	"github.com/lidp280504357/exchange/internal/marketdata/ports"
)

// Source implements ports.ReferenceSource and ports.ReferenceHistory.
type Source struct {
	rest   string
	stream string
	// futuresREST and futuresStream are the USDⓈ-M futures endpoints of the
	// contracts' books and trades (WithFutures).
	futuresREST   string
	futuresStream string
	client        *http.Client
	// gap spaces REST requests: Binance allows 6000 request weight a
	// minute per IP; a klines call weighs 2.
	gap time.Duration
	// idle ends a stream that sent nothing for this long: tickers arrive
	// every second, and a connection the network silently dropped would
	// otherwise block until TCP keepalive gives up.
	idle time.Duration

	// turn lets one caller at a time wait for its turn; last is when the
	// latest request went out, or when requests may resume after Binance
	// asked to back off.
	turn chan struct{}
	mu   sync.Mutex
	last time.Time
}

// New returns a source on the REST and stream base URLs, e.g.
// https://data-api.binance.vision and wss://data-stream.binance.vision.
func New(rest, stream string, client *http.Client) *Source {
	return &Source{
		rest: strings.TrimRight(rest, "/"), stream: strings.TrimRight(stream, "/"), client: client, gap: 200 * time.Millisecond,
		turn: make(chan struct{}, 1),
		idle: 30 * time.Second,
	}
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

// get fetches a spot REST path into out.
func (s *Source) get(ctx context.Context, what, path string, q url.Values, out any) error {
	return s.getAt(ctx, what, s.rest, path, q, out)
}

// getAt fetches a REST path of base into out; requests to every base wait
// their turn together.
func (s *Source) getAt(ctx context.Context, what, base, path string, q url.Values, out any) error {
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
	var rows [][]any
	if err := s.get(ctx, "klines", "/api/v3/klines", q, &rows); err != nil {
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
// quoteVolume, trades, ...].
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

// tickerRow is a /api/v3/ticker/24hr answer.
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

// Tickers reads the rolling 24-hour tickers of refs in one request.
func (s *Source) Tickers(ctx context.Context, refs []ports.Reference) ([]domain.Ticker, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	byRemote := make(map[string]ports.Reference, len(refs))
	names := make([]string, 0, len(refs))
	for _, r := range refs {
		byRemote[r.Remote] = r
		names = append(names, r.Remote)
	}
	list, err := json.Marshal(names)
	if err != nil {
		return nil, err
	}
	var rows []tickerRow
	if err := s.get(ctx, "tickers", "/api/v3/ticker/24hr", url.Values{"symbols": {string(list)}}, &rows); err != nil {
		return nil, err
	}
	out := make([]domain.Ticker, 0, len(rows))
	for _, row := range rows {
		ref, ok := byRemote[row.Symbol]
		if !ok {
			continue
		}
		t, err := ticker(ref, [8]string{
			row.LastPrice, row.OpenPrice, row.HighPrice, row.LowPrice, row.Volume, row.QuoteVolume,
			row.BidPrice, row.AskPrice,
		}, row.Count, row.CloseTime)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// ticker builds a platform ticker from Binance's last, open, high, low,
// volume, quote volume, bid and ask.
func ticker(ref ports.Reference, fields [8]string, trades, atMS int64) (domain.Ticker, error) {
	var n [8]decimal.Decimal
	for i, f := range fields {
		d, err := decimal.NewFromString(f)
		if err != nil {
			return domain.Ticker{}, fmt.Errorf("binance ticker %s: bad amount %q", ref.Remote, f)
		}
		n[i] = d
	}
	conv := newConverter(ref)
	t := domain.Ticker{
		Symbol: ref.Symbol, Last: conv.price(n[0]), Open: conv.price(n[1]), High: conv.price(n[2]), Low: conv.price(n[3]),
		Volume: conv.quantity(n[4]), QuoteVolume: n[5], Trades: trades, Bid: conv.price(n[6]), Ask: conv.price(n[7]),
		At: time.UnixMilli(atMS).UTC(),
	}
	if t.Open.IsPositive() {
		t.Change = t.Last.Sub(t.Open).DivRound(t.Open, 8)
	}
	return t, nil
}

// converter turns the source's prices and quantities into the platform's.
type converter struct{ shift int32 }

func newConverter(ref ports.Reference) converter {
	shift := int32(0)
	for m := ref.Multiplier; m.GreaterThan(decimal.NewFromInt(1)); m = m.Shift(-1) {
		shift++
	}
	return converter{shift: shift}
}

func (c converter) price(d decimal.Decimal) decimal.Decimal    { return d.Shift(c.shift) }
func (c converter) quantity(d decimal.Decimal) decimal.Decimal { return d.Shift(-c.shift) }

func (c converter) candle(k domain.Candle) domain.Candle {
	k.Open, k.High, k.Low, k.Close = c.price(k.Open), c.price(k.High), c.price(k.Low), c.price(k.Close)
	k.Volume = c.quantity(k.Volume)
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

// Stream follows the combined kline_1m and ticker streams of refs.
// Binance pings every few minutes (answered by the library) and closes
// connections after 24 hours; a stream silent for idle ends too. The
// caller reconnects.
func (s *Source) Stream(ctx context.Context, refs []ports.Reference, on ports.StreamHandlers) error {
	names := make([]string, 0, 2*len(refs))
	byRemote := make(map[string]ports.Reference, len(refs))
	for _, r := range refs {
		lower := strings.ToLower(r.Remote)
		names = append(names, lower+"@kline_1m", lower+"@ticker")
		byRemote[r.Remote] = r
	}
	conn, resp, err := websocket.Dial(ctx, s.stream+"/stream?streams="+strings.Join(names, "/"), &websocket.DialOptions{HTTPClient: s.client})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("binance stream: %w", err)
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(1 << 16)
	for {
		read, cancel := context.WithTimeout(ctx, s.idle)
		_, data, err := conn.Read(read)
		cancel()
		if err != nil {
			if ctx.Err() == nil && errors.Is(read.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("binance stream: nothing received for %s", s.idle)
			}
			return fmt.Errorf("binance stream: %w", err)
		}
		var ev streamEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			continue
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
				if t, err := ticker(ref, [8]string{d.Last, d.Open, d.High, d.Low, d.Volume, d.QuoteVol, d.Bid, d.Ask}, d.Count, d.EventT); err == nil {
					on.Ticker(t)
				}
			}
		}
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
