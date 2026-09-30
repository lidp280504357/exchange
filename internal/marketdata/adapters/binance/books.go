package binance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketdata/domain"
	"github.com/lidp280504357/exchange/internal/marketdata/ports"
)

// Order books and trades (ADR-0010, ADR-0015): REST snapshots and the
// depth@100ms and aggTrade streams, of spot pairs on the spot endpoints
// and of perpetual contracts on the USDⓈ-M futures ones.

// SnapshotLevels is how deep a book snapshot goes (a spot request weighs
// 50 at this depth, a futures one 20).
const SnapshotLevels = 1000

// tradeNamespace seeds the platform IDs of the reference market's trades.
var tradeNamespace = uuid.MustParse("5f0b6c8e-2d4a-4c1e-9b7a-3e8d2f6a1c04")

// WithFutures sets the USDⓈ-M futures base URLs, e.g.
// https://fapi.binance.com and wss://fstream.binance.com.
func (s *Source) WithFutures(rest, stream string) *Source {
	s.futuresREST, s.futuresStream = strings.TrimRight(rest, "/"), strings.TrimRight(stream, "/")
	return s
}

func (s *Source) urls(futures bool) (rest, stream, prefix string, err error) {
	if !futures {
		return s.rest, s.stream, "/api/v3", nil
	}
	if s.futuresREST == "" || s.futuresStream == "" {
		return "", "", "", errors.New("binance: futures endpoints are not configured")
	}
	return s.futuresREST, s.futuresStream, "/fapi/v1", nil
}

type depthRow struct {
	LastUpdateID int64       `json:"lastUpdateId"`
	Bids         [][2]string `json:"bids"`
	Asks         [][2]string `json:"asks"`
}

// DepthSnapshot returns ref's book, SnapshotLevels a side at most, and the
// update ID it stands at, in the platform's units.
func (s *Source) DepthSnapshot(ctx context.Context, ref ports.Reference, futures bool) (int64, []domain.Level, []domain.Level, error) {
	rest, _, prefix, err := s.urls(futures)
	if err != nil {
		return 0, nil, nil, err
	}
	var row depthRow
	q := url.Values{"symbol": {ref.Remote}, "limit": {strconv.Itoa(SnapshotLevels)}}
	if err := s.getAt(ctx, "depth", rest, prefix+"/depth", q, &row); err != nil {
		return 0, nil, nil, err
	}
	conv := newConverter(ref)
	bids, err := conv.levels(row.Bids)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("binance depth %s: %w", ref.Remote, err)
	}
	asks, err := conv.levels(row.Asks)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("binance depth %s: %w", ref.Remote, err)
	}
	return row.LastUpdateID, bids, asks, nil
}

type aggTradeRow struct {
	ID           int64  `json:"a"`
	Price        string `json:"p"`
	Quantity     string `json:"q"`
	Time         int64  `json:"T"`
	BuyerIsMaker bool   `json:"m"`
	// BestMatch keeps "M" (spot's best price match, true) out of
	// BuyerIsMaker: keys that differ only in case need a field each.
	BestMatch bool `json:"M"`
}

