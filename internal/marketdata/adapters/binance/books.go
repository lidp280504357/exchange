package binance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/marketdata/ports"
)

// Order books and trades (ADR-0010, ADR-0015): REST snapshots and the
// depth (every 100 or 500 ms, depthStream) and aggTrade streams, of spot
// pairs on the spot endpoints and of perpetual contracts on the futures
// ones of their market (USDⓈ-M or COIN-M).

// SnapshotLevels is how deep a book snapshot goes (a spot request weighs
// 50 at this depth, a futures one 20), SlowSnapshotLevels that of a
// perpetual on 500 ms updates (weighing 10): the public books carry 200
// levels a side.
const (
	SnapshotLevels     = 1000
	SlowSnapshotLevels = 500
)

// tradeNamespace seeds the platform IDs of the reference market's trades.
var tradeNamespace = uuid.MustParse("5f0b6c8e-2d4a-4c1e-9b7a-3e8d2f6a1c04")

// WithFutures sets the USDⓈ-M futures base URLs, e.g.
// https://fapi.binance.com and wss://fstream.binance.com.
func (s *Source) WithFutures(rest, stream string) *Source {
	s.futuresREST, s.futuresStream = strings.TrimRight(rest, "/"), strings.TrimRight(stream, "/")
	return s
}

// endpoints returns a market's REST base, REST path prefix and stream
// base.
func (s *Source) endpoints(market string) (rest, prefix, stream string, err error) {
	switch market {
	case ports.MarketSpot:
		return s.rest, "/api/v3", s.stream, nil
	case ports.MarketUSDM:
		if s.futuresREST == "" || s.futuresStream == "" {
			return "", "", "", errors.New("binance: USDⓈ-M futures endpoints are not configured")
		}
		return s.futuresREST, "/fapi/v1", s.futuresStream, nil
	case ports.MarketCoinM:
		if s.coinREST == "" || s.coinStream == "" {
			return "", "", "", errors.New("binance: COIN-M futures endpoints are not configured")
		}
		return s.coinREST, "/dapi/v1", s.coinStream, nil
	}
	return "", "", "", fmt.Errorf("binance: unknown market %q", market)
}

// market returns the market of refs, all of which must share it.
func market(refs []ports.Reference) (string, error) {
	if len(refs) == 0 {
		return "", errors.New("binance: no symbols")
	}
	for _, r := range refs[1:] {
		if r.Market != refs[0].Market {
			return "", fmt.Errorf("binance: %s and %s are on different markets", refs[0].Symbol, r.Symbol)
		}
	}
	return refs[0].Market, nil
}

type depthRow struct {
	LastUpdateID int64       `json:"lastUpdateId"`
	Bids         [][2]string `json:"bids"`
	Asks         [][2]string `json:"asks"`
}

