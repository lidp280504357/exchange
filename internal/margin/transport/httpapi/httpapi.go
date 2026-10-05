// Package httpapi serves the margin endpoints (api/openapi/margin.yaml)
// under /v1/margin; the gateway attaches the caller's identity.
package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/margin/application"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
)

// HeaderIdempotencyKey carries the client's key of a write (§7.1).
const HeaderIdempotencyKey = "Idempotency-Key"

// Handler serves margin trading.
type Handler struct {
	Svc *application.Service
}

// Routes mounts the endpoints on r: the terms are public, the rest need
// the caller's identity.
func (h *Handler) Routes(r chi.Router) {
	r.Get("/v1/margin/assets", h.assets)
	r.Get("/v1/margin/pairs", h.pairs)
	r.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if httpx.UserID(r) == "" {
					httpx.WriteError(w, r, apperr.Unauthorized("sign in first"))
					return
				}
				next.ServeHTTP(w, r)
			})
		})
		r.Get("/v1/margin/accounts", h.accounts)
		r.Post("/v1/margin/transfer", h.transfer)
		r.Post("/v1/margin/borrow", h.borrow)
		r.Post("/v1/margin/repay", h.repay)
		r.Get("/v1/margin/loans", h.loans)
		r.Get("/v1/margin/interest", h.interest)
		r.Get("/v1/margin/liquidations", h.liquidations)
		r.Get("/v1/margin/max-borrowable", h.maxBorrowable)
	})
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func decimalField(name, s string) (decimal.Decimal, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, apperr.Invalid(name + " must be a decimal string")
	}
	return d, nil
}

// accountParam reads an account from account and symbol parameters;
// required makes the account mandatory, otherwise nil means all.
func accountParam(r *http.Request, required bool) (*domain.Account, error) {
	q := r.URL.Query()
	if q.Get("account") == "" {
		if required {
			return nil, apperr.Invalid("account is required")
		}
		if q.Get("symbol") != "" {
			return nil, apperr.Invalid("symbol goes with account MARGIN_ISOLATED")
		}
		return nil, nil
	}
	a, err := domain.ParseAccount(q.Get("account"), q.Get("symbol"))
	if err != nil {
		return nil, err
	}
	return &a, nil
}

type floatingJSON struct {
	BaseRate string `json:"base_rate"`
	Kink     string `json:"kink"`
	KinkRate string `json:"kink_rate"`
	MaxRate  string `json:"max_rate"`
}

type assetJSON struct {
	Asset         string       `json:"asset"`
	Borrowable    bool         `json:"borrowable"`
	Collateral    bool         `json:"collateral"`
	Haircut       string       `json:"haircut"`
	PoolCap       string       `json:"pool_cap"`
	PoolAvailable string       `json:"pool_available"`
	Utilization   string       `json:"utilization"`
	UserCap       string       `json:"user_cap"`
	InterestModel string       `json:"interest_model"`
	HourlyRate    string       `json:"hourly_rate"`
	RateHour      string       `json:"rate_hour"`
	Floating      floatingJSON `json:"floating"`
}

