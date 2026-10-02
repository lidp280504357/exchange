// Package httpapi is the simulated market's internal management API (ASTRA
// design §5.1), on the internal network only: the gateway does not route
// it; the admin console's service and the ops scripts call it. The
// changes (PUT, POST) must be signed with the shared secret
// SIM_API_SECRET (internal/platform/svcsign): the caller vouches for the
// actor and the approver it names (design §6.2).
//
//	GET  /internal/sim                   the state: prices, settings, bots, open events
//	PUT  /internal/sim/params            new settings {"params": {...}, "actor": "...", "approved_by": "..."}
//	POST /internal/sim/bots              a bot {"user_id", "role", "label"}
//	GET  /internal/sim/events            the open events (?all=1: the latest, &limit=)
//	POST /internal/sim/events            a price event (design §6.2)
//	POST /internal/sim/events/{id}/end   ends an event early {"actor", "reason"}
//	GET  /internal/sim/history           the target and last price every 10 s (?minutes=, a day at most)
//	GET  /internal/sim/stream            the same, every second, as server-sent events
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketsim/application"
	"github.com/lidp280504357/exchange/internal/marketsim/domain"
	"github.com/lidp280504357/exchange/internal/marketsim/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
	"github.com/lidp280504357/exchange/internal/platform/svcsign"
)

// Handler serves the management API; Signed checks the changes'
// signatures.
type Handler struct {
	Sim    *application.Sim
	Signed *svcsign.Verifier
}

// Routes mounts the API.
func (h *Handler) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.Signed.Changes)
		r.Get("/internal/sim", h.status)
		r.Put("/internal/sim/params", h.params)
		r.Post("/internal/sim/bots", h.addBot)
		r.Get("/internal/sim/events", h.events)
		r.Post("/internal/sim/events", h.createEvent)
		r.Post("/internal/sim/events/{id}/end", h.endEvent)
		r.Get("/internal/sim/history", h.history)
		r.Get("/internal/sim/stream", h.stream)
	})
}

// StatusJSON is the simulation's state; prices are decimal strings, the
// settings are the model's numbers.
type StatusJSON struct {
	Symbol          string         `json:"symbol"`
	Enabled         bool           `json:"enabled"`
	Running         bool           `json:"running"`
	TargetPrice     *string        `json:"target_price"`
	LastPrice       *string        `json:"last_price"`
	ReferencesFresh bool           `json:"references_fresh"`
	Params          domain.Params  `json:"params"`
	Version         int64          `json:"version"`
	Guards          map[string]int `json:"guards"`
	Bots            []BotJSON      `json:"bots"`
	Events          []EventJSON    `json:"events"`
	Perp            string         `json:"perp"`
	PerpRunning     bool           `json:"perp_running"`
	// The price band (ASTRA design §4): its anchor as market-sim reads it,
	// the band, where the makers quote, whether the quotes walk toward a
	// target beyond the band, how far the target is from the anchor in
	// bands (within ±1: inside), the pair's last trade, the watchdog.
	AnchorPrice  *string      `json:"anchor_price"`
	PriceBand    string       `json:"price_band"`
	QuoteCenter  *string      `json:"quote_center"`
	Walking      bool         `json:"walking"`
	BandDistance *float64     `json:"band_distance"`
	LastTradeAt  *string      `json:"last_trade_at"`
	Watchdog     WatchdogJSON `json:"watchdog"`
	At           *string      `json:"at"`
}

// WatchdogJSON is how often the watchdog found the market locked and
// rebased the model, and when last.
type WatchdogJSON struct {
	Fired  int     `json:"fired"`
	LastAt *string `json:"last_at"`
}

// EventJSON is a price event; prices are decimal strings.
type EventJSON struct {
	ID         string  `json:"id"`
	Type       string  `json:"type"`
	Size       float64 `json:"size"`
	Price      *string `json:"price"`
	Mu         float64 `json:"mu"`
	Factor     float64 `json:"factor"`
	DurationS  int     `json:"duration_seconds"`
	HoldS      int     `json:"hold_seconds"`
	StartsAt   string  `json:"starts_at"`
	Status     string  `json:"status"`
	CreatedBy  string  `json:"created_by"`
	ApprovedBy string  `json:"approved_by"`
	Reason     string  `json:"reason"`
	CreatedAt  string  `json:"created_at"`
	StartedAt  *string `json:"started_at"`
	EndedAt    *string `json:"ended_at"`
	FromPrice  *string `json:"from_price"`
	EndedBy    string  `json:"ended_by"`
}

