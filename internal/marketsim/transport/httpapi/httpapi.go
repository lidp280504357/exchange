// Package httpapi is the simulated market's internal management API (ASTRA
// design §5.1), on the internal network only: the gateway does not route
// it; the admin console's service and the ops scripts call it.
//
//	GET  /internal/sim          the state: target and last price, settings, bots
//	PUT  /internal/sim/params   new settings {"params": {...}, "actor": "..."}
//	POST /internal/sim/bots     a bot {"user_id", "role", "label"}
package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketsim/application"
	"github.com/lidp280504357/exchange/internal/marketsim/domain"
	"github.com/lidp280504357/exchange/internal/marketsim/ports"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
)

// Handler serves the management API.
type Handler struct {
	Sim *application.Sim
}

// Routes mounts the API.
func (h *Handler) Routes(r chi.Router) {
	r.Get("/internal/sim", h.status)
	r.Put("/internal/sim/params", h.params)
	r.Post("/internal/sim/bots", h.addBot)
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
	At              *string        `json:"at"`
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
	Error         string  `json:"error"`
	ErrorAt       *string `json:"error_at"`
}

func (h *Handler) status(w http.ResponseWriter, _ *http.Request) {
	st := h.Sim.Status()
	out := StatusJSON{
		Symbol: st.Symbol, Enabled: st.Enabled, Running: st.Running, ReferencesFresh: st.RefsFresh, Params: st.Params, Version: st.Version,
		Guards: map[string]int{}, Bots: []BotJSON{},
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
	for _, b := range st.Bots {
		j := BotJSON{
			UserID: b.UserID, Role: string(b.Role), Label: b.Label, Enabled: b.Enabled, BalancesKnown: b.Known,
			USDT: b.USDT.String(), Coin: b.Coin.String(), Error: b.Error,
		}
		if !b.ErrorAt.IsZero() {
			v := httpx.FormatTime(b.ErrorAt)
			j.ErrorAt = &v
		}
		out.Bots = append(out.Bots, j)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) params(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Params domain.Params `json:"params"`
		Actor  string        `json:"actor"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	version, err := h.Sim.UpdateParams(r.Context(), body.Params, body.Actor)
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
