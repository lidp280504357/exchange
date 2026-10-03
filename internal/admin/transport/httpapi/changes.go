package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
)

// The changes of trading parameters (design 2026-10-02 §2 item 6): a
// status change or a document moving trading parameters answers 202 with
// the change that waits; a halt, or a document without them, 200.

// InstrumentChangeJSON is a change of trading parameters (its document
// left out: the summary says what it moves).
type InstrumentChangeJSON struct {
	ID               string          `json:"id"`
	Kind             string          `json:"kind"`
	Target           string          `json:"target"`
	Status           string          `json:"status"`
	Reason           string          `json:"reason"`
	Summary          json.RawMessage `json:"summary"`
	RequestedBy      string          `json:"requested_by"`
	RequestedByEmail string          `json:"requested_by_email"`
	ApprovedByEmail  *string         `json:"approved_by_email"`
	ApprovedAt       *string         `json:"approved_at"`
	ClosedByEmail    *string         `json:"closed_by_email"`
	ClosedAt         *string         `json:"closed_at"`
	EffectiveAt      *string         `json:"effective_at"`
	AppliedAt        *string         `json:"applied_at"`
	// ApplyingAt: an apply round has it, so it is not canceled (C5.5 ⑩).
	ApplyingAt *string `json:"applying_at"`
	Result     string  `json:"result"`
	CreatedAt  string  `json:"created_at"`
}

func instrumentChangeJSON(c domain.InstrumentChange) InstrumentChangeJSON {
	return InstrumentChangeJSON{
		ID: c.ID, Kind: c.Kind, Target: c.Target, Status: c.Status, Reason: c.Reason, Summary: c.Summary, RequestedBy: c.RequestedBy,
		RequestedByEmail: c.RequestedByEmail, ApprovedByEmail: optText(c.ApprovedByEmail), ApprovedAt: optTime(c.ApprovedAt),
		ClosedByEmail: optText(c.ClosedByEmail), ClosedAt: optTime(c.ClosedAt), EffectiveAt: optTime(c.EffectiveAt),
		AppliedAt: optTime(c.AppliedAt), ApplyingAt: optTime(c.ApplyingAt), Result: c.Result, CreatedAt: httpx.FormatTime(c.CreatedAt),
	}
}

// applyConfig applies a config document, or records the change of trading
// parameters it is.
func (h *Handler) applyConfig(w http.ResponseWriter, r *http.Request) {
	var body configBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	res, c, err := h.Svc.ApplyConfig(r.Context(), principal(r), body.Config, body.Reason, body.Confirmation)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := struct {
		ports.ConfigResult
		Change *InstrumentChangeJSON `json:"change"`
	}{ConfigResult: res}
	status := http.StatusOK
	if c != nil {
		v := instrumentChangeJSON(*c)
		out.Change, status = &v, http.StatusAccepted
	}
	httpx.WriteJSON(w, status, out)
}

type statusBody struct {
	To           string `json:"to"`
	Reason       string `json:"reason"`
	Confirmation string `json:"confirmation"`
}

func (h *Handler) previewStatus(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body statusBody
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		prev, err := h.Svc.PreviewStatus(r.Context(), principal(r), kind, chi.URLParam(r, "symbol"), body.To)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, prev)
	}
}

func (h *Handler) setStatus(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body statusBody
		if err := httpx.DecodeJSON(w, r, &body); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		set := h.Svc.SetPairStatus
		if kind == domain.ChangeContractStatus {
			set = h.Svc.SetContractStatus
		}
		res, err := set(r.Context(), principal(r), chi.URLParam(r, "symbol"), body.To, body.Reason, body.Confirmation)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		out := struct {
			From   string                `json:"from"`
			To     string                `json:"to"`
			Change *InstrumentChangeJSON `json:"change"`
		}{From: res.From, To: res.To}
		status := http.StatusOK
		if res.Change != nil {
			v := instrumentChangeJSON(*res.Change)
			out.Change, status = &v, http.StatusAccepted
		}
		httpx.WriteJSON(w, status, out)
	}
}

func (h *Handler) instrumentChanges(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, next, err := h.Svc.InstrumentChanges(r.Context(), principal(r), q.Get("status"), q.Get("cursor"), limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]InstrumentChangeJSON, 0, len(list))
	for _, c := range list {
		items = append(items, instrumentChangeJSON(c))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": optText(next)})
}

func (h *Handler) decideInstrumentChange(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Approve bool   `json:"approve"`
		Reason  string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	c, err := h.Svc.DecideInstrumentChange(r.Context(), principal(r), chi.URLParam(r, "id"), body.Approve, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, instrumentChangeJSON(c))
}

func (h *Handler) cancelInstrumentChange(w http.ResponseWriter, r *http.Request) {
	var body reasonBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	c, err := h.Svc.CancelInstrumentChange(r.Context(), principal(r), chi.URLParam(r, "id"), body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, instrumentChangeJSON(c))
}
