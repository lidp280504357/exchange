// Package httpapi is the simulated market's internal management API (ASTRA
// design §5.1), on the internal network only: the gateway does not route
// it; the admin console's service and the ops scripts call it. The
// changes (PUT, POST) must be signed (internal/platform/svcsign) with one
// of two keys: KeyOps (SIM_API_SECRET, exchangectl in market-sim's
// container) or KeyAdmin (SIM_ADMIN_API_SECRET, the admin console's
// service). The signer vouches for the actor it names, and only KeyAdmin
// may name an approver: the console signs both operators in (design
// §6.2).
//
//	GET  /internal/sim                   the state: prices, settings, bots, open events
//	PUT  /internal/sim/params            new settings {"params": {...}, "actor": "...", "approved_by": "..."}
//	POST /internal/sim/bots              a bot {"user_id", "role", "label"}
//	GET  /internal/sim/events            the open events (?all=1: the latest, &limit=)
//	POST /internal/sim/events            a price event (design §6.2; a TARGET with its "spikes")
//	POST /internal/sim/events/{id}/end   ends an event early {"actor", "reason"}
//	GET  /internal/sim/events/{id}/plan  a threshold target's plan and where the price is against it
//	GET  /internal/sim/target-preview    a threshold target's plan before it is made (?direction=&price=&duration_seconds=&starts_at=)
//	GET  /internal/sim/history           the target and last price every 10 s (?minutes=, a day at most)
//	GET  /internal/sim/stream            the same, every second, as server-sent events, with the running target's plan
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
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

// The keys the changes are signed with: exchangectl's in market-sim's
// container, and the admin console's service's — the only one that may
// name an approver, from the two operators it signed in (ASTRA design
// §6.2).
const (
	KeyOps   = "ops"
	KeyAdmin = "admin"
)

// ErrApprovalNeedsAdmin refuses an approver named by any caller but the
// admin console's service.
var ErrApprovalNeedsAdmin = apperr.New(apperr.KindForbidden, "SIM_APPROVAL_NEEDS_ADMIN",
	"only the admin console's service names an approver: it signed both operators in")

// approver is the request's approver: allowed only signed with KeyAdmin.
func approver(r *http.Request, name string) (string, error) {
	if name != "" && svcsign.KeyID(r.Context()) != KeyAdmin {
		return "", ErrApprovalNeedsAdmin
	}
	return name, nil
}

// Handler serves the management API; Signed checks the changes'
// signatures.
type Handler struct {
	Sim    *application.Sim
	Signed *svcsign.Verifier
}

// ready answers 503 SIM_NOT_READY until the simulation is loaded (a
// standby waiting for the lease has nothing to show or change).
func (h *Handler) ready(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.Sim.Ready() {
			httpx.WriteError(w, r, application.ErrNotReady)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Routes mounts the API.
func (h *Handler) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.ready, h.Signed.Changes)
		r.Get("/internal/sim", h.status)
		r.Put("/internal/sim/params", h.params)
		r.Post("/internal/sim/bots", h.addBot)
		r.Get("/internal/sim/events", h.events)
		r.Post("/internal/sim/events", h.createEvent)
		r.Post("/internal/sim/events/{id}/end", h.endEvent)
		r.Get("/internal/sim/events/{id}/plan", h.plan)
		r.Get("/internal/sim/target-preview", h.preview)
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
	// A threshold target's (ASTRA design §3, §6.2): its direction (ABOVE,
	// BELOW), what follows its crossing (FOLLOW, HOLD), how it ended (HIT,
	// MISSED, CANCELED; "" before), when it crossed, when its window ends
	// and closes in, when its hold ends; a spike's width and its target.
	Direction string      `json:"direction"`
	Then      string      `json:"then"`
	Result    string      `json:"result"`
	CrossedAt *string     `json:"crossed_at"`
	EndsAt    *string     `json:"ends_at"`
	ClosingAt *string     `json:"closing_at"`
	HoldUntil *string     `json:"hold_until"`
	ParentID  *string     `json:"parent_id"`
	WidthS    int         `json:"width_seconds"`
	Spikes    []EventJSON `json:"spikes,omitempty"`
}

