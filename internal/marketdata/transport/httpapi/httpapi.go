// Package httpapi serves the public market data endpoints
// (api/openapi/market.yaml): tickers, depth, trades and candles, and the
// contracts' mark prices and funding rates.
package httpapi

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	marketv1 "github.com/skill/exchange/api/gen/go/exchange/market/v1"
	"github.com/skill/exchange/internal/marketdata/application"
	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/marketdata/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
)

// Handler serves the market data; no sign-in needed.
type Handler struct {
	Svc *application.Service
	// Tickers picks each symbol's ticker, the reference market's or the
	// platform's (ADR-0010).
	Tickers *application.Tickers
	// Ref is the reference feed and Guard watches it; nil when none is
	// configured.
	Ref   *application.ReferenceFeed
	Guard *application.FeedGuard
	Marks *application.Marks
	// RefKlines serves the charts in reference mode; nil without a feed.
	RefKlines *application.ReferenceCandles
	// Books serves the reference market's book and trades of the symbols
	// that show them (ADR-0010); nil without a feed.
	Books *application.Books
	// Sparks serves the market lists' trend lines.
	Sparks *application.Sparklines
	// Platform is the reference of the pairs no reference market follows
	// (their own market, else the simulated market's price); nil: none.
	Platform *application.PlatformReference
	// Listed tells whether the reference market lists a symbol, for the
	// admin console's checks; nil answers unavailable.
	Listed interface {
		Listed(ctx context.Context, symbol string, futures bool) (bool, error)
	}
	Now func() time.Time
}

// Routes mounts the endpoints on r.
func (h *Handler) Routes(r chi.Router) {
	r.Get("/v1/market/tickers", h.tickers)
	r.Get("/v1/market/summary", h.summary)
	r.Get("/v1/market/sparklines", h.sparklines)
	r.Get("/v1/market/{symbol}/ticker", h.ticker)
	r.Get("/v1/market/{symbol}/depth", h.depth)
	r.Get("/v1/market/{symbol}/trades", h.trades)
	r.Get("/v1/market/{symbol}/candles", h.candles)
	r.Get("/v1/market/{symbol}/mark-price", h.markPrice)
	r.Get("/v1/market/{symbol}/funding-rates", h.fundingRates)
	// Internal: the gateway does not route /internal, so reference data
	// (Binance, §11.9) never reaches clients; the trading service and the
	// market maker read it, and the index components for audits.
	r.Get("/internal/market/{symbol}/reference", h.reference)
	r.Put("/internal/market/{symbol}/simulated-price", h.simulatedPrice)
	r.Get("/internal/market/{symbol}/mark", h.markInternal)
	r.Get("/internal/market/feed", h.feed)
	r.Get("/internal/market/reference-symbols/{remote}", h.referenceSymbol)
}

// referenceSymbol tells the admin console whether Binance lists a symbol
// (BTCUSDT) on its spot market and on USDⓈ-M futures, before a pair or a
// contract follows it: a symbol Binance does not know fails the batched
// reads of every pair.
func (h *Handler) referenceSymbol(w http.ResponseWriter, r *http.Request) {
	remote := strings.ToUpper(chi.URLParam(r, "remote"))
	if h.Listed == nil {
		httpx.WriteError(w, r, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "no reference market is configured"))
		return
	}
	if !remoteSymbolRE.MatchString(remote) {
		httpx.WriteError(w, r, apperr.Invalid("a reference symbol is 2-20 letters and digits (BTCUSDT)"))
		return
	}
	spot, err := h.Listed.Listed(r.Context(), remote, false)
	if err != nil {
		httpx.WriteError(w, r, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "the reference market did not answer"))
		return
	}
	futures, err := h.Listed.Listed(r.Context(), remote, true)
	if err != nil {
		httpx.WriteError(w, r, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "the reference market did not answer"))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"symbol": remote, "spot": spot, "futures": futures})
}

var remoteSymbolRE = regexp.MustCompile(`^[A-Z0-9]{2,20}$`)

type haltJSON struct {
	Symbol   string `json:"symbol"`
	HaltedAt string `json:"halted_at"`
}