func eventJSON(e domain.Event) EventJSON {
	j := EventJSON{
		ID: e.ID, Type: string(e.Type), Size: e.Size, Mu: e.Mu, Factor: e.Factor, DurationS: int(e.Duration.Seconds()),
		HoldS: int(e.Hold.Seconds()), StartsAt: httpx.FormatTime(e.StartsAt), Status: e.Status, CreatedBy: e.CreatedBy,
		ApprovedBy: e.ApprovedBy, Reason: e.Reason, CreatedAt: httpx.FormatTime(e.CreatedAt), StartedAt: timeOrNil(e.StartedAt),
		EndedAt: timeOrNil(e.EndedAt), EndedBy: e.EndedBy,
	}
	if e.Price.IsPositive() {
		v := e.Price.String()
		j.Price = &v
	}
	if e.FromP.IsPositive() {
		v := e.FromP.Round(8).String()
		j.FromPrice = &v
	}
	return j
}

func timeOrNil(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	v := httpx.FormatTime(t)
	return &v
}

// BotJSON is one bot.
type BotJSON struct {
	UserID        string  `json:"user_id"`
	Role          string  `json:"role"`
	Label         string  `json:"label"`
	Enabled       bool    `json:"enabled"`
	BalancesKnown bool    `json:"balances_known"`
	USDT          string  `json:"usdt"`
	Coin          string  `json:"coin"`
	Position      string  `json:"perp_position"`
	Futures       string  `json:"futures_usdt"`
	Error         string  `json:"error"`
	ErrorAt       *string `json:"error_at"`
	// RetryAt is when the bot places orders again after a refusal; null
	// while it does.
	RetryAt *string `json:"retry_at"`
}