func eventJSON(e domain.Event) EventJSON {
	j := EventJSON{
		ID: e.ID, Type: string(e.Type), Size: e.Size, Mu: e.Mu, Factor: e.Factor, DurationS: int(e.Duration.Seconds()),
		HoldS: int(e.Hold.Seconds()), StartsAt: httpx.FormatTime(e.StartsAt), Status: e.Status, CreatedBy: e.CreatedBy,
		ApprovedBy: e.ApprovedBy, Reason: e.Reason, CreatedAt: httpx.FormatTime(e.CreatedAt), StartedAt: timeOrNil(e.StartedAt),
		EndedAt: timeOrNil(e.EndedAt), EndedBy: e.EndedBy, Direction: e.Direction, Then: e.Then, Result: e.Result,
		CrossedAt: timeOrNil(e.CrossedAt), WidthS: int(e.Width.Seconds()),
	}
	if e.Type == domain.EventTarget {
		j.EndsAt, j.ClosingAt, j.HoldUntil = timeOrNil(e.EndsAt()), timeOrNil(e.ClosingAt()), timeOrNil(e.HoldUntil())
	}
	if e.ParentID != "" {
		j.ParentID = &e.ParentID
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
	approvedBy, err := approver(r, body.ApprovedBy)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	version, err := h.Sim.UpdateParams(r.Context(), body.Params, body.Actor, approvedBy)
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
		Type      string  `json:"type"`
		Size      float64 `json:"size"`
		Price     string  `json:"price"`
		Mu        float64 `json:"mu"`
		Factor    float64 `json:"factor"`
		DurationS int     `json:"duration_seconds"`
		HoldS     int     `json:"hold_seconds"`
		StartsAt  string  `json:"starts_at"`
		Direction string  `json:"direction"`
		Then      string  `json:"then"`
		WidthS    int     `json:"width_seconds"`
		Spikes    []struct {
			At     string  `json:"at"`
			Size   float64 `json:"size"`
			WidthS int     `json:"width_seconds"`
		} `json:"spikes"`
		Actor      string `json:"actor"`
		ApprovedBy string `json:"approved_by"`
		Reason     string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if _, err := approver(r, body.ApprovedBy); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	e := domain.Event{
		Type: domain.EventType(body.Type), Size: body.Size, Mu: body.Mu, Factor: body.Factor,
		Duration: time.Duration(body.DurationS) * time.Second, Hold: time.Duration(body.HoldS) * time.Second,
		Direction: strings.ToUpper(body.Direction), Then: strings.ToUpper(body.Then), Width: time.Duration(body.WidthS) * time.Second,
		CreatedBy: body.Actor, ApprovedBy: body.ApprovedBy, Reason: body.Reason,
	}
	spikes := make([]domain.Event, 0, len(body.Spikes))
	for _, x := range body.Spikes {
		at, err := time.Parse(time.RFC3339, x.At)
		if err != nil {
			httpx.WriteError(w, r, apperr.Invalid("a spike's at is an RFC 3339 time"))
			return
		}
		spikes = append(spikes, domain.Event{StartsAt: at, Size: x.Size, Width: time.Duration(x.WidthS) * time.Second})
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
	out, err := h.Sim.CreateEvent(r.Context(), e, spikes...)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	j := eventJSON(out)
	for _, x := range h.Sim.SpikesOf(out.ID) {
		j.Spikes = append(j.Spikes, eventJSON(x))
	}
	httpx.WriteJSON(w, http.StatusCreated, j)
}

// PlanPointJSON is a target's planned price at a time and the band its
// path keeps to.
type PlanPointJSON struct {
	At   string `json:"at"`
	Plan string `json:"plan"`
	Low  string `json:"low"`
	High string `json:"high"`
}

func price(v float64) string { return decimal.NewFromFloat(v).Round(8).String() }

func pointsJSON(points []domain.PlanPoint) []PlanPointJSON {
	out := make([]PlanPointJSON, 0, len(points))
	for _, p := range points {
		out = append(out, PlanPointJSON{At: httpx.FormatTime(p.At), Plan: price(p.Plan), Low: price(p.Low), High: price(p.High)})
	}
	return out
}

// TargetNowJSON is where the target is against a running target's plan.
type TargetNowJSON struct {
	At        string  `json:"at"`
	Target    string  `json:"target"`
	Plan      string  `json:"plan"`
	Low       string  `json:"low"`
	High      string  `json:"high"`
	Deviation float64 `json:"deviation"`
	AtRisk    bool    `json:"at_risk"`
	CrossedAt *string `json:"crossed_at"`
	Result    string  `json:"result"`
}

// PlanJSON is a threshold target's plan (GET /internal/sim/events/{id}/plan).
type PlanJSON struct {
	EventID   string          `json:"event_id"`
	Direction string          `json:"direction"`
	Level     string          `json:"level"`
	FromPrice string          `json:"from_price"`
	StartsAt  string          `json:"starts_at"`
	ClosingAt string          `json:"closing_at"`
	EndsAt    string          `json:"ends_at"`
	HoldUntil *string         `json:"hold_until"`
	Status    string          `json:"status"`
	Points    []PlanPointJSON `json:"points"`
	Spikes    []EventJSON     `json:"spikes"`
	Now       *TargetNowJSON  `json:"now"`
}

func planJSON(v application.TargetView) PlanJSON {
	e := v.Event
	out := PlanJSON{
		EventID: e.ID, Direction: e.Direction, Level: e.Price.String(), FromPrice: price(v.From), StartsAt: httpx.FormatTime(e.Start()),
		ClosingAt: httpx.FormatTime(e.ClosingAt()), EndsAt: httpx.FormatTime(e.EndsAt()), HoldUntil: timeOrNil(e.HoldUntil()),
		Status: e.Status, Points: pointsJSON(v.Points), Spikes: []EventJSON{},
	}
	for _, x := range v.Spikes {
		out.Spikes = append(out.Spikes, eventJSON(x))
	}
	if e.Status == domain.EventRunning && v.Now.Plan > 0 {
		out.Now = &TargetNowJSON{
			At: httpx.FormatTime(v.Now.At), Target: price(v.Target), Plan: price(v.Now.Plan), Low: price(v.Now.Low), High: price(v.Now.High),
			Deviation: math.Round(v.Deviation*1e6) / 1e6, AtRisk: v.AtRisk, CrossedAt: timeOrNil(e.CrossedAt), Result: e.Result,
		}
	}
	return out
}

func (h *Handler) plan(w http.ResponseWriter, r *http.Request) {
	v, err := h.Sim.TargetPlan(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, planJSON(v))
}

func (h *Handler) preview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	level, err := decimal.NewFromString(q.Get("price"))
	if err != nil {
		httpx.WriteError(w, r, apperr.Invalid("price is a decimal"))
		return
	}
	seconds, _ := strconv.Atoi(q.Get("duration_seconds"))
	var starts time.Time
	if v := q.Get("starts_at"); v != "" {
		if starts, err = time.Parse(time.RFC3339, v); err != nil {
			httpx.WriteError(w, r, apperr.Invalid("starts_at is an RFC 3339 time"))
			return
		}
	}
	p, err := h.Sim.PreviewTarget(r.Context(), strings.ToUpper(q.Get("direction")), level, time.Duration(seconds)*time.Second, starts)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"direction": p.Event.Direction, "feasible": p.Feasible, "min_duration_seconds": int(p.MinDuration.Seconds()),
		"move": math.Round(p.Move*1e4) / 1e4, "needs_approval": p.NeedsApproval, "points": pointsJSON(p.Points),
	})
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

