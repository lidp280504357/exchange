package httpapi

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketdata/application"
	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/marketdata/ports"
	"github.com/skill/exchange/internal/platform/httpx"
)

// FuturesData serves the contracts' data panel (design 2026-10-06 §3.3):
// a contract's statistics from the reference market, and every
// contract's prices, funding rate, open interest and 24 hours at a
// glance. No sign-in needed.
type FuturesData struct {
	// Stats is the reference market's statistics and open interest
	// (application.FuturesStats).
	Stats interface {
		Series(ctx context.Context, symbol, metric, period string, limit int) ([]ports.FuturesStat, error)
		Market(symbol string) (m ports.FuturesMarket, known, followed bool)
		OpenInterestNow(symbol string) (application.OpenInterest, bool)
	}
	Marks interface {
		Latest(symbol string) (application.MarkPrice, bool)
	}
	// Tickers gives the 24-hour change and turnover.
	Tickers interface {
		All(ctx context.Context) ([]domain.Ticker, error)
	}
	Listed ports.Instruments
}

// Routes mounts the endpoints on r; the static overview wins over
// {symbol}.
func (h *FuturesData) Routes(r chi.Router) {
	r.Get("/v1/market/futures/overview", h.overview)
	r.Get("/v1/market/{symbol}/futures-data", h.series)
}

// contract returns a listed contract (not delisted), or false.
func (h *FuturesData) contract(ctx context.Context, symbol string) (bool, error) {
	list, err := h.Listed.Contracts(ctx)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(list, func(c ports.Contract) bool { return c.Symbol == symbol }), nil
}

type statPointJSON struct {
	Time   string            `json:"time"`
	Values map[string]string `json:"values"`
}

// series answers GET /v1/market/{symbol}/futures-data?metric=&period=&limit=:
// the latest limit points (30 by default, 500 at most) of a statistic,
// oldest first; a funding rate takes no period.
func (h *FuturesData) series(w http.ResponseWriter, r *http.Request) {
	s := symbol(r)
	ok, err := h.contract(r.Context(), s)
	switch {
	case err != nil:
		httpx.WriteError(w, r, err)
		return
	case !ok:
		httpx.WriteError(w, r, ErrUnknownContract)
		return
	}
	q := r.URL.Query()
	metric, period := q.Get("metric"), q.Get("period")
	if period == "" {
		period = "5m"
	}
	n := limit(r)
	if n <= 0 {
		n = 30
	}
	points, err := h.Stats.Series(r.Context(), s, metric, period, n)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]statPointJSON, 0, len(points))
	for _, p := range points {
		values := make(map[string]string, len(p.Values))
		for k, v := range p.Values {
			values[k] = v.String()
		}
		out = append(out, statPointJSON{Time: p.At.UTC().Format(time.RFC3339Nano), Values: values})
	}
	var shownPeriod *string
	if metric != ports.MetricFunding {
		shownPeriod = &period
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"symbol": s, "metric": metric, "period": shownPeriod, "points": out})
}

// overviewJSON is one contract at a glance: null where not known (a
// contract the reference market does not trade has no open interest).
type overviewJSON struct {
	Symbol          string  `json:"symbol"`
	MarkPrice       *string `json:"mark_price"`
	IndexPrice      *string `json:"index_price"`
	FundingRate     *string `json:"funding_rate"`
	NextFundingTime *string `json:"next_funding_time"`
	// OpenInterest is in the base asset (USDⓈ-M) or in contracts
	// (COIN-M); OpenInterestValue in USD either way.
	OpenInterest      *string `json:"open_interest"`
	OpenInterestValue *string `json:"open_interest_value"`
	Change            *string `json:"change"`
	QuoteVolume       *string `json:"quote_volume"`
	// FuturesData tells whether the data panel has the reference market's
	// statistics of the contract.
	FuturesData bool `json:"futures_data"`
}

// overview answers GET /v1/market/futures/overview: every contract that
// is not delisted, by symbol.
func (h *FuturesData) overview(w http.ResponseWriter, r *http.Request) {
	contracts, err := h.Listed.Contracts(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	tickers := map[string]domain.Ticker{}
	if list, err := h.Tickers.All(r.Context()); err == nil {
		for _, t := range list {
			tickers[t.Symbol] = t
		}
	}
	out := make([]overviewJSON, 0, len(contracts))
	for _, c := range contracts {
		o := overviewJSON{Symbol: c.Symbol}
		var mark decimal.Decimal
		if p, ok := h.Marks.Latest(c.Symbol); ok {
			mark = p.Mark
			o.MarkPrice, o.IndexPrice = price(p.Mark), price(p.Index)
			if !p.NextFunding.IsZero() {
				rate := p.FundingRate.String()
				o.FundingRate, o.NextFundingTime = &rate, stamp(p.NextFunding)
			}
		}
		if t, ok := tickers[c.Symbol]; ok {
			if t.Open.IsPositive() {
				change := t.Change.String()
				o.Change = &change
			}
			volume := t.QuoteVolume.String()
			o.QuoteVolume = &volume
		}
		_, _, o.FuturesData = h.Stats.Market(c.Symbol)
		if oi, ok := h.Stats.OpenInterestNow(c.Symbol); ok {
			qty := oi.Quantity.String()
			o.OpenInterest = &qty
			switch {
			case oi.Market.CoinMargined:
				o.OpenInterestValue = price(oi.Quantity.Mul(oi.Market.ContractSize))
			case mark.IsPositive():
				o.OpenInterestValue = price(oi.Quantity.Mul(mark).Round(2))
			}
		}
		out = append(out, o)
	}
	slices.SortFunc(out, func(a, b overviewJSON) int { return strings.Compare(a.Symbol, b.Symbol) })
	w.Header().Set("Cache-Control", "public, max-age=5")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"contracts": out})
}
