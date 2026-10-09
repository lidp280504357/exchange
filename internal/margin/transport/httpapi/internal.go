package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/margin/application"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
)

// HeaderAdminID names the administrator admin-service acts for (their
// email): it becomes the terms' updated_by and a freeze's frozen_by.
const HeaderAdminID = "X-Admin-Id"

// InternalRoutes serves the admin console through admin-service
// (coordination decision of 2026-10-06 03:24 ⑥): only on the compose
// network — the gateway does not route /internal, and a request that came
// through it (it carries the caller's X-User-Id) is not served. Field
// names are margin-service's columns.
func (h *Handler) InternalRoutes(r chi.Router) {
	r.Route("/internal/margin", func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if httpx.UserID(r) != "" {
					httpx.WriteError(w, r, apperr.NotFound("no such endpoint"))
					return
				}
				next.ServeHTTP(w, r)
			})
		})
		r.Get("/assets", h.consoleAssets)
		r.Put("/assets/{asset}", h.setAsset)
		r.Get("/settings", h.consoleSettings)
		r.Put("/settings", h.setSettings)
		r.Get("/pairs", h.consolePairs)
		r.Put("/pairs/{symbol}", h.setPair)
		r.Get("/accounts", h.consoleAccounts)
		r.Post("/accounts/list", h.consoleAccounts)
		r.Get("/accounts/{user_id}/{account}", h.consoleAccount)
		r.Post("/accounts/{user_id}/{account}/freeze", h.freeze)
		r.Post("/accounts/{user_id}/{account}/unfreeze", h.unfreeze)
		r.Post("/accounts/{user_id}/{account}/liquidate", h.liquidate)
	})
}

// updatedBy shows the seed as "" (the console's "种子").
func updatedBy(by string) string {
	if by == application.SourceFile {
		return ""
	}
	return by
}

type consoleAssetJSON struct {
	Asset         string `json:"asset"`
	Borrowable    bool   `json:"borrowable"`
	Collateral    bool   `json:"collateral"`
	Haircut       string `json:"haircut"`
	PoolCap       string `json:"pool_cap"`
	UserCap       string `json:"user_cap"`
	InterestModel string `json:"interest_model"`
	FixedRate     string `json:"fixed_rate"`
	FloatBase     string `json:"float_base"`
	FloatKink     string `json:"float_kink"`
	FloatKinkRate string `json:"float_kink_rate"`
	FloatMaxRate  string `json:"float_max_rate"`
	Lent          string `json:"lent"`
	PoolAvailable string `json:"pool_available"`
	Utilization   string `json:"utilization"`
	HourlyRate    string `json:"hourly_rate"`
	RateHour      string `json:"rate_hour"`
	InterestOwed  string `json:"interest_owed"`
	Borrowers     int    `json:"borrowers"`
	Version       int64  `json:"version"`
	UpdatedBy     string `json:"updated_by"`
	UpdatedAt     string `json:"updated_at"`
}

func toConsoleAsset(a application.ConsoleAsset) consoleAssetJSON {
	t := a.Terms
	return consoleAssetJSON{
		Asset: t.Asset, Borrowable: t.Borrowable, Collateral: t.Collateral, Haircut: t.Haircut.String(), PoolCap: t.PoolCap.String(),
		UserCap: t.UserCap.String(), InterestModel: string(t.Model), FixedRate: t.FixedRate.String(), FloatBase: t.Floating.Base.String(),
		FloatKink: t.Floating.Kink.String(), FloatKinkRate: t.Floating.KinkRate.String(), FloatMaxRate: t.Floating.MaxRate.String(),
		Lent: a.Lent.String(), PoolAvailable: decimal.Max(t.PoolCap.Sub(a.Lent), decimal.Zero).String(), Utilization: a.Utilization.String(),
		HourlyRate: a.Rate.String(), RateHour: stamp(a.RateHour), InterestOwed: a.InterestOwed.String(), Borrowers: a.Borrowers,
		Version: a.Meta.Version, UpdatedBy: updatedBy(a.Meta.UpdatedBy), UpdatedAt: stamp(a.Meta.UpdatedAt),
	}
}

