// Package binance reads candles from Binance's public market data
// (data-api.binance.vision over REST, data-stream.binance.vision over
// WebSocket) as a reference source: 1m candles for reference prices, any
// interval for reference K-lines. Binance's terms forbid using the data
// for a trading service without a license (requirements §11.9): test
// environments only, behind the market.reference_feed flag.
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
)

// Source implements ports.ReferenceSource.
type Source struct {
	rest   string
	stream string
	client *http.Client
	// gap spaces REST requests: Binance allows 6000 request weight a
	// minute per IP; a klines call weighs 2.
	gap time.Duration
	// idle ends a stream that sent nothing for this long: kline_1m updates
	// arrive every two seconds, and a connection the network silently
	// dropped would otherwise block until TCP keepalive gives up.
	idle time.Duration

	// turn lets one caller at a time wait for its turn; last is when the
	// latest request went out.
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

// remote turns BTC-USDT into BTCUSDT.
func remote(symbol string) string { return strings.ReplaceAll(symbol, "-", "") }

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

// Backfill pages through /api/v3/klines from from to now.
func (s *Source) Backfill(ctx context.Context, symbol string, from time.Time) ([]domain.Candle, error) {
	var out []domain.Candle
	for start := from; start.Before(time.Now()); {
		q := url.Values{"symbol": {remote(symbol)}, "interval": {"1m"}, "startTime": {strconv.FormatInt(start.UnixMilli(), 10)}, "limit": {"1000"}}
		page, err := s.klines(ctx, symbol, domain.Minute1, q)
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

// Klines returns the latest limit (at most 1000) candles of an interval,
// oldest first, up to the one containing to (now when zero); the open
// candle is the last. Binance names its intervals as the platform does
// and aligns them the same way (UTC days, weeks from Monday).
func (s *Source) Klines(ctx context.Context, symbol string, interval domain.Interval, to time.Time, limit int) ([]domain.Candle, error) {
	q := url.Values{"symbol": {remote(symbol)}, "interval": {string(interval)}, "limit": {strconv.Itoa(max(1, min(limit, 1000)))}}
	if !to.IsZero() {
		q.Set("endTime", strconv.FormatInt(to.UnixMilli(), 10))
	}
	return s.klines(ctx, symbol, interval, q)
}

func (s *Source) klines(ctx context.Context, symbol string, interval domain.Interval, q url.Values) ([]domain.Candle, error) {
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.rest+"/api/v3/klines?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("binance klines: %w", err)
	}
	var rows [][]any
	err = json.NewDecoder(resp.Body).Decode(&rows)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("binance klines: HTTP %d", resp.StatusCode)
	}
	if err != nil {
		return nil, fmt.Errorf("binance klines: %w", err)
	}
	out := make([]domain.Candle, 0, len(rows))
	for _, row := range rows {
		c, err := fromRow(symbol, interval, row)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
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

// klineEvent is a combined-stream kline message. Binance uses keys that
// differ only in case (t/T, l/L, v/V, q/Q) and encoding/json matches keys
// case-insensitively, so every one of them has its own field: an exact
// match wins, and the number in "L" no longer lands in the price "l".
type klineEvent struct {
	Data struct {
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

// Stream follows the combined kline_1m streams of symbols. Binance pings
// every few minutes (answered by the library) and closes connections after
// 24 hours; a stream silent for idle ends too. The caller reconnects.
func (s *Source) Stream(ctx context.Context, symbols []string, on func(domain.Candle)) error {
	names := make([]string, len(symbols))
	local := make(map[string]string, len(symbols))
	for i, sym := range symbols {
		names[i] = strings.ToLower(remote(sym)) + "@kline_1m"
		local[remote(sym)] = sym
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
		var ev klineEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			continue
		}
		k := ev.Data.K
		sym, ok := local[k.Symbol]
		if !ok {
			continue
		}
		c := domain.Candle{Symbol: sym, Interval: domain.Minute1, OpenTime: time.UnixMilli(k.Open).UTC(), Trades: k.N}
		parsed := true
		for _, f := range []struct {
			dst *decimal.Decimal
			src string
		}{{&c.Open, k.O}, {&c.High, k.H}, {&c.Low, k.L}, {&c.Close, k.C}, {&c.Volume, k.V}, {&c.QuoteVolume, k.Q}} {
			d, err := decimal.NewFromString(f.src)
			if err != nil {
				parsed = false
				break
			}
			*f.dst = d
		}
		if parsed {
			on(c)
		}
	}
}