func (h *Handler) status(w http.ResponseWriter, _ *http.Request) {
	st := h.Sim.Status()
	out := StatusJSON{
		Symbol: st.Symbol, Enabled: st.Enabled, Running: st.Running, ReferencesFresh: st.RefsFresh, Params: st.Params, Version: st.Version,
		Guards: map[string]int{}, Bots: []BotJSON{}, Perp: st.Perp, PerpRunning: st.PerpOn,
	}
	if st.Target > 0 {
		v := decimal.NewFromFloat(st.Target).Round(8).String()
		out.TargetPrice = &v
	}
	if st.Last.IsPositive() {
		v := st.Last.String()
		out.LastPrice = &v
	}
	if !st.At.IsZero() {
		v := httpx.FormatTime(st.At)
		out.At = &v
	}
	for g, n := range st.Guards {
		out.Guards[string(g)] = n
	}
	out.PriceBand = decimal.NewFromFloat(st.Band).String()
	if st.Anchor > 0 {
		v := decimal.NewFromFloat(st.Anchor).Round(8).String()
		out.AnchorPrice = &v
		if st.Band > 0 && st.Target > 0 {
			dist := math.Round((st.Target/st.Anchor-1)/st.Band*100) / 100
			out.BandDistance = &dist
		}
	}
	if st.Center > 0 {
		v := decimal.NewFromFloat(st.Center).Round(8).String()
		out.QuoteCenter = &v
	}
	out.Walking, out.LastTradeAt = st.Walking, timeOrNil(st.LastTradeAt)
	out.Watchdog = WatchdogJSON{Fired: st.Deadlocks, LastAt: timeOrNil(st.DeadlockAt)}
	out.Events = []EventJSON{}
	for _, e := range st.Events {
		out.Events = append(out.Events, eventJSON(e))
	}
	for _, b := range st.Bots {
		j := BotJSON{
			UserID: b.UserID, Role: string(b.Role), Label: b.Label, Enabled: b.Enabled, BalancesKnown: b.Known,
			USDT: b.USDT.String(), Coin: b.Coin.String(), Position: b.Position.String(), Futures: b.Futures.String(), Error: b.Error,
		}
		if !b.ErrorAt.IsZero() {
			v := httpx.FormatTime(b.ErrorAt)
			j.ErrorAt = &v
		}
		j.RetryAt = timeOrNil(b.RetryAt)
		out.Bots = append(out.Bots, j)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) params(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Params     domain.Params `json:"params"`
		Actor      string        `json:"actor"`
		ApprovedBy string        `json:"approved_by"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	version, err := h.Sim.UpdateParams(r.Context(), body.Params, body.Actor, body.ApprovedBy)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"version": version})
}

func (h *Handler) addBot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID string `json:"user_id"`
		Role   string `json:"role"`
		Label  string `json:"label"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := h.Sim.AddBot(r.Context(), ports.Bot{UserID: body.UserID, Role: domain.Role(body.Role), Label: body.Label}); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := h.Sim.Events(r.Context(), r.URL.Query().Get("all") == "", limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]EventJSON, 0, len(list))
	for _, e := range list {
		out = append(out, eventJSON(e))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handler) createEvent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type       string  `json:"type"`
		Size       float64 `json:"size"`
		Price      string  `json:"price"`
		Mu         float64 `json:"mu"`
		Factor     float64 `json:"factor"`
		DurationS  int     `json:"duration_seconds"`
		HoldS      int     `json:"hold_seconds"`
		StartsAt   string  `json:"starts_at"`
		Actor      string  `json:"actor"`
		ApprovedBy string  `json:"approved_by"`
		Reason     string  `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	e := domain.Event{
		Type: domain.EventType(body.Type), Size: body.Size, Mu: body.Mu, Factor: body.Factor,
		Duration: time.Duration(body.DurationS) * time.Second, Hold: time.Duration(body.HoldS) * time.Second,
		CreatedBy: body.Actor, ApprovedBy: body.ApprovedBy, Reason: body.Reason,
	}
	if body.Price != "" {
		p, err := decimal.NewFromString(body.Price)
		if err != nil {
			httpx.WriteError(w, r, apperr.Invalid("price is a decimal"))
			return
		}
		e.Price = p
	}
	if body.StartsAt != "" {
		t, err := time.Parse(time.RFC3339, body.StartsAt)
		if err != nil {
			httpx.WriteError(w, r, apperr.Invalid("starts_at is an RFC 3339 time"))
			return
		}
		e.StartsAt = t
	}
	out, err := h.Sim.CreateEvent(r.Context(), e)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, eventJSON(out))
}

func (h *Handler) endEvent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Actor  string `json:"actor"`
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out, err := h.Sim.EndEvent(r.Context(), chi.URLParam(r, "id"), body.Actor, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, eventJSON(out))
}

// SampleJSON is the target and the last price at a time.
type SampleJSON struct {
	At          string  `json:"at"`
	TargetPrice string  `json:"target_price"`
	LastPrice   *string `json:"last_price"`
}

func sampleJSON(s application.Sample) SampleJSON {
	j := SampleJSON{At: httpx.FormatTime(s.At), TargetPrice: decimal.NewFromFloat(s.Target).Round(8).String()}
	if s.Last.IsPositive() {
		v := s.Last.String()
		j.LastPrice = &v
	}
	return j
}

func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
	minutes, _ := strconv.Atoi(r.URL.Query().Get("minutes"))
	if minutes <= 0 || minutes > 24*60 {
		minutes = 24 * 60
	}
	list := h.Sim.History(time.Now().Add(-time.Duration(minutes) * time.Minute))
	out := make([]SampleJSON, 0, len(list))
	for _, s := range list {
		out = append(out, sampleJSON(s))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

// stream sends the target and the last price every second until the
// client leaves.
func (h *Handler) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.WriteError(w, r, apperr.Internal(errors.New("streaming is not supported")))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		st := h.Sim.Status()
		raw, _ := json.Marshal(sampleJSON(application.Sample{At: time.Now(), Target: st.Target, Last: st.Last}))
		if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
			return
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}
}