// DepthSnapshot returns ref's book, SnapshotLevels a side at most
// (SlowSnapshotLevels for a perpetual on 500 ms updates), and the update ID
// it stands at, in the platform's units.
func (s *Source) DepthSnapshot(ctx context.Context, ref ports.Reference) (int64, []domain.Level, []domain.Level, error) {
	rest, prefix, _, err := s.endpoints(ref.Market)
	if err != nil {
		return 0, nil, nil, err
	}
	var row depthRow
	levels := SnapshotLevels
	if s.slow(ref) {
		levels = SlowSnapshotLevels
	}
	q := url.Values{"symbol": {ref.Remote}, "limit": {strconv.Itoa(levels)}}
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
func (s *Source) RecentTrades(ctx context.Context, ref ports.Reference, limit int) ([]domain.Trade, error) {
	rest, prefix, _, err := s.endpoints(ref.Market)
	if err != nil {
		return nil, err
	}
	var rows []aggTradeRow
	if err := s.getAt(ctx, "aggTrades", rest, prefix+"/aggTrades", url.Values{"symbol": {ref.Remote}, "limit": {strconv.Itoa(limit)}}, &rows); err != nil {
		return nil, err
	}
	out := make([]domain.Trade, 0, len(rows))
	for _, r := range rows {
		t, err := aggTrade(ref, r)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// aggTrade converts an aggregate trade: its platform ID is derived from
// the source's, its number is the source's ID. A COIN-M trade's quantity
// is whole contracts and its quote amount their USD value (coin-M design
// §2.4, ⑪).
func aggTrade(ref ports.Reference, r aggTradeRow) (domain.Trade, error) {
	p, err1 := decimal.NewFromString(r.Price)
	q, err2 := decimal.NewFromString(r.Quantity)
	if err1 != nil || err2 != nil {
		return domain.Trade{}, fmt.Errorf("binance trade %s %d: bad price %q or quantity %q", ref.Remote, r.ID, r.Price, r.Quantity)
	}
	conv := newConverter(ref)
	market := "spot"
	switch ref.Market {
	case ports.MarketUSDM:
		market = "futures"
	case ports.MarketCoinM:
		market = "coinm"
	}
	side := "BUY"
	if r.BuyerIsMaker {
		side = "SELL" // the seller took the bid
	}
	if r.ID < 0 {
		return domain.Trade{}, fmt.Errorf("binance trade %s: negative ID %d", ref.Remote, r.ID)
	}
	price, qty := conv.price(p), conv.quantity(q)
	quote := price.Mul(qty)
	if ref.Market == ports.MarketCoinM {
		quote = qty.Mul(ref.ContractSize)
	}
	return domain.Trade{
		Symbol: ref.Symbol, ID: uuid.NewSHA1(tradeNamespace, []byte(market+":"+ref.Remote+":"+strconv.FormatInt(r.ID, 10))).String(),
		Number:   uint64(r.ID), //nolint:gosec // not negative (checked above)
		Price:    price,
		Quantity: qty, Quote: quote, TakerSide: side, At: time.UnixMilli(r.Time).UTC(),
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

// BookStream follows the depth updates (depthStream) and aggregate trades
// of refs, all of one market, and passes them to on in the platform's
// symbols and units. Spot and COIN-M send both on one combined
// connection; USDⓈ-M futures sends the books and the trades on paths of
// their own, so the trades have a connection of their own, kept up
// (reconnected with backoff, each failure passed to on.Failed) for as
// long as the books' lasts. It returns when the books' connection ends;
// the caller reconnects.
func (s *Source) BookStream(ctx context.Context, refs []ports.Reference, on ports.BookHandlers) error {
	m, err := market(refs)
	if err != nil {
		return err
	}
	_, _, stream, err := s.endpoints(m)
	if err != nil {
		return err
	}
	var both, depths, trades []string
	byRemote := make(map[string]ports.Reference, len(refs))
	for _, r := range refs {
		depth, trade := s.depthStream(r), strings.ToLower(r.Remote)+"@aggTrade"
		both = append(both, depth, trade)
		depths, trades = append(depths, depth), append(trades, trade)
		byRemote[r.Remote] = r
	}
	handle := func(data []byte) { s.bookMessage(data, byRemote, on) }
	// A busy book's update can be large.
	if m != ports.MarketUSDM {
		return s.listen(ctx, "binance book stream", stream+allStreams+strings.Join(both, "/"), 4<<20, s.idle, handle)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		backoff := time.Second
		for ctx.Err() == nil {
			started := time.Now()
			err := s.listen(ctx, "binance trade stream", stream+marketStreams+strings.Join(trades, "/"), 1<<20, tradeIdle, handle)
			if ctx.Err() != nil {
				return
			}
			if on.Failed != nil {
				on.Failed(err)
			}
			if time.Since(started) > time.Minute {
				backoff = time.Second
			}
			select {
			case <-ctx.Done():
			case <-time.After(backoff):
			}
			backoff = min(2*backoff, 30*time.Second)
		}
	}()
	err = s.listen(ctx, "binance book stream", stream+publicStreams+strings.Join(depths, "/"), 4<<20, s.idle, handle)
	cancel()
	wg.Wait()
	return err
}

// slow reports whether r is a perpetual whose book follows depth updates
// every 500 ms: those of bases outside fastDepth (BTC and ETH by default,
// WithFastDepth), coin-margined design §3.4: about 105 contracts, whose
// market.depth the test server's Redpanda, limited to 2 GB, has to take.
// Spot has no 500 ms stream: every pair's book takes 100 ms.
func (s *Source) slow(r ports.Reference) bool {
	base, _, _ := strings.Cut(r.Symbol, "-")
	return r.Market != ports.MarketSpot && !s.fastDepth[base]
}

// depthStream is the name of r's depth stream.
func (s *Source) depthStream(r ports.Reference) string {
	if s.slow(r) {
		return strings.ToLower(r.Remote) + "@depth@500ms"
	}
	return strings.ToLower(r.Remote) + "@depth@100ms"
}

// tradeIdle ends a futures trade connection silent for this long: a group
// of quiet contracts may see no trade for a while, and the books do not
// depend on it.
const tradeIdle = 5 * time.Minute

// bookMessage passes one depth update or trade of refs to on.
func (s *Source) bookMessage(data []byte, refs map[string]ports.Reference, on ports.BookHandlers) {
	var msg struct {
		Data json.RawMessage `json:"data"`
	}
	// Every key the header's fields match regardless of case needs a field
	// of its own: without EventTime, "E" (a number) would land in Event and
	// fail the whole message.
	var head struct {
		Event     string `json:"e"`
		EventTime int64  `json:"E"`
		Symbol    string `json:"s"`
	}
	if json.Unmarshal(data, &msg) != nil || json.Unmarshal(msg.Data, &head) != nil {
		return
	}
	ref, ok := refs[head.Symbol]
	if !ok {
		return
	}
	switch head.Event {
	case "depthUpdate":
		var ev depthEvent
		if on.Depth == nil || json.Unmarshal(msg.Data, &ev) != nil {
			return
		}
		conv := newConverter(ref)
		bids, err1 := conv.levels(ev.Bids)
		asks, err2 := conv.levels(ev.Asks)
		if err1 != nil || err2 != nil {
			return
		}
		on.Depth(ref.Symbol, domain.DepthDiff{First: ev.First, Last: ev.Last, Prev: ev.Prev, Bids: bids, Asks: asks})
	case "aggTrade":
		var ev aggEvent
		if on.Trade == nil || json.Unmarshal(msg.Data, &ev) != nil {
			return
		}
		t, err := aggTrade(ref, aggTradeRow{
			ID: ev.ID, Price: ev.Price, Quantity: ev.Quantity, Time: ev.Time, BuyerIsMaker: ev.BuyerIsMaker,
		})
		if err == nil {
			on.Trade(t)
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