// feed reports the reference feed's state and the pairs halted for it,
// for the admin console.
func (h *Handler) feed(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"state": application.FeedOff, "received_at": nil, "followed": []string{}, "halted": []haltJSON{}}
	if h.Guard != nil {
		st, err := h.Guard.Status(r.Context())
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		halted := make([]haltJSON, 0, len(st.Halted))
		for _, x := range st.Halted {
			halted = append(halted, haltJSON{Symbol: x.Symbol, HaltedAt: x.HaltedAt.UTC().Format(time.RFC3339)})
		}
		out = map[string]any{"state": st.State, "received_at": stamp(st.Received), "followed": st.Followed, "halted": halted}
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) reference(w http.ResponseWriter, r *http.Request) {
	s := symbol(r)
	out := map[string]any{"symbol": s, "source": nil, "price": nil, "updated_at": nil, "fresh": false}
	if h.Ref != nil {
		if ref, fresh := h.Ref.Latest(s); !ref.At.IsZero() {
			out["source"], out["price"], out["fresh"] = ref.Source, ref.Price.String(), fresh
			out["updated_at"] = ref.At.UTC().Format(time.RFC3339Nano)
		}
	}
	if out["source"] == nil && h.Platform != nil {
		if ref, ok := h.Platform.Price(r.Context(), s); ok {
			out["source"], out["price"], out["fresh"] = ref.Source, ref.Price.String(), true
			out["updated_at"] = ref.At.UTC().Format(time.RFC3339Nano)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, out)
}

// simulatedPrice keeps the price the simulated market reports for a pair
// no reference market follows (ASTRA design §4): the pair's reference
// while its own book has no middle.
func (h *Handler) simulatedPrice(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Price string `json:"price"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	p, err := decimal.NewFromString(body.Price)
	if err != nil {
		httpx.WriteError(w, r, apperr.Invalid("price is a decimal"))
		return
	}
	if h.Platform == nil {
		httpx.WriteError(w, r, application.ErrFollowed)
		return
	}
	if err := h.Platform.Report(r.Context(), symbol(r), p); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// live lets clients and Cloudflare reuse an answer for a second at most.
func live(w http.ResponseWriter) { w.Header().Set("Cache-Control", "public, max-age=1") }

func symbol(r *http.Request) string { return strings.ToUpper(chi.URLParam(r, "symbol")) }

var symbolRE = regexp.MustCompile(`^[A-Z0-9]{2,10}-[A-Z0-9]{2,10}(-PERP)?$`)

// sparklines returns the trend lines of the market lists for up to 60
// symbols (symbols=BTC-USDT,ETH-USDT): range 7d (the default) thins a
// week of hourly closes to 56 points, 24h gives the last 24. A symbol
// without candles is left out.
func (h *Handler) sparklines(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rng := q.Get("range")
	if rng == "" {
		rng = application.SparkWeek
	}
	if rng != application.SparkWeek && rng != application.SparkDay {
		httpx.WriteError(w, r, apperr.Invalid("range must be 7d or 24h"))
		return
	}
	var symbols []string
	for _, s := range strings.Split(q.Get("symbols"), ",") {
		s = strings.ToUpper(strings.TrimSpace(s))
		switch {
		case s == "" || slices.Contains(symbols, s):
		case !symbolRE.MatchString(s):
			httpx.WriteError(w, r, apperr.Invalid("not a symbol: "+s))
			return
		default:
			symbols = append(symbols, s)
		}
	}
	if len(symbols) == 0 || len(symbols) > 60 {
		httpx.WriteError(w, r, apperr.Invalid("symbols takes 1 to 60 symbols"))
		return
	}
	lines := h.Sparks.Get(r.Context(), symbols, rng)
	w.Header().Set("Cache-Control", "public, max-age=60")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"range": rng, "interval": "1h", "sparklines": lines})
}

func limit(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return n
}

// tickerJSON: prices are null while unknown (a pair that never traded).
type tickerJSON struct {
	Symbol      string  `json:"symbol"`
	Rank        *int32  `json:"rank"`
	Last        *string `json:"last"`
	Open        *string `json:"open"`
	High        *string `json:"high"`
	Low         *string `json:"low"`
	Volume      string  `json:"volume"`
	QuoteVolume string  `json:"quote_volume"`
	TradeCount  int64   `json:"trade_count"`
	Change      *string `json:"change"`
	Bid         *string `json:"bid"`
	Ask         *string `json:"ask"`
	UpdatedAt   string  `json:"updated_at"`
	// LastTradeAt is the platform's own last trade (null for a reference
	// ticker): how fresh last is.
	LastTradeAt *string `json:"last_trade_at"`
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// tickerJSON renders a ticker: a reference ticker is as of when the
// source computed it, the platform's as of now.
func (h *Handler) tickerJSON(t domain.Ticker, ranks map[string]int32) tickerJSON {
	at := t.At
	if at.IsZero() {
		at = h.Now()
	}
	p := application.TickerProto(t, at)
	out := tickerJSON{
		Symbol: p.GetSymbol(), Last: optional(p.GetLast()), Open: optional(p.GetOpen()), High: optional(p.GetHigh()),
		Low: optional(p.GetLow()), Volume: p.GetVolume(), QuoteVolume: p.GetQuoteVolume(), TradeCount: p.GetTradeCount(),
		Change: optional(p.GetChange()), Bid: optional(p.GetBid()), Ask: optional(p.GetAsk()),
		UpdatedAt: p.GetUpdatedAt().AsTime().UTC().Format(time.RFC3339Nano),
	}
	if t.At.IsZero() && !t.LastTradeAt.IsZero() {
		last := t.LastTradeAt.UTC().Format(time.RFC3339Nano)
		out.LastTradeAt = &last
	}
	if r := ranks[t.Symbol]; r > 0 {
		out.Rank = &r
	}
	return out
}

// ranks returns the symbols' ranks; none when the listing is unavailable
// (a ticker is still worth serving).
func (h *Handler) ranks(r *http.Request) map[string]int32 {
	ranks, err := h.Tickers.Ranks(r.Context())
	if err != nil {
		return nil
	}
	return ranks
}

func (h *Handler) tickers(w http.ResponseWriter, r *http.Request) {
	list, err := h.Tickers.All(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	ranks := h.ranks(r)
	out := make([]tickerJSON, 0, len(list))
	for _, t := range list {
		out = append(out, h.tickerJSON(t, ranks))
	}
	live(w)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tickers": out})
}

func (h *Handler) ticker(w http.ResponseWriter, r *http.Request) {
	t, err := h.Tickers.Ticker(r.Context(), symbol(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	live(w)
	httpx.WriteJSON(w, http.StatusOK, h.tickerJSON(t, h.ranks(r)))
}

// summary serves the home page's market overview.
func (h *Handler) summary(w http.ResponseWriter, r *http.Request) {
	n := limit(r)
	if n <= 0 || n > 20 {
		n = 5
	}
	s, err := h.Tickers.Summary(r.Context(), n)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	ranks := h.ranks(r)
	render := func(list []domain.Ticker) []tickerJSON {
		out := make([]tickerJSON, 0, len(list))
		for _, t := range list {
			out = append(out, h.tickerJSON(t, ranks))
		}
		return out
	}
	live(w)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"gainers": render(s.Gainers), "losers": render(s.Losers), "turnover": render(s.Turnover),
		"updated_at": h.Now().UTC().Format(time.RFC3339Nano),
	})
}

// levels renders price levels as [price, quantity] pairs.
func levels(in []*marketv1.PriceLevel) [][2]string {
	out := make([][2]string, len(in))
	for i, l := range in {
		out[i] = [2]string{l.GetPrice(), l.GetQuantity()}
	}
	return out
}

func (h *Handler) depth(w http.ResponseWriter, r *http.Request) {
	d, err := h.Svc.Depth(r.Context(), symbol(r), limit(r)) // checks the symbol is listed
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if h.Books != nil {
		n := limit(r)
		if n <= 0 || n > application.MaxDepth {
			n = 100
		}
		if ref, ok := h.Books.Depth(d.GetSymbol(), n); ok {
			d = ref
		}
	}
	var updated *string
	if d.GetTakenAt() != nil {
		s := d.GetTakenAt().AsTime().UTC().Format(time.RFC3339Nano)
		updated = &s
	}
	live(w)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"symbol": d.GetSymbol(), "sequence": d.GetSequence(), "bids": levels(d.GetBids()), "asks": levels(d.GetAsks()),
		"updated_at": updated,
	})
}

type tradeJSON struct {
	TradeID       string `json:"trade_id"`
	TradeNumber   uint64 `json:"trade_number"`
	Price         string `json:"price"`
	Quantity      string `json:"quantity"`
	QuoteQuantity string `json:"quote_quantity"`
	TakerSide     string `json:"taker_side"`
	ExecutedAt    string `json:"executed_at"`
}

// toTradeJSON renders a public trade.
func toTradeJSON(t domain.Trade) tradeJSON {
	return tradeJSON{
		TradeID: t.ID, TradeNumber: t.Number, Price: t.Price.String(), Quantity: t.Quantity.String(),
		QuoteQuantity: t.Quote.String(), TakerSide: t.TakerSide, ExecutedAt: t.At.UTC().Format(time.RFC3339Nano),
	}
}

func (h *Handler) trades(w http.ResponseWriter, r *http.Request) {
	s := symbol(r)
	list, err := h.Svc.Trades(r.Context(), s, limit(r)) // checks the symbol is listed
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if h.Books != nil {
		n := limit(r)
		if n <= 0 || n > application.RecentTrades {
			n = 50
		}
		if ref, ok := h.Books.Trades(s, n); ok {
			list = ref
		}
	}
	out := make([]tradeJSON, 0, len(list))
	for _, t := range list {
		out = append(out, toTradeJSON(t))
	}
	live(w)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"symbol": s, "trades": out})
}

type candleJSON struct {
	OpenTime    string `json:"open_time"`
	Open        string `json:"open"`
	High        string `json:"high"`
	Low         string `json:"low"`
	Close       string `json:"close"`
	Volume      string `json:"volume"`
	QuoteVolume string `json:"quote_volume"`
	TradeCount  int64  `json:"trade_count"`
	Closed      bool   `json:"closed"`
}

func timeParam(r *http.Request, name string) (time.Time, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, apperr.Invalid(name + " must be an RFC 3339 time")
	}
	return t, nil
}

func (h *Handler) candles(w http.ResponseWriter, r *http.Request) {
	from, err := timeParam(r, "from")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	to, err := timeParam(r, "to")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	s, interval := symbol(r), r.URL.Query().Get("interval")
	var list []domain.Candle
	if ref, ok := h.referenceKlines(r, s); ok {
		list, err = h.RefKlines.Candles(r.Context(), s, ref, interval, from, to, limit(r))
	} else {
		list, err = h.Svc.Candles(r.Context(), s, interval, from, to, limit(r))
	}
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	now := h.Now()
	out := make([]candleJSON, 0, len(list))
	for _, c := range list {
		out = append(out, candleJSON{
			OpenTime: c.OpenTime.UTC().Format(time.RFC3339), Open: c.Open.String(), High: c.High.String(), Low: c.Low.String(),
			Close: c.Close.String(), Volume: c.Volume.String(), QuoteVolume: c.QuoteVolume.String(), TradeCount: c.Trades,
			Closed: c.Closed(now),
		})
	}
	live(w)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"symbol": s, "interval": interval, "candles": out})
}

// referenceKlines reports whether the symbol's chart shows reference
// candles (market.reference_kline) and of which reference market.
func (h *Handler) referenceKlines(r *http.Request, symbol string) (ports.Reference, bool) {
	if h.RefKlines == nil {
		return ports.Reference{}, false
	}
	return h.RefKlines.Serves(r.Context(), symbol)
}

// ErrUnknownContract is returned for a symbol that is not a contract.
var ErrUnknownContract = apperr.NotFound("no such contract")

func stamp(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(time.RFC3339Nano)
	return &s
}

func price(d decimal.Decimal) *string {
	if d.IsZero() {
		return nil
	}
	s := d.String()
	return &s
}

// markJSON: the prices are the last computed, null before the first;
// degraded is set while the contract has had none for 10 seconds.
type markJSON struct {
	Symbol          string  `json:"symbol"`
	IndexSymbol     string  `json:"index_symbol"`
	MarkPrice       *string `json:"mark_price"`
	IndexPrice      *string `json:"index_price"`
	FundingRate     string  `json:"funding_rate"`
	InterestRate    string  `json:"interest_rate"`
	NextFundingTime string  `json:"next_funding_time"`
	Degraded        bool    `json:"degraded"`
	UpdatedAt       *string `json:"updated_at"`
}

func toMarkJSON(p application.MarkPrice) markJSON {
	return markJSON{
		Symbol: p.Symbol, IndexSymbol: p.IndexSymbol, MarkPrice: price(p.Mark), IndexPrice: price(p.Index),
		FundingRate: p.FundingRate.String(), InterestRate: p.InterestRate.String(),
		NextFundingTime: p.NextFunding.UTC().Format(time.RFC3339), Degraded: p.Degraded, UpdatedAt: stamp(p.At),
	}
}

func (h *Handler) markPrice(w http.ResponseWriter, r *http.Request) {
	p, ok := h.Marks.Latest(symbol(r))
	if !ok {
		httpx.WriteError(w, r, ErrUnknownContract)
		return
	}
	live(w)
	httpx.WriteJSON(w, http.StatusOK, toMarkJSON(p))
}

type componentJSON struct {
	Source   string `json:"source"`
	Price    string `json:"price"`
	Weight   int32  `json:"weight"`
	Included bool   `json:"included"`
}

// markInternal adds what the index was made of, for audits (§5.11).
func (h *Handler) markInternal(w http.ResponseWriter, r *http.Request) {
	p, ok := h.Marks.Latest(symbol(r))
	if !ok {
		httpx.WriteError(w, r, ErrUnknownContract)
		return
	}
	comps := make([]componentJSON, 0, len(p.Components))
	for _, c := range p.Components {
		comps = append(comps, componentJSON{Source: c.Source, Price: c.Price.String(), Weight: c.Weight, Included: c.Included})
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"mark": toMarkJSON(p), "basis": p.Basis.String(), "premium": p.Premium.String(), "samples": p.Samples,
		"components": comps,
	})
}

type fundingJSON struct {
	FundingTime  string `json:"funding_time"`
	FundingRate  string `json:"funding_rate"`
	MarkPrice    string `json:"mark_price"`
	IndexPrice   string `json:"index_price"`
	Premium      string `json:"premium"`
	InterestRate string `json:"interest_rate"`
	Samples      int64  `json:"samples"`
	SettledAt    string `json:"settled_at"`
}

func (h *Handler) fundingRates(w http.ResponseWriter, r *http.Request) {
	from, err := timeParam(r, "from")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	to, err := timeParam(r, "to")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if to.IsZero() {
		to = h.Now().Add(24 * time.Hour)
	}
	n := limit(r)
	if n <= 0 || n > 1000 {
		n = 100
	}
	s := symbol(r)
	list, err := h.Marks.Settled(r.Context(), s, from, to, n)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if _, known := h.Marks.Latest(s); !known && len(list) == 0 {
		httpx.WriteError(w, r, ErrUnknownContract)
		return
	}
	out := make([]fundingJSON, 0, len(list))
	for _, p := range list {
		out = append(out, fundingJSON{
			FundingTime: p.FundingTime.UTC().Format(time.RFC3339), FundingRate: p.Rate.String(), MarkPrice: p.MarkPrice.String(),
			IndexPrice: p.IndexPrice.String(), Premium: p.Premium.String(), InterestRate: p.InterestRate.String(),
			Samples: p.Samples, SettledAt: p.SettledAt.UTC().Format(time.RFC3339Nano),
		})
	}
	live(w)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"symbol": s, "funding_rates": out})
}
