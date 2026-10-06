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
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/marketdata/ports"
)

// Perpetual mark prices and settled funding rates (coin-M design §3.1):
// the markPrice@1s streams and /fundingRate of the USDⓈ-M futures market
// (fstream, fapi) and of the COIN-M one (dstream, dapi).

// fundingPage is the most rows a /fundingRate answer holds.
const fundingPage = 1000

// WithCoinFutures sets the COIN-M futures base URLs, e.g.
// https://dapi.binance.com and wss://dstream.binance.com.
func (s *Source) WithCoinFutures(rest, stream string) *Source {
	s.coinREST, s.coinStream = strings.TrimRight(rest, "/"), strings.TrimRight(stream, "/")
	return s
}

// futuresURLs returns a futures market's REST base, REST path prefix and
// stream base: COIN-M's when coin is set, USDⓈ-M's otherwise.
func (s *Source) futuresURLs(coin bool) (rest, prefix, stream string, err error) {
	if coin {
		if s.coinREST == "" || s.coinStream == "" {
			return "", "", "", errors.New("binance: COIN-M futures endpoints are not configured")
		}
		return s.coinREST, "/dapi/v1", s.coinStream, nil
	}
	rest, stream, prefix, err = s.urls(true)
	return rest, prefix, stream, err
}

// markEvent is a markPriceUpdate on a combined stream. The mark price "p"
// and the estimated settlement price "P" differ only in case, as do "e"
// and "E": each has its own field.
type markEvent struct {
	Data struct {
		Event       string `json:"e"`
		EventTime   int64  `json:"E"`
		Symbol      string `json:"s"`
		Mark        string `json:"p"`
		SettleEst   string `json:"P"`
		Index       string `json:"i"`
		FundingRate string `json:"r"`
		NextFunding int64  `json:"T"`
	} `json:"data"`
}

// MarkStream follows the markPrice@1s streams of refs' perpetuals on one
// combined connection of the COIN-M market (coin) or the USDⓈ-M one, and
// passes every update to on (on the connection's goroutine) in the
// platform's symbols and units. It returns when the connection ends or
// stays silent for the idle timeout; the caller reconnects.
func (s *Source) MarkStream(ctx context.Context, refs []ports.Reference, coin bool, on func(domain.ReferenceMark)) error {
	_, _, stream, err := s.futuresURLs(coin)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(refs))
	byRemote := make(map[string]ports.Reference, len(refs))
	for _, r := range refs {
		names = append(names, strings.ToLower(r.Remote)+"@markPrice@1s")
		byRemote[r.Remote] = r
	}
	conn, resp, err := websocket.Dial(ctx, stream+"/stream?streams="+strings.Join(names, "/"), &websocket.DialOptions{HTTPClient: s.client})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("binance mark stream: %w", err)
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(1 << 16)
	for {
		read, cancel := context.WithTimeout(ctx, s.idle)
		_, data, err := conn.Read(read)
		cancel()
		if err != nil {
			if ctx.Err() == nil && errors.Is(read.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("binance mark stream: nothing received for %s", s.idle)
			}
			return fmt.Errorf("binance mark stream: %w", err)
		}
		var ev markEvent
		if json.Unmarshal(data, &ev) != nil || ev.Data.Event != "markPriceUpdate" {
			continue
		}
		ref, ok := byRemote[ev.Data.Symbol]
		if !ok {
			continue
		}
		if m, err := referenceMark(ref, ev); err == nil {
			on(m)
		}
	}
}

// referenceMark converts a markPriceUpdate; a rate of "" (a delivery
// contract's) leaves HasRate false.
func referenceMark(ref ports.Reference, ev markEvent) (domain.ReferenceMark, error) {
	d := ev.Data
	mark, err1 := decimal.NewFromString(d.Mark)
	index, err2 := decimal.NewFromString(d.Index)
	if err1 != nil || err2 != nil {
		return domain.ReferenceMark{}, fmt.Errorf("binance mark %s: bad mark %q or index %q", ref.Remote, d.Mark, d.Index)
	}
	conv := newConverter(ref)
	out := domain.ReferenceMark{
		Symbol: ref.Symbol, Mark: conv.price(mark), Index: conv.price(index), At: time.UnixMilli(d.EventTime).UTC(),
	}
	if d.NextFunding > 0 {
		out.NextFunding = time.UnixMilli(d.NextFunding).UTC()
	}
	if d.FundingRate != "" {
		rate, err := decimal.NewFromString(d.FundingRate)
		if err != nil {
			return domain.ReferenceMark{}, fmt.Errorf("binance mark %s: bad funding rate %q", ref.Remote, d.FundingRate)
		}
		out.FundingRate, out.HasRate = rate, true
	}
	return out, nil
}

// fundingRow is a /fundingRate answer's row.
type fundingRow struct {
	Symbol      string `json:"symbol"`
	FundingTime int64  `json:"fundingTime"`
	FundingRate string `json:"fundingRate"`
	MarkPrice   string `json:"markPrice"`
}

// SettledFunding returns the funding rates refs' perpetuals settled with
// funding times in [from, to], oldest first. USDⓈ-M answers every symbol
// in one request when the rows fit a page (one request each otherwise);
// COIN-M wants a symbol, so one request each.
func (s *Source) SettledFunding(ctx context.Context, refs []ports.Reference, coin bool, from, to time.Time) ([]domain.SettledFunding, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	rest, prefix, _, err := s.futuresURLs(coin)
	if err != nil {
		return nil, err
	}
	window := url.Values{
		"startTime": {strconv.FormatInt(from.UnixMilli(), 10)}, "endTime": {strconv.FormatInt(to.UnixMilli(), 10)},
		"limit": {strconv.Itoa(fundingPage)},
	}
	byRemote := make(map[string]ports.Reference, len(refs))
	for _, r := range refs {
		byRemote[r.Remote] = r
	}
	if !coin && len(refs) > 1 {
		var rows []fundingRow
		if err := s.getAt(ctx, "funding rates", rest, prefix+"/fundingRate", window, &rows); err != nil {
			return nil, err
		}
		// A full page may have left rows out: they share their funding
		// time, so the window cannot be paged.
		if len(rows) < fundingPage {
			return settled(byRemote, rows)
		}
	}
	var all []domain.SettledFunding
	for _, r := range refs {
		q := url.Values{"symbol": {r.Remote}}
		for k, v := range window {
			q[k] = v
		}
		var rows []fundingRow
		if err := s.getAt(ctx, "funding rates", rest, prefix+"/fundingRate", q, &rows); err != nil {
			return nil, err
		}
		list, err := settled(byRemote, rows)
		if err != nil {
			return nil, err
		}
		all = append(all, list...)
	}
	return all, nil
}

// settled converts the rows of refs' symbols, leaving the others out.
func settled(refs map[string]ports.Reference, rows []fundingRow) ([]domain.SettledFunding, error) {
	var out []domain.SettledFunding
	for _, row := range rows {
		ref, ok := refs[row.Symbol]
		if !ok {
			continue
		}
		rate, err1 := decimal.NewFromString(row.FundingRate)
		mark, err2 := decimal.NewFromString(row.MarkPrice)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("binance funding rate %s: bad rate %q or mark %q", row.Symbol, row.FundingRate, row.MarkPrice)
		}
		out = append(out, domain.SettledFunding{
			Symbol: ref.Symbol, FundingTime: time.UnixMilli(row.FundingTime).UTC(), Rate: rate, Mark: newConverter(ref).price(mark),
		})
	}
	return out, nil
}