func (h *Handler) assets(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.Assets(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]assetJSON, 0, len(list))
	for _, a := range list {
		f := a.Terms.Floating
		items = append(items, assetJSON{
			Asset: a.Terms.Asset, Borrowable: a.Terms.Borrowable, Collateral: a.Terms.Collateral, Haircut: a.Terms.Haircut.String(),
			PoolCap: a.Terms.PoolCap.String(), PoolAvailable: a.PoolAvailable.String(), Utilization: a.Utilization.String(),
			UserCap: a.Terms.UserCap.String(), InterestModel: string(a.Terms.Model), HourlyRate: a.Rate.String(), RateHour: stamp(a.RateHour),
			Floating: floatingJSON{BaseRate: f.Base.String(), Kink: f.Kink.String(), KinkRate: f.KinkRate.String(), MaxRate: f.MaxRate.String()},
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

type termsJSON struct {
	Leverage           int    `json:"leverage"`
	WarnLevel          string `json:"warn_level"`
	LiquidationLevel   string `json:"liquidation_level"`
	LiquidationFeeRate string `json:"liquidation_fee_rate"`
}

func toTerms(t domain.Terms) termsJSON {
	return termsJSON{
		Leverage: t.Leverage, WarnLevel: t.WarnLevel.String(), LiquidationLevel: t.LiquidationLevel.String(),
		LiquidationFeeRate: t.LiquidationFee.String(),
	}
}

type pairJSON struct {
	termsJSON
	Symbol   string `json:"symbol"`
	Base     string `json:"base"`
	Quote    string `json:"quote"`
	Isolated bool   `json:"isolated"`
}

func (h *Handler) pairs(w http.ResponseWriter, r *http.Request) {
	cross, pairs, err := h.Svc.Pairs(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]pairJSON, 0, len(pairs))
	for _, p := range pairs {
		items = append(items, pairJSON{termsJSON: toTerms(p.Terms), Symbol: p.Symbol, Base: p.Base, Quote: p.Quote, Isolated: p.Isolated})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"cross": toTerms(cross), "items": items})
}

type balanceJSON struct {
	Asset    string `json:"asset"`
	Free     string `json:"free"`
	Locked   string `json:"locked"`
	Borrowed string `json:"borrowed"`
	Interest string `json:"interest"`
	Net      string `json:"net"`
}

type accountJSON struct {
	Account          string        `json:"account"`
	Symbol           *string       `json:"symbol"`
	Leverage         int           `json:"leverage"`
	Status           string        `json:"status"`
	MarginLevel      *string       `json:"margin_level"`
	WarnLevel        string        `json:"warn_level"`
	LiquidationLevel string        `json:"liquidation_level"`
	TotalAsset       string        `json:"total_asset"`
	TotalLiability   string        `json:"total_liability"`
	NetAsset         string        `json:"net_asset"`
	LiquidationPrice *string       `json:"liquidation_price"`
	Balances         []balanceJSON `json:"balances"`
	UpdatedAt        string        `json:"updated_at"`
}

// AccountJSON renders an account view (also for the margin channel).
func AccountJSON(v application.AccountView, now time.Time) any {
	return toAccount(v, now)
}

func toAccount(v application.AccountView, now time.Time) accountJSON {
	out := accountJSON{
		Account: string(v.Account.Type), Symbol: nullable(v.Account.Symbol), Leverage: v.Terms.Leverage, Status: string(v.Status),
		WarnLevel: v.Terms.WarnLevel.String(), LiquidationLevel: v.Terms.LiquidationLevel.String(),
		TotalAsset: v.Valuation.TotalAsset.String(), TotalLiability: v.Valuation.TotalLiability.String(),
		NetAsset: v.Valuation.Net().String(), Balances: make([]balanceJSON, 0, len(v.Holdings)), UpdatedAt: stamp(now),
	}
	if out.Status == "" {
		out.Status = string(domain.StatusNormal)
	}
	if level, ok := v.Valuation.Level(); ok {
		l := level.String()
		out.MarginLevel = &l
	}
	if v.LiquidationPrice != nil {
		p := v.LiquidationPrice.String()
		out.LiquidationPrice = &p
	}
	for _, b := range v.Holdings {
		if b.Empty() {
			continue
		}
		out.Balances = append(out.Balances, balanceJSON{
			Asset: b.Asset, Free: b.Free.String(), Locked: b.Locked.String(), Borrowed: b.Borrowed.String(),
			Interest: b.Interest.String(), Net: b.Net().String(),
		})
	}
	return out
}

func (h *Handler) accounts(w http.ResponseWriter, r *http.Request) {
	cross, isolated, err := h.Svc.Accounts(r.Context(), httpx.UserID(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	now := h.Svc.Now()
	out := make([]accountJSON, 0, len(isolated))
	for _, v := range isolated {
		out = append(out, toAccount(v, now))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"cross": toAccount(cross, now), "isolated": out})
}

type transferBody struct {
	Direction string  `json:"direction"`
	Account   string  `json:"account"`
	Symbol    *string `json:"symbol"`
	Asset     string  `json:"asset"`
	Amount    string  `json:"amount"`
}

type transferJSON struct {
	TransferID string  `json:"transfer_id"`
	Direction  string  `json:"direction"`
	Account    string  `json:"account"`
	Symbol     *string `json:"symbol"`
	Asset      string  `json:"asset"`
	Amount     string  `json:"amount"`
	CreatedAt  string  `json:"created_at"`
}

func symbolOf(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (h *Handler) transfer(w http.ResponseWriter, r *http.Request) {
	var body transferBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	dir, err := domain.ParseDirection(body.Direction)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	a, err := domain.ParseAccount(body.Account, symbolOf(body.Symbol))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	amount, err := decimalField("amount", body.Amount)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	t, err := h.Svc.Transfer(r.Context(), application.TransferInput{
		UserID: httpx.UserID(r), IdemKey: r.Header.Get(HeaderIdempotencyKey), Direction: dir, Account: a,
		Asset: strings.ToUpper(body.Asset), Amount: amount,
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, transferJSON{
		TransferID: t.ID, Direction: string(t.Direction), Account: string(t.Account.Type), Symbol: nullable(t.Account.Symbol),
		Asset: t.Asset, Amount: t.Amount.String(), CreatedAt: stamp(t.CreatedAt),
	})
}

type borrowBody struct {
	Account string  `json:"account"`
	Symbol  *string `json:"symbol"`
	Asset   string  `json:"asset"`
	Amount  string  `json:"amount"`
}

type loanJSON struct {
	Account       string  `json:"account"`
	Symbol        *string `json:"symbol"`
	Asset         string  `json:"asset"`
	Principal     string  `json:"principal"`
	Interest      string  `json:"interest"`
	InterestModel string  `json:"interest_model"`
	HourlyRate    string  `json:"hourly_rate"`
	UpdatedAt     string  `json:"updated_at"`
}

func toLoan(v application.LoanView) loanJSON {
	l := v.Loan
	updated := l.UpdatedAt
	if updated.IsZero() {
		updated = time.Now()
	}
	return loanJSON{
		Account: string(l.Account.Type), Symbol: nullable(l.Account.Symbol), Asset: l.Asset, Principal: l.Principal.String(),
		Interest: l.Interest.String(), InterestModel: string(v.Model), HourlyRate: v.Rate.String(), UpdatedAt: stamp(updated),
	}
}

func (h *Handler) borrow(w http.ResponseWriter, r *http.Request) {
	var body borrowBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	a, err := domain.ParseAccount(body.Account, symbolOf(body.Symbol))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	amount, err := decimalField("amount", body.Amount)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	loan, err := h.Svc.Borrow(r.Context(), application.BorrowInput{
		UserID: httpx.UserID(r), IdemKey: r.Header.Get(HeaderIdempotencyKey), Account: a, Asset: strings.ToUpper(body.Asset),
		Amount: amount,
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.writeLoan(w, r, loan)
}

func (h *Handler) writeLoan(w http.ResponseWriter, r *http.Request, loan ports.Loan) {
	v, err := h.Svc.Loan(r.Context(), loan)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toLoan(v))
}

func (h *Handler) repay(w http.ResponseWriter, r *http.Request) {
	var body borrowBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	a, err := domain.ParseAccount(body.Account, symbolOf(body.Symbol))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	in := application.RepayInput{
		UserID: httpx.UserID(r), IdemKey: r.Header.Get(HeaderIdempotencyKey), Account: a, Asset: strings.ToUpper(body.Asset),
	}
	if strings.EqualFold(body.Amount, "ALL") {
		in.All = true
	} else if in.Amount, err = decimalField("amount", body.Amount); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	res, err := h.Svc.Repay(r.Context(), in)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	v, err := h.Svc.Loan(r.Context(), res.Loan)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"interest_repaid": res.Repay.Interest.String(), "principal_repaid": res.Repay.Principal.String(), "loan": toLoan(v),
	})
}

func (h *Handler) loans(w http.ResponseWriter, r *http.Request) {
	a, err := accountParam(r, false)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	list, err := h.Svc.Loans(r.Context(), httpx.UserID(r), a)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]loanJSON, 0, len(list))
	for _, v := range list {
		items = append(items, toLoan(v))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

type interestJSON struct {
	InterestID    string  `json:"interest_id"`
	Account       string  `json:"account"`
	Symbol        *string `json:"symbol"`
	Asset         string  `json:"asset"`
	Principal     string  `json:"principal"`
	HourlyRate    string  `json:"hourly_rate"`
	Interest      string  `json:"interest"`
	InterestModel string  `json:"interest_model"`
	Hour          string  `json:"hour"`
}

func limitParam(r *http.Request) (int, error) {
	s := r.URL.Query().Get("limit")
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 100 {
		return 0, apperr.Invalid("limit must be 1 to 100")
	}
	return n, nil
}

func (h *Handler) interest(w http.ResponseWriter, r *http.Request) {
	a, err := accountParam(r, false)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	limit, err := limitParam(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	q := r.URL.Query()
	list, next, err := h.Svc.Interest(r.Context(), httpx.UserID(r), a, strings.ToUpper(q.Get("asset")), q.Get("cursor"), limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]interestJSON, 0, len(list))
	for _, c := range list {
		items = append(items, interestJSON{
			InterestID: c.ID, Account: string(c.Account.Type), Symbol: nullable(c.Account.Symbol), Asset: c.Asset,
			Principal: c.Principal.String(), HourlyRate: c.Rate.String(), Interest: c.Interest.String(), InterestModel: string(c.Model),
			Hour: stamp(c.Hour),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nullable(next)})
}

// liquidations lists the caller's liquidations; none before batch E3.
func (h *Handler) liquidations(w http.ResponseWriter, r *http.Request) {
	if _, err := accountParam(r, false); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if _, err := limitParam(r); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": []any{}, "next_cursor": nil})
}

func (h *Handler) maxBorrowable(w http.ResponseWriter, r *http.Request) {
	a, err := accountParam(r, true)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	asset := strings.ToUpper(r.URL.Query().Get("asset"))
	if asset == "" {
		httpx.WriteError(w, r, apperr.Invalid("asset is required"))
		return
	}
	b, err := h.Svc.MaxBorrowable(r.Context(), httpx.UserID(r), *a, asset)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"asset": b.Asset, "amount": b.Amount.String(), "limited_by": string(b.LimitedBy)})
}
