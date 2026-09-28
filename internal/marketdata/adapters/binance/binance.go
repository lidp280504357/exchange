// Package binance reads 1m candles from Binance's public market data
// (data-api.binance.vision over REST, data-stream.binance.vision over
// WebSocket) as a reference source. Binance's terms forbid using the data
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

	mu   sync.Mutex
	last time.Time
}

// New returns a source on the REST and stream base URLs, e.g.
// https://data-api.binance.vision and wss://data-stream.binance.vision.
func New(rest, stream string, client *http.Client) *Source {
	return &Source{rest: strings.TrimRight(rest, "/"), stream: strings.TrimRight(stream, "/"), client: client, gap: 200 * time.Millisecond}
}

// Name is the source's name.
func (s *Source) Name() string { return "binance" }

// remote turns BTC-USDT into BTCUSDT.
func remote(symbol string) string { return strings.ReplaceAll(symbol, "-", "") }

// wait keeps REST requests at least gap apart.
func (s *Source) wait(ctx context.Context) error {
	s.mu.Lock()
	next := s.last.Add(s.gap)
	now := time.Now()
	if next.Before(now) {
		next = now
	}
	s.last = next
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Until(next)):
		return nil
	}
}

// Backfill pages through /api/v3/klines from from to now.
func (s *Source) Backfill(ctx context.Context, symbol string, from time.Time) ([]domain.Candle, error) {
	var out []domain.Candle
	for start := from; start.Before(time.Now()); {
		if err := s.wait(ctx); err != nil {
			return nil, err
		}
		q := url.Values{"symbol": {remote(symbol)}, "interval": {"1m"}, "startTime": {strconv.FormatInt(start.UnixMilli(), 10)}, "limit": {"1000"}}
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
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			c, err := fromRow(symbol, row)
			if err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		start = out[len(out)-1].OpenTime.Add(time.Minute)
		if len(rows) < 1000 {
			break
		}
	}
	return out, nil
}

// fromRow reads [openTime, open, high, low, close, volume, closeTime,
// quoteVolume, trades, ...].
func fromRow(symbol string, row []any) (domain.Candle, error) {
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
		Symbol: symbol, Interval: domain.Minute1, OpenTime: time.UnixMilli(int64(openMS)).UTC(),
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
// 24 hours; the caller reconnects.
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
		_, data, err := conn.Read(ctx)
		if err != nil {
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