// RecentTrades returns ref's latest aggregate trades, oldest first.
func (s *Source) RecentTrades(ctx context.Context, ref ports.Reference, futures bool, limit int) ([]domain.Trade, error) {
	rest, _, prefix, err := s.urls(futures)
	if err != nil {
		return nil, err
	}
	var rows []aggTradeRow
	if err := s.getAt(ctx, "aggTrades", rest, prefix+"/aggTrades", url.Values{"symbol": {ref.Remote}, "limit": {strconv.Itoa(limit)}}, &rows); err != nil {
		return nil, err
	}
	out := make([]domain.Trade, 0, len(rows))
	for _, r := range rows {
		t, err := aggTrade(ref, futures, r)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// aggTrade converts an aggregate trade: its platform ID is derived from
// the source's, its number is the source's ID.
func aggTrade(ref ports.Reference, futures bool, r aggTradeRow) (domain.Trade, error) {
	p, err1 := decimal.NewFromString(r.Price)
	q, err2 := decimal.NewFromString(r.Quantity)
	if err1 != nil || err2 != nil {
		return domain.Trade{}, fmt.Errorf("binance trade %s %d: bad price %q or quantity %q", ref.Remote, r.ID, r.Price, r.Quantity)
	}
	conv := newConverter(ref)
	market := "spot"
	if futures {
		market = "futures"
	}
	side := "BUY"
	if r.BuyerIsMaker {
		side = "SELL" // the seller took the bid
	}
	if r.ID < 0 {
		return domain.Trade{}, fmt.Errorf("binance trade %s: negative ID %d", ref.Remote, r.ID)
	}
	price, qty := conv.price(p), conv.quantity(q)
	return domain.Trade{
		Symbol: ref.Symbol, ID: uuid.NewSHA1(tradeNamespace, []byte(market+":"+ref.Remote+":"+strconv.FormatInt(r.ID, 10))).String(),
		Number:   uint64(r.ID), //nolint:gosec // not negative (checked above)
		Price:    price,
		Quantity: qty, Quote: price.Mul(qty), TakerSide: side, At: time.UnixMilli(r.Time).UTC(),
	}, nil
}

// depthEvent is a depthUpdate. encoding/json matches keys regardless of
// case unless one matches exactly, so keys that differ only in case (e/E,
// U/u) each have a field.
type depthEvent struct {
	Event     string      `json:"e"`
	EventTime int64       `json:"E"`
	TxTime    int64       `json:"T"`
	Symbol    string      `json:"s"`
	First     int64       `json:"U"`
	Last      int64       `json:"u"`
	Prev      int64       `json:"pu"`
	Bids      [][2]string `json:"b"`
	Asks      [][2]string `json:"a"`
}

// aggEvent is an aggTrade (e/E and m/M differ only in case).
type aggEvent struct {
	Event        string `json:"e"`
	EventTime    int64  `json:"E"`
	Symbol       string `json:"s"`
	ID           int64  `json:"a"`
	Price        string `json:"p"`
	Quantity     string `json:"q"`
	FirstID      int64  `json:"f"`
	LastID       int64  `json:"l"`
	Time         int64  `json:"T"`
	BuyerIsMaker bool   `json:"m"`
	BestMatch    bool   `json:"M"`
}

// BookStream follows the depth updates (every 100 ms) and aggregate trades
// of refs on one combined connection, spot or futures, and passes them to
// on (on the connection's goroutine) in the platform's symbols and units.
// It returns when the connection ends; the caller reconnects.
func (s *Source) BookStream(ctx context.Context, refs []ports.Reference, futures bool, on ports.BookHandlers) error {
	_, stream, _, err := s.urls(futures)
	if err != nil {
		return err
	}
	names := make([]string, 0, 2*len(refs))
	byRemote := make(map[string]ports.Reference, len(refs))
	for _, r := range refs {
		lower := strings.ToLower(r.Remote)
		names = append(names, lower+"@depth@100ms", lower+"@aggTrade")
		byRemote[r.Remote] = r
	}
	conn, resp, err := websocket.Dial(ctx, stream+"/stream?streams="+strings.Join(names, "/"), &websocket.DialOptions{HTTPClient: s.client})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("binance book stream: %w", err)
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(4 << 20) // a busy book's update can be large
	for {
		read, cancel := context.WithTimeout(ctx, s.idle)
		_, data, err := conn.Read(read)
		cancel()
		if err != nil {
			if ctx.Err() == nil && errors.Is(read.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("binance book stream: nothing received for %s", s.idle)
			}
			return fmt.Errorf("binance book stream: %w", err)
		}
		var msg struct {
			Data json.RawMessage `json:"data"`
		}
		// Every key the header's fields match regardless of case needs a
		// field of its own: without EventTime, "E" (a number) would land in
		// Event and fail the whole message.
		var head struct {
			Event     string `json:"e"`
			EventTime int64  `json:"E"`
			Symbol    string `json:"s"`
		}
		if json.Unmarshal(data, &msg) != nil || json.Unmarshal(msg.Data, &head) != nil {
			continue
		}
		ref, ok := byRemote[head.Symbol]
		if !ok {
			continue
		}
		switch head.Event {
		case "depthUpdate":
			var ev depthEvent
			if on.Depth == nil || json.Unmarshal(msg.Data, &ev) != nil {
				continue
			}
			conv := newConverter(ref)
			bids, err1 := conv.levels(ev.Bids)
			asks, err2 := conv.levels(ev.Asks)
			if err1 != nil || err2 != nil {
				continue
			}
			on.Depth(ref.Symbol, domain.DepthDiff{First: ev.First, Last: ev.Last, Prev: ev.Prev, Bids: bids, Asks: asks})
		case "aggTrade":
			var ev aggEvent
			if on.Trade == nil || json.Unmarshal(msg.Data, &ev) != nil {
				continue
			}
			t, err := aggTrade(ref, futures, aggTradeRow{
				ID: ev.ID, Price: ev.Price, Quantity: ev.Quantity, Time: ev.Time, BuyerIsMaker: ev.BuyerIsMaker,
			})
			if err == nil {
				on.Trade(t)
			}
		}
	}
}

// levels converts [price, quantity] rows; a quantity of zero stays zero
// (it removes the level).
func (c converter) levels(rows [][2]string) ([]domain.Level, error) {
	out := make([]domain.Level, 0, len(rows))
	for _, r := range rows {
		p, err1 := decimal.NewFromString(r[0])
		q, err2 := decimal.NewFromString(r[1])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("bad level %q x %q", r[0], r[1])
		}
		out = append(out, domain.Level{Price: c.price(p), Quantity: c.quantity(q)})
	}
	return out, nil
}
