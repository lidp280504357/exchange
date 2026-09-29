// Package httpapi serves the public market data endpoints
// (api/openapi/market.yaml): tickers, depth, trades and candles, and the
// contracts' mark prices and funding rates.
package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	"github.com/lidp280504357/exchange/internal/marketdata/application"
	"github.com/lidp280504357/exchange/internal/marketdata/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
)

// Handler serves the market data; no sign-in needed.
type Handler struct {
	Svc *application.Service
	// Ref is the reference feed; nil when none is configured.
	Ref   *application.ReferenceFeed
	Marks *application.Marks
	Now   func() time.Time
}

// Routes mounts the endpoints on r.
func (h *Handler) Routes(r chi.Router) {
	r.Get("/v1/market/tickers", h.tickers)
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
	r.Get("/internal/market/{symbol}/mark", h.markInternal)
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
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, out)
}

// live lets clients and Cloudflare reuse an answer for a second at most.
func live(w http.ResponseWriter) { w.Header().Set("Cache-Control", "public, max-age=1") }

func symbol(r *http.Request) string { return strings.ToUpper(chi.URLParam(r, "symbol")) }

func limit(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return n
}

// tickerJSON: prices are null while unknown (a pair that never traded).
type tickerJSON struct {
	Symbol      string  `json:"symbol"`
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
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (h *Handler) tickerJSON(t domain.Ticker) tickerJSON {
	p := application.TickerProto(t, h.Now())
	return tickerJSON{
		Symbol: p.GetSymbol(), Last: optional(p.GetLast()), Open: optional(p.GetOpen()), High: optional(p.GetHigh()),
		Low: optional(p.GetLow()), Volume: p.GetVolume(), QuoteVolume: p.GetQuoteVolume(), TradeCount: p.GetTradeCount(),
		Change: optional(p.GetChange()), Bid: optional(p.GetBid()), Ask: optional(p.GetAsk()),
		UpdatedAt: p.GetUpdatedAt().AsTime().UTC().Format(time.RFC3339Nano),
	}
}

func (h *Handler) tickers(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.Tickers(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]tickerJSON, 0, len(list))
	for _, t := range list {
		out = append(out, h.tickerJSON(t))
	}
	live(w)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tickers": out})
}

func (h *Handler) ticker(w http.ResponseWriter, r *http.Request) {
	t, err := h.Svc.Ticker(r.Context(), symbol(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	live(w)
	httpx.WriteJSON(w, http.StatusOK, h.tickerJSON(t))
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
	d, err := h.Svc.Depth(r.Context(), symbol(r), limit(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
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
	list, err := h.Svc.Trades(r.Context(), s, limit(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
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
	list, err := h.Svc.Candles(r.Context(), s, interval, from, to, limit(r))
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
