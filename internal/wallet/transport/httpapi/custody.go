package httpapi

import (
	"io"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
	"github.com/skill/exchange/internal/platform/udun"
	"github.com/skill/exchange/internal/wallet/domain"
	"github.com/skill/exchange/internal/wallet/ports"
)

// callback takes a custodian's callback (ADR-0011): recorded and applied,
// it is answered with the reply the custodian expects; anything else
// makes the custodian try again. The provider is named in lower case
// only, the one path the edge proxy guards. With addresses in
// CallbackFrom for the provider only those are taken; without, any (the
// signature, its age and the trade's status decide), and the address is
// kept with the callback.
func (h *Handler) callback(w http.ResponseWriter, r *http.Request) {
	p := chi.URLParam(r, "provider")
	if p != strings.ToLower(p) {
		httpx.WriteError(w, r, apperr.NotFound("no such callback"))
		return
	}
	provider := strings.ToUpper(p)
	from := httpx.ClientIPFrom(r.Context())
	if allowed := h.CallbackFrom[provider]; len(allowed) > 0 {
		ip, err := netip.ParseAddr(from)
		if err != nil || !slices.ContainsFunc(allowed, func(p netip.Prefix) bool { return p.Contains(ip.Unmap()) }) {
			httpx.WriteError(w, r, apperr.Forbidden("callbacks come from the custodian's addresses only"))
			return
		}
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		httpx.WriteError(w, r, apperr.Invalid("the callback could not be read"))
		return
	}
	if _, err := h.Svc.HandleCallback(r.Context(), provider, r.Header.Get("Content-Type"), from, raw); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, udun.CallbackReply)
}

// CoinJSON is one of the custodian's coins.
type CoinJSON struct {
	Coin     string              `json:"coin"`
	Symbol   string              `json:"symbol"`
	Decimals int32               `json:"decimals"`
	Balance  *string             `json:"balance"`
	Token    bool                `json:"token"`
	Networks []map[string]string `json:"networks"`
}

// CheckJSON is a chain check.
type CheckJSON struct {
	Holder    string `json:"holder"`
	Asset     string `json:"asset"`
	Held      string `json:"held"`
	Expected  string `json:"expected"`
	Elsewhere string `json:"elsewhere"`
	InFlight  string `json:"in_flight"`
	Unbooked  string `json:"unbooked"`
	Shortfall string `json:"shortfall"`
	// Baseline: simulated deposits of the custodian's stand-in taken out
	// of Expected when the real gateway replaced it, at no custodian.
	Baseline  string `json:"baseline"`
	Addresses int    `json:"addresses"`
	CheckedAt string `json:"checked_at"`
}

// adminCustody describes the custodian (provider, default UDUN) for the
// admin console.
func (h *Handler) adminCustody(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToUpper(r.URL.Query().Get("provider"))
	if provider == "" {
		provider = domain.ProviderUdun
	}
	o, err := h.Svc.Custody(r.Context(), provider)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	coins := make([]CoinJSON, 0, len(o.Coins))
	for _, c := range o.Coins {
		j := CoinJSON{Coin: c.Code, Symbol: c.Symbol, Decimals: c.Decimals, Token: c.Token, Networks: []map[string]string{}}
		if c.Balance != nil {
			b := c.Balance.String()
			j.Balance = &b
		}
		for _, n := range o.Networks[c.Code] {
			j.Networks = append(j.Networks, map[string]string{"asset": n.Asset, "network": n.Network})
		}
		coins = append(coins, j)
	}
	checks := make([]CheckJSON, 0, len(o.Checks))
	for _, c := range o.Checks {
		checks = append(checks, CheckJSON{
			Holder: c.Network, Asset: c.Asset, Held: c.Chain.String(), Expected: c.Ledger.String(), Elsewhere: c.Elsewhere.String(),
			InFlight: c.InFlight.String(), Unbooked: c.Unbooked.String(), Shortfall: c.Shortfall.String(), Baseline: c.Baseline.String(),
			Addresses: c.Addresses, CheckedAt: httpx.FormatTime(c.CheckedAt),
		})
	}
	value := decimal.Zero
	var oldest *string
	for i, wd := range o.Submitted {
		value = value.Add(wd.ValueUSDT)
		if i == 0 {
			oldest = timeOrNil(wd.SubmittedAt)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"provider": o.Provider, "configured": o.Configured, "gateway_host": o.GatewayHost, "error": optional(o.Error), "coins": coins,
		"checks":    checks,
		"submitted": map[string]any{"count": len(o.Submitted), "amount_usdt": value.String(), "oldest_at": oldest},
		"callbacks": map[string]any{"attention": o.Attention, "last_at": timeOrNil(o.LastAt)},
	})
}

// CallbackJSON is a custodian's callback as the admin console shows it.
type CallbackJSON struct {
	ID          string   `json:"id"`
	Provider    string   `json:"provider"`
	TradeID     string   `json:"trade_id"`
	Kind        string   `json:"kind"`
	Status      *int     `json:"status"`
	BusinessID  string   `json:"business_id"`
	Coin        string   `json:"coin"`
	Address     string   `json:"address"`
	Amount      *string  `json:"amount"`
	TxHash      string   `json:"tx_hash"`
	SignatureOK bool     `json:"signature_ok"`
	Result      string   `json:"result"`
	Detail      string   `json:"detail"`
	Attempts    int      `json:"attempts"`
	ReceivedAt  string   `json:"received_at"`
	ProcessedAt *string  `json:"processed_at"`
	RemoteIPs   []string `json:"remote_ips"`
	Raw         *string  `json:"raw,omitempty"`
}

func callbackJSON(c domain.Callback, raw bool) CallbackJSON {
	j := CallbackJSON{
		ID: c.ID, Provider: c.Provider, TradeID: c.TradeID, Kind: c.Kind, BusinessID: c.BusinessID, Coin: c.Coin, Address: c.Address,
		TxHash: c.TxHash, SignatureOK: c.SignatureOK, Result: c.Result, Detail: c.Detail, Attempts: c.Attempts,
		ReceivedAt: httpx.FormatTime(c.ReceivedAt), ProcessedAt: timeOrNil(c.ProcessedAt), RemoteIPs: c.RemoteIPs,
	}
	if j.RemoteIPs == nil {
		j.RemoteIPs = []string{}
	}
	if c.Status >= 0 {
		j.Status = &c.Status
	}
	if c.Amount != nil {
		a := c.Amount.String()
		j.Amount = &a
	}
	if raw {
		masked := udun.MaskSign(c.Raw)
		j.Raw = &masked
	}
	return j
}

func (h *Handler) adminCallbacks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, next, err := h.Svc.Callbacks(r.Context(), ports.CallbackFilter{
		Provider: strings.ToUpper(q.Get("provider")), Result: strings.ToUpper(q.Get("result")), Kind: strings.ToUpper(q.Get("kind")),
		Query: strings.TrimSpace(q.Get("q")), After: q.Get("cursor"), Limit: limit,
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]CallbackJSON, 0, len(list))
	for _, c := range list {
		out = append(out, callbackJSON(c, false))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out, "next_cursor": optional(next)})
}

func (h *Handler) adminCallback(w http.ResponseWriter, r *http.Request) {
	c, err := h.Svc.Callback(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, callbackJSON(c, true))
}

func (h *Handler) adminReplay(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Actor  string `json:"actor"`
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	c, err := h.Svc.ReplayCallback(r.Context(), chi.URLParam(r, "id"), body.Actor, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, callbackJSON(c, true))
}
