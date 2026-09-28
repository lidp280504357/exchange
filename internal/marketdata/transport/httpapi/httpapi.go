// Package httpapi serves the public market data endpoints
// (api/openapi/market.yaml): tickers, depth, trades and candles.
package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	"github.com/lidp280504357/exchange/internal/marketdata/application"
	"github.com/lidp280504357/exchange/internal/marketdata/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
)

// Handler serves the market data; no sign-in needed.
type Handler struct {
	Svc *application.Service
	Now func() time.Time
}

// Routes mounts the endpoints on r.
func (h *Handler) Routes(r chi.Router) {
	r.Get("/v1/market/tickers", h.tickers)
	r.Get("/v1/market/{symbol}/ticker", h.ticker)
	r.Get("/v1/market/{symbol}/depth", h.depth)
	r.Get("/v1/market/{symbol}/trades", h.trades)
	r.Get("/v1/market/{symbol}/candles", h.candles)
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