func (h *Handler) consoleAssets(w http.ResponseWriter, r *http.Request) {
	all, err := h.Svc.ConsoleAssets(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]consoleAssetJSON, 0, len(all))
	for _, a := range all {
		items = append(items, toConsoleAsset(a))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// adminOf reads the administrator a write is for.
func adminOf(r *http.Request) (string, error) {
	by := strings.TrimSpace(r.Header.Get(HeaderAdminID))
	if by == "" || len(by) > 200 {
		return "", apperr.Invalid("the administrator is required (" + HeaderAdminID + ")")
	}
	return by, nil
}

func (h *Handler) setAsset(w http.ResponseWriter, r *http.Request) {
	by, err := adminOf(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	var body struct {
		Borrowable      *bool  `json:"borrowable"`
		Collateral      *bool  `json:"collateral"`
		Haircut         string `json:"haircut"`
		PoolCap         string `json:"pool_cap"`
		UserCap         string `json:"user_cap"`
		InterestModel   string `json:"interest_model"`
		FixedRate       string `json:"fixed_rate"`
		FloatBase       string `json:"float_base"`
		FloatKink       string `json:"float_kink"`
		FloatKinkRate   string `json:"float_kink_rate"`
		FloatMaxRate    string `json:"float_max_rate"`
		ExpectedVersion *int64 `json:"expected_version"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if body.Borrowable == nil || body.Collateral == nil || body.ExpectedVersion == nil {
		httpx.WriteError(w, r, apperr.Invalid("borrowable, collateral and expected_version are required"))
		return
	}
	t := domain.AssetTerms{
		Asset: strings.ToUpper(chi.URLParam(r, "asset")), Borrowable: *body.Borrowable, Collateral: *body.Collateral,
		Model: domain.InterestModel(body.InterestModel),
	}
	for _, f := range []struct {
		name string
		s    string
		to   *decimal.Decimal
	}{
		{"haircut", body.Haircut, &t.Haircut},
		{"pool_cap", body.PoolCap, &t.PoolCap},
		{"user_cap", body.UserCap, &t.UserCap},
		{"fixed_rate", body.FixedRate, &t.FixedRate},
		{"float_base", body.FloatBase, &t.Floating.Base},
		{"float_kink", body.FloatKink, &t.Floating.Kink},
		{"float_kink_rate", body.FloatKinkRate, &t.Floating.KinkRate},
		{"float_max_rate", body.FloatMaxRate, &t.Floating.MaxRate},
	} {
		if *f.to, err = decimalField(f.name, f.s); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
	}
	a, err := h.Svc.SetAssetTerms(r.Context(), t, *body.ExpectedVersion, by)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toConsoleAsset(a))
}

type levelsJSON struct {
	Leverage         int    `json:"leverage"`
	WarnLevel        string `json:"warn_level"`
	LiquidationLevel string `json:"liquidation_level"`
}

type crossJSON struct {
	Leverage         int    `json:"leverage"`
	WarnLevel        string `json:"warn_level"`
	LiquidationLevel string `json:"liquidation_level"`
	LiquidationFee   string `json:"liquidation_fee"`
}

// terms reads leverage, levels and fee.
func (c crossJSON) terms() (domain.Terms, error) {
	t := domain.Terms{Leverage: c.Leverage}
	var err error
	if t.WarnLevel, err = decimalField("warn_level", c.WarnLevel); err != nil {
		return t, err
	}
	if t.LiquidationLevel, err = decimalField("liquidation_level", c.LiquidationLevel); err != nil {
		return t, err
	}
	t.LiquidationFee, err = decimalField("liquidation_fee", c.LiquidationFee)
	return t, err
}

func toCross(t domain.Terms) crossJSON {
	return crossJSON{
		Leverage: t.Leverage, WarnLevel: t.WarnLevel.String(), LiquidationLevel: t.LiquidationLevel.String(),
		LiquidationFee: t.LiquidationFee.String(),
	}
}

func writeSettings(w http.ResponseWriter, s application.ConsoleSettings) {
	defaults := make([]levelsJSON, 0, len(s.IsolatedDefaults))
	for _, t := range s.IsolatedDefaults {
		defaults = append(defaults, levelsJSON{Leverage: t.Leverage, WarnLevel: t.WarnLevel.String(), LiquidationLevel: t.LiquidationLevel.String()})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"cross": toCross(s.Cross), "isolated_defaults": defaults, "version": s.Meta.Version, "updated_by": updatedBy(s.Meta.UpdatedBy),
		"updated_at": stamp(s.Meta.UpdatedAt),
	})
}

func (h *Handler) consoleSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.Svc.ConsoleSettings(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeSettings(w, s)
}

func (h *Handler) setSettings(w http.ResponseWriter, r *http.Request) {
	by, err := adminOf(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	var body struct {
		Cross           *crossJSON `json:"cross"`
		ExpectedVersion *int64     `json:"expected_version"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if body.Cross == nil || body.ExpectedVersion == nil {
		httpx.WriteError(w, r, apperr.Invalid("cross and expected_version are required"))
		return
	}
	t, err := body.Cross.terms()
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	s, err := h.Svc.SetCrossTerms(r.Context(), t, *body.ExpectedVersion, by)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeSettings(w, s)
}

type consolePairJSON struct {
	Symbol           string `json:"symbol"`
	Base             string `json:"base"`
	Quote            string `json:"quote"`
	Isolated         bool   `json:"isolated"`
	Leverage         int    `json:"leverage"`
	WarnLevel        string `json:"warn_level"`
	LiquidationLevel string `json:"liquidation_level"`
	LiquidationFee   string `json:"liquidation_fee"`
	Accounts         int    `json:"accounts"`
	Version          int64  `json:"version"`
	UpdatedBy        string `json:"updated_by"`
	UpdatedAt        string `json:"updated_at"`
}

func toConsolePair(c application.ConsolePair) consolePairJSON {
	p := c.Pair
	return consolePairJSON{
		Symbol: p.Symbol, Base: p.Base, Quote: p.Quote, Isolated: p.Isolated, Leverage: p.Terms.Leverage,
		WarnLevel: p.Terms.WarnLevel.String(), LiquidationLevel: p.Terms.LiquidationLevel.String(),
		LiquidationFee: p.Terms.LiquidationFee.String(), Accounts: c.Accounts, Version: c.Meta.Version,
		UpdatedBy: updatedBy(c.Meta.UpdatedBy), UpdatedAt: stamp(c.Meta.UpdatedAt),
	}
}

func (h *Handler) consolePairs(w http.ResponseWriter, r *http.Request) {
	all, err := h.Svc.ConsolePairs(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]consolePairJSON, 0, len(all))
	for _, p := range all {
		items = append(items, toConsolePair(p))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) setPair(w http.ResponseWriter, r *http.Request) {
	by, err := adminOf(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	var body struct {
		crossJSON
		Isolated        *bool  `json:"isolated"`
		ExpectedVersion *int64 `json:"expected_version"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if body.Isolated == nil || body.ExpectedVersion == nil {
		httpx.WriteError(w, r, apperr.Invalid("isolated and expected_version are required"))
		return
	}
	t, err := body.terms()
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	p := domain.Pair{Symbol: strings.ToUpper(chi.URLParam(r, "symbol")), Isolated: *body.Isolated, Terms: t}
	c, err := h.Svc.SetPairTerms(r.Context(), p, *body.ExpectedVersion, by)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toConsolePair(c))
}

type consoleAccountJSON struct {
	UserID           string   `json:"user_id"`
	Account          string   `json:"account"`
	Symbol           *string  `json:"symbol"`
	Leverage         int      `json:"leverage"`
	Status           string   `json:"status"`
	MarginLevel      *string  `json:"margin_level"`
	WarnLevel        string   `json:"warn_level"`
	LiquidationLevel string   `json:"liquidation_level"`
	TotalAsset       string   `json:"total_asset"`
	TotalLiability   string   `json:"total_liability"`
	NetAsset         string   `json:"net_asset"`
	LiquidationPrice *string  `json:"liquidation_price"`
	WarnedAt         *string  `json:"warned_at"`
	FrozenBy         *string  `json:"frozen_by"`
	FrozenReason     *string  `json:"frozen_reason"`
	FrozenAt         *string  `json:"frozen_at"`
	Unpriced         []string `json:"unpriced"`
	UpdatedAt        string   `json:"updated_at"`
}

func stampOf(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := stamp(t)
	return &s
}

func toConsoleAccount(c application.ConsoleAccount) consoleAccountJSON {
	v := toAccount(c.View, c.State.UpdatedAt)
	out := consoleAccountJSON{
		UserID: c.State.UserID, Account: v.Account, Symbol: v.Symbol, Leverage: v.Leverage, Status: v.Status, MarginLevel: v.MarginLevel,
		WarnLevel: v.WarnLevel, LiquidationLevel: v.LiquidationLevel, TotalAsset: v.TotalAsset, TotalLiability: v.TotalLiability,
		NetAsset: v.NetAsset, LiquidationPrice: v.LiquidationPrice, WarnedAt: stampOf(c.State.WarnedAt), Unpriced: c.Unpriced,
		UpdatedAt: v.UpdatedAt,
	}
	if c.State.FrozenBy != "" {
		out.FrozenBy, out.FrozenReason, out.FrozenAt = &c.State.FrozenBy, &c.State.FrozenReason, stampOf(c.State.FrozenAt)
	}
	return out
}

// consoleAccounts lists the margin accounts riskiest first: user_id,
// user_ids or exclude_user_ids (review L3: the console's real users; in
// the JSON body of POST .../list, up to 5,000, review C76;
// api/internal/margin.yaml), account, symbol, status, limit.
func (h *Handler) consoleAccounts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	only, except, err := httpx.UserIDsOf(w, r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	f := ports.AccountFilter{
		UserID: q.Get("user_id"), UserIDs: only, ExcludeUserIDs: except, Type: domain.AccountType(q.Get("account")),
		Symbol: strings.ToUpper(q.Get("symbol")), Status: domain.Status(q.Get("status")),
	}
	if f.UserID != "" {
		if _, err := uuid.Parse(f.UserID); err != nil {
			httpx.WriteError(w, r, apperr.Invalid("user_id must be a UUID"))
			return
		}
	}
	switch f.Type {
	case "", domain.AccountCross, domain.AccountIsolated:
	default:
		httpx.WriteError(w, r, apperr.Invalid("account must be MARGIN_CROSS or MARGIN_ISOLATED"))
		return
	}
	switch f.Status {
	case "", domain.StatusNormal, domain.StatusWarned, domain.StatusLiquidating, domain.StatusFrozen:
	default:
		httpx.WriteError(w, r, apperr.Invalid("status must be NORMAL, WARNED, LIQUIDATING or FROZEN"))
		return
	}
	limit := 500
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			httpx.WriteError(w, r, apperr.Invalid("limit is 1 to 500"))
			return
		}
		limit = n
	}
	all, truncated, err := h.Svc.ConsoleAccounts(r.Context(), f, limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]consoleAccountJSON, 0, len(all))
	for _, c := range all {
		items = append(items, toConsoleAccount(c))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "truncated": truncated})
}

// accountKey reads the {user_id} and {account} path parameters: account is
// MARGIN_CROSS or MARGIN_ISOLATED:<symbol>.
func accountKey(r *http.Request) (string, domain.Account, error) {
	user := chi.URLParam(r, "user_id")
	if _, err := uuid.Parse(user); err != nil {
		return "", domain.Account{}, apperr.Invalid("user_id must be a UUID")
	}
	accountType, symbol, _ := strings.Cut(chi.URLParam(r, "account"), ":")
	a, err := domain.ParseAccount(accountType, symbol)
	return user, a, err
}

func optional(v decimal.Decimal, ok bool) *string {
	if !ok {
		return nil
	}
	s := v.String()
	return &s
}

func (h *Handler) consoleAccount(w http.ResponseWriter, r *http.Request) {
	user, a, err := accountKey(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	d, err := h.Svc.ConsoleAccountDetail(r.Context(), user, a)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	balances := make([]map[string]any, 0, len(d.Balances))
	for _, b := range d.Balances {
		held := b.Holding.Total().Mul(b.Price).Mul(b.Haircut)
		owed := b.Holding.Debt().Mul(b.Price)
		balances = append(balances, map[string]any{
			"asset": b.Holding.Asset, "free": b.Holding.Free.String(), "locked": b.Holding.Locked.String(),
			"borrowed": b.Holding.Borrowed.String(), "interest": b.Holding.Interest.String(), "net": b.Holding.Net().String(),
			"price_usdt": optional(b.Price, b.Priced), "asset_usdt": optional(held.Round(8), b.Priced),
			"liability_usdt": optional(owed.RoundCeil(8), b.Priced), "haircut": b.Haircut.String(), "hourly_rate": b.Rate.String(),
		})
	}
	loans := make([]map[string]any, 0, len(d.Loans))
	for _, l := range d.Loans {
		loans = append(loans, map[string]any{
			"asset": l.Loan.Asset, "principal": l.Loan.Principal.String(), "interest": l.Loan.Interest.String(),
			"interest_model": string(l.Model), "hourly_rate": l.Rate.String(), "opened_at": stampOf(l.Loan.OpenedAt),
			"updated_at": stamp(l.Loan.UpdatedAt),
		})
	}
	changes := make([]map[string]any, 0, len(d.Changes))
	for _, c := range d.Changes {
		changes = append(changes, map[string]any{
			"id": c.ID, "asset": c.Asset, "kind": c.Kind, "status": string(c.Status), "amount": c.Amount.String(),
			"principal_part": c.PrincipalPart.String(), "interest_part": c.InterestPart.String(), "reason": nullable(c.Reason),
			"order_id": nullable(c.OrderID), "liquidation_id": nullable(c.LiquidationID), "journal_key": c.JournalKey,
			"created_at": stamp(c.CreatedAt),
		})
	}
	charges := make([]map[string]any, 0, len(d.Interest))
	for _, c := range d.Interest {
		key := c.JournalKey
		if c.BorrowID != "" {
			key = "margin:margin-borrow:" + c.BorrowID + ":1"
		}
		charges = append(charges, map[string]any{
			"interest_id": c.ID, "asset": c.Asset, "principal": c.Principal.String(), "interest_model": string(c.Model),
			"hourly_rate": c.Rate.String(), "interest": c.Interest.String(), "hour": stamp(c.Hour), "status": string(c.Status),
			"journal_key": key,
		})
	}
	liquidations, _, err := h.Svc.Liquidations(r.Context(), user, &a, "", 20)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	liqs := make([]map[string]any, 0, len(liquidations))
	for _, l := range liquidations {
		liqs = append(liqs, liquidationJSON(l, true))
	}
	acc := toConsoleAccount(d.ConsoleAccount)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user_id": acc.UserID, "account": acc.Account, "symbol": acc.Symbol, "leverage": acc.Leverage, "status": acc.Status,
		"margin_level": acc.MarginLevel, "warn_level": acc.WarnLevel, "liquidation_level": acc.LiquidationLevel,
		"total_asset": acc.TotalAsset, "total_liability": acc.TotalLiability, "net_asset": acc.NetAsset,
		"liquidation_price": acc.LiquidationPrice, "warned_at": acc.WarnedAt, "frozen_by": acc.FrozenBy,
		"frozen_reason": acc.FrozenReason, "frozen_at": acc.FrozenAt, "unpriced": acc.Unpriced, "updated_at": acc.UpdatedAt,
		"balances": balances, "loans": loans, "loan_changes": changes, "interest": charges,
		"liquidations": liqs,
	})
}

func (h *Handler) freeze(w http.ResponseWriter, r *http.Request) {
	by, err := adminOf(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	user, a, err := accountKey(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if strings.TrimSpace(body.Reason) == "" || len(body.Reason) > 500 {
		httpx.WriteError(w, r, apperr.Invalid("a reason of at most 500 bytes is required"))
		return
	}
	c, err := h.Svc.Freeze(r.Context(), user, a, by, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toConsoleAccount(c))
}

// liquidate starts an account's liquidation for an approved request of
// the administrators (MANUAL; the same approval again returns it).
func (h *Handler) liquidate(w http.ResponseWriter, r *http.Request) {
	by, err := adminOf(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	user, a, err := accountKey(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	var body struct {
		ApprovalID string `json:"approval_id"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if _, err := uuid.Parse(body.ApprovalID); err != nil {
		httpx.WriteError(w, r, apperr.Invalid("approval_id must be a UUID"))
		return
	}
	l, err := h.Svc.StartLiquidation(r.Context(), user, a, ports.TriggerManual, body.ApprovalID, by)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, liquidationJSON(l, true))
}

func (h *Handler) unfreeze(w http.ResponseWriter, r *http.Request) {
	by, err := adminOf(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	user, a, err := accountKey(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	c, err := h.Svc.Unfreeze(r.Context(), user, a, by)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toConsoleAccount(c))
}