// StreamJSON is a message of the stream: the target and the last price,
// and the running threshold target against its plan (null when none).
type StreamJSON struct {
	SampleJSON
	Target *StreamTargetJSON `json:"target"`
}

// StreamTargetJSON is the running threshold target on the stream.
type StreamTargetJSON struct {
	EventID   string  `json:"event_id"`
	Plan      string  `json:"plan"`
	Low       string  `json:"low"`
	High      string  `json:"high"`
	Deviation float64 `json:"deviation"`
	AtRisk    bool    `json:"at_risk"`
	Result    string  `json:"result"`
	CrossedAt *string `json:"crossed_at"`
	EndsAt    string  `json:"ends_at"`
}

// stream sends the target and the last price every second until the
// client leaves, with the running threshold target's plan.
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
		msg := StreamJSON{SampleJSON: sampleJSON(application.Sample{At: time.Now(), Target: st.Target, Last: st.Last})}
		if v, ok := h.Sim.RunningTarget(); ok && v.Now.Plan > 0 {
			msg.Target = &StreamTargetJSON{
				EventID: v.Event.ID, Plan: price(v.Now.Plan), Low: price(v.Now.Low), High: price(v.Now.High),
				Deviation: math.Round(v.Deviation*1e6) / 1e6, AtRisk: v.AtRisk, Result: v.Event.Result,
				CrossedAt: timeOrNil(v.Event.CrossedAt), EndsAt: httpx.FormatTime(v.Event.EndsAt()),
			}
		}
		raw, _ := json.Marshal(msg)
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
