package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
)

// fakeMargin answers as margin-service's internal API does: terms set as
// of their version (MARGIN_PARAMS_CHANGED otherwise), in the name of the
// administrator sent; accounts frozen and unfrozen; a liquidation per
// approval.
type fakeMargin struct {
	assets   map[string]*fakeMarginAsset
	pairs    map[string]*fakeMarginPair
	cross    MarginTerms
	crossV   int64
	crossBy  string
	accounts map[string]map[string]any
	// liquidated are the approvals liquidations were started for.
	liquidated []string
	// lose applies the next change but loses its answer.
	lose bool
}

type fakeMarginAsset struct {
	Asset string `json:"asset"`
	MarginAssetTerms
	Lent      string `json:"lent"`
	Version   int64  `json:"version"`
	UpdatedBy string `json:"updated_by"`
}

type fakeMarginPair struct {
	Symbol string `json:"symbol"`
	Base   string `json:"base"`
	Quote  string `json:"quote"`
	MarginPairTerms
	Accounts  int    `json:"accounts"`
	Version   int64  `json:"version"`
	UpdatedBy string `json:"updated_by"`
}

const marginUser = "0199a000-0000-7000-8000-000000000001"

func newFakeMargin() *fakeMargin {
	d := decimal.RequireFromString
	return &fakeMargin{
		assets: map[string]*fakeMarginAsset{
			"BTC": {Asset: "BTC", MarginAssetTerms: MarginAssetTerms{
				Borrowable: true, Collateral: true, Haircut: d("0.95"), PoolCap: d("100"), UserCap: d("10"), InterestModel: "FIXED",
				FixedRate: d("0.0000025"), FloatBase: d("0.000005"), FloatKink: d("0.8"), FloatKinkRate: d("0.00003"), FloatMaxRate: d("0.0001"),
			}, Lent: "1.5", Version: 1},
		},
		pairs: map[string]*fakeMarginPair{
			"BTC-USDT": {Symbol: "BTC-USDT", Base: "BTC", Quote: "USDT", MarginPairTerms: MarginPairTerms{Isolated: true, MarginTerms: MarginTerms{
				Leverage: 10, WarnLevel: d("1.1"), LiquidationLevel: d("1.05"), LiquidationFee: d("0.02"),
			}}, Version: 1},
		},
		cross:  MarginTerms{Leverage: 3, WarnLevel: d("1.3"), LiquidationLevel: d("1.1"), LiquidationFee: d("0.02")},
		crossV: 1,
		accounts: map[string]map[string]any{
			marginUser + " MARGIN_CROSS": {
				"user_id": marginUser, "account": "MARGIN_CROSS", "symbol": nil, "status": "WARNED", "margin_level": "1.25",
				"total_asset": "1250", "total_liability": "1000", "frozen_by": nil, "frozen_reason": nil, "frozen_at": nil,
				// An earlier liquidation, rendered as margin-service does: no level is "".
				"liquidations": []any{map[string]any{"liquidation_id": "l1", "margin_level": "", "status": "SHORTFALL"}},
			},
			marginUser + " MARGIN_ISOLATED:ETH-USDT": {
				"user_id": marginUser, "account": "MARGIN_ISOLATED", "symbol": "ETH-USDT", "status": "NORMAL", "margin_level": nil,
				"total_asset": "300", "total_liability": "0", "frozen_by": nil, "frozen_reason": nil, "frozen_at": nil,
			},
		},
	}
}

var errParamsChanged = apperr.New(apperr.KindConflict, "MARGIN_PARAMS_CHANGED", "changed")

// answer is a change's answer, lost once when asked to.
func (f *fakeMargin) answer(v any) (json.RawMessage, error) {
	if f.lose {
		f.lose = false
		return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "the answer was lost")
	}
	return json.Marshal(v)
}

func (f *fakeMargin) Assets(context.Context) (json.RawMessage, error) {
	items := []any{}
	for _, a := range f.assets {
		items = append(items, a)
	}
	return json.Marshal(map[string]any{"items": items})
}

func (f *fakeMargin) SetAsset(_ context.Context, asset string, terms json.RawMessage, expected int64, admin string) (json.RawMessage, error) {
	a := f.assets[asset]
	if a == nil || a.Version != expected {
		return nil, errParamsChanged
	}
	if err := json.Unmarshal(terms, &a.MarginAssetTerms); err != nil {
		return nil, apperr.Invalid(err.Error())
	}
	a.Version, a.UpdatedBy = a.Version+1, admin
	return f.answer(a)
}

func (f *fakeMargin) settings() map[string]any {
	return map[string]any{"cross": f.cross, "isolated_defaults": []any{}, "version": f.crossV, "updated_by": f.crossBy}
}

func (f *fakeMargin) Settings(context.Context) (json.RawMessage, error) {
	return json.Marshal(f.settings())
}

func (f *fakeMargin) SetCross(_ context.Context, cross json.RawMessage, expected int64, admin string) (json.RawMessage, error) {
	if f.crossV != expected {
		return nil, errParamsChanged
	}
	if err := json.Unmarshal(cross, &f.cross); err != nil {
		return nil, apperr.Invalid(err.Error())
	}
	f.crossV, f.crossBy = f.crossV+1, admin
	return f.answer(f.settings())
}

func (f *fakeMargin) Pairs(context.Context) (json.RawMessage, error) {
	items := []any{}
	for _, p := range f.pairs {
		items = append(items, p)
	}
	return json.Marshal(map[string]any{"items": items})
}

func (f *fakeMargin) SetPair(_ context.Context, symbol string, terms json.RawMessage, expected int64, admin string) (json.RawMessage, error) {
	p := f.pairs[symbol]
	if p == nil || p.Version != expected {
		return nil, errParamsChanged
	}
	if err := json.Unmarshal(terms, &p.MarginPairTerms); err != nil {
		return nil, apperr.Invalid(err.Error())
	}
	p.Version, p.UpdatedBy = p.Version+1, admin
	return f.answer(p)
}

func (f *fakeMargin) Accounts(context.Context, ports.MarginAccountQuery) (json.RawMessage, error) {
	items := []any{}
	for _, a := range f.accounts {
		items = append(items, a)
	}
	return json.Marshal(map[string]any{"items": items, "truncated": false})
}

func (f *fakeMargin) account(userID, account string) (map[string]any, error) {
	a := f.accounts[userID+" "+account]
	if a == nil {
		return nil, apperr.New(apperr.KindNotFound, "MARGIN_ACCOUNT_NOT_FOUND", "no such margin account")
	}
	return a, nil
}

func (f *fakeMargin) Account(_ context.Context, userID, account string) (json.RawMessage, error) {
	a, err := f.account(userID, account)
	if err != nil {
		return nil, err
	}
	return json.Marshal(a)
}

func (f *fakeMargin) Freeze(_ context.Context, userID, account, admin, reason string) (json.RawMessage, error) {
	a, err := f.account(userID, account)
	if err != nil {
		return nil, err
	}
	if a["status"] != "NORMAL" && a["status"] != "WARNED" {
		return nil, apperr.New(apperr.KindConflict, "MARGIN_FROZEN", "frozen")
	}
	a["status"], a["frozen_by"], a["frozen_reason"], a["frozen_at"] = "FROZEN", admin, reason, "2026-09-29T10:00:00Z"
	return f.answer(a)
}

func (f *fakeMargin) Unfreeze(_ context.Context, userID, account, _ string) (json.RawMessage, error) {
	a, err := f.account(userID, account)
	if err != nil {
		return nil, err
	}
	if a["status"] != "FROZEN" || a["frozen_by"] == nil {
		return nil, apperr.New(apperr.KindConflict, "MARGIN_NOT_FROZEN", "not frozen")
	}
	a["status"], a["frozen_by"], a["frozen_reason"], a["frozen_at"] = "NORMAL", nil, nil, nil
	return json.Marshal(a)
}

func (f *fakeMargin) Liquidate(_ context.Context, _, _, approvalID, _ string) (json.RawMessage, error) {
	f.liquidated = append(f.liquidated, approvalID)
	return json.Marshal(map[string]string{"liquidation_id": "liq-" + approvalID[:8]})
}

func assetTerms(f *fakeMargin, edit func(*MarginAssetTerms)) MarginAssetTerms {
	t := f.assets["BTC"].MarginAssetTerms
	edit(&t)
	return t
}

func detailOf(err error) any {
	var e *apperr.Error
	if errors.As(err, &e) {
		return e.Details["approval_id"]
	}
	return nil
}

// TestMarginTerms: what only stops new borrowing applies at once; any
// other change of an asset's, a pair's or the cross account's terms waits
// for a second ADMIN in either mode (MARGIN_PARAMS), one request a target,
// set as of the version read in its requester's name, lapsing after a day.
func TestMarginTerms(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	m := newFakeMargin()
	h.svc.Margin = m
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "second@example.com", domain.RoleAdmin)
	h.admin(t, "ops@example.com", domain.RoleOperator)
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	boss, second, ops, auditor := h.login(t, "boss@example.com"), h.login(t, "second@example.com"), h.login(t, "ops@example.com"),
		h.login(t, "audit@example.com")
	d := decimal.RequireFromString

	raw, err := h.svc.MarginAssets(ctx, auditor)
	if err != nil || !strings.Contains(string(raw), `"pending_approval_id":null`) {
		t.Fatalf("read by an AUDITOR: %s %v", raw, err)
	}
	stop := assetTerms(m, func(t *MarginAssetTerms) { t.Borrowable, t.PoolCap = false, d("50") })
	if _, err := h.svc.SetMarginAsset(ctx, ops, "BTC", stop, 1, "stop lending BTC"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an OPERATOR changes terms: %v", err)
	}
	for name, bad := range map[string]MarginAssetTerms{
		"a haircut of 0":           assetTerms(m, func(t *MarginAssetTerms) { t.Haircut = decimal.Zero }),
		"a user cap over the pool": assetTerms(m, func(t *MarginAssetTerms) { t.UserCap = d("101") }),
		"another model":            assetTerms(m, func(t *MarginAssetTerms) { t.InterestModel = "STEP" }),
		"a falling curve":          assetTerms(m, func(t *MarginAssetTerms) { t.FloatMaxRate = d("0.00001") }),
		"a rate too fine":          assetTerms(m, func(t *MarginAssetTerms) { t.FixedRate = d("0.0000000000001") }),
		"nothing changes":          assetTerms(m, func(*MarginAssetTerms) {}),
	} {
		if _, err := h.svc.SetMarginAsset(ctx, boss, "BTC", bad, 1, "a bad change"); code(err) != apperr.CodeInvalidArgument {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := h.svc.SetMarginAsset(ctx, boss, "DOGE", stop, 1, "no such asset"); code(err) != apperr.CodeNotFound {
		t.Fatalf("an asset not lent: %v", err)
	}

	// Borrowing off and a lower pool cap: at once, audited before and after.
	res, err := h.svc.SetMarginAsset(ctx, boss, "btc", stop, 1, "stop lending BTC")
	if err != nil || res.Approval != nil || m.assets["BTC"].Borrowable || m.assets["BTC"].Version != 2 || m.assets["BTC"].UpdatedBy != "boss@example.com" {
		t.Fatalf("stopped %+v %v %+v", res, err, m.assets["BTC"])
	}
	if !strings.Contains(string(res.Value), `"pending_approval_id":null`) || !strings.Contains(string(res.Value), `"version":2`) {
		t.Fatalf("the asset as changed, with nothing waiting: %s", res.Value)
	}
	if got := h.auditsOf("admin.margin.asset_changed"); len(got) != 1 || !strings.HasPrefix(got[0], "margin:asset:BTC stop lending BTC") ||
		!strings.Contains(got[0], `"changed":["borrowable","pool_cap"]`) || !strings.Contains(got[0], `"version":2`) {
		t.Fatalf("audited %v", got)
	}
	if _, err := h.svc.SetMarginAsset(ctx, boss, "BTC", stop, 1, "a stale version"); code(err) != "MARGIN_PARAMS_CHANGED" {
		t.Fatalf("a stale version: %v", err)
	}

	// Borrowing on again with a new rate: a request for a second ADMIN, as
	// is anything mixed with a stop.
	more := assetTerms(m, func(t *MarginAssetTerms) { t.Borrowable, t.FixedRate = true, d("0.000003") })
	res, err = h.svc.SetMarginAsset(ctx, boss, "BTC", more, 2, "lend BTC again, dearer")
	if err != nil || res.Approval == nil || m.assets["BTC"].Version != 2 {
		t.Fatalf("a request %+v %v", res, err)
	}
	a := *res.Approval
	if a.Kind != domain.KindMarginParams || a.Mode != domain.ModeTwoPerson || a.Escalation != domain.EscalationMarginRisk ||
		a.Payload["target"] != "asset:BTC" || a.Payload["expected_version"] != "2" || a.Payload["changed"] != "borrowable,fixed_rate" ||
		!strings.Contains(a.Payload["previous"], `"borrowable":false`) || !strings.Contains(a.Payload["terms"], `"fixed_rate":"0.000003"`) {
		t.Fatalf("the request %+v", a)
	}
	if got := h.auditsOf("admin.margin.params_requested"); len(got) != 1 || !strings.HasPrefix(got[0], "margin:asset:BTC ") {
		t.Fatalf("the request audited %v", got)
	}
	// One request an asset at a time, whoever asks.
	other := assetTerms(m, func(t *MarginAssetTerms) { t.Haircut = d("0.9") })
	if _, err := h.svc.SetMarginAsset(ctx, second, "BTC", other, 2, "a lower haircut"); code(err) != "ADMIN_MARGIN_CHANGE_PENDING" || detailOf(err) != a.ID {
		t.Fatalf("a second request while one waits: %v", err)
	}
	if raw, _ := h.svc.MarginAssets(ctx, auditor); !strings.Contains(string(raw), `"pending_approval_id":"`+a.ID+`"`) {
		t.Fatalf("the waiting request not shown: %s", raw)
	}
	if _, err := h.svc.DecideApproval(ctx, boss, a.ID, true, "my own"); code(err) != "ADMIN_SELF_APPROVAL" {
		t.Fatalf("approved by its requester: %v", err)
	}
	if _, err := h.svc.DecideApproval(ctx, ops, a.ID, true, "an OPERATOR approves"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("approved by an OPERATOR: %v", err)
	}
	// margin-service set it but the answer was lost: still pending; approved
	// again, its 409 is the earlier attempt's work.
	m.lose = true
	if _, err := h.svc.DecideApproval(ctx, second, a.ID, true, "agreed"); err == nil {
		t.Fatal("a lost answer decided the request")
	}
	if got := h.store.approvals[a.ID]; got.Status != domain.ApprovalPending || m.assets["BTC"].Version != 3 || m.assets["BTC"].UpdatedBy != "boss@example.com" {
		t.Fatalf("after the lost answer %+v %+v", got, m.assets["BTC"])
	}
	done, err := h.svc.DecideApproval(ctx, second, a.ID, true, "agreed again")
	if err != nil || done.Status != domain.ApprovalExecuted || done.Result != "asset:BTC version 3 (set by an earlier attempt)" {
		t.Fatalf("approved again %+v %v", done, err)
	}

	// A request whose terms changed meanwhile fails when approved.
	res, err = h.svc.SetMarginAsset(ctx, boss, "BTC", assetTerms(m, func(t *MarginAssetTerms) { t.Haircut = d("0.9") }), 3, "a lower haircut")
	if err != nil || res.Approval == nil {
		t.Fatalf("a request %+v %v", res, err)
	}
	if _, err := h.svc.SetMarginAsset(ctx, second, "BTC", assetTerms(m, func(t *MarginAssetTerms) { t.UserCap = d("5") }), 3, "a lower cap"); err != nil {
		t.Fatalf("a stop while a request waits: %v", err)
	}
	done, err = h.svc.DecideApproval(ctx, second, res.Approval.ID, true, "too late")
	if err != nil || done.Status != domain.ApprovalFailed || !strings.HasPrefix(done.Result, "MARGIN_PARAMS_CHANGED") || !m.assets["BTC"].Haircut.Equal(d("0.95")) {
		t.Fatalf("a stale request %+v %v", done, err)
	}

	// A pair: isolated accounts off at once; its leverage waits.
	pair := m.pairs["BTC-USDT"].MarginPairTerms
	pair.Isolated = false
	if res, err := h.svc.SetMarginPair(ctx, boss, "BTC-USDT", pair, 1, "no new isolated accounts"); err != nil || res.Approval != nil || m.pairs["BTC-USDT"].Isolated {
		t.Fatalf("isolated off %+v %v", res, err)
	}
	if got := h.auditsOf("admin.margin.pair_changed"); len(got) != 1 || !strings.HasPrefix(got[0], "margin:pair:BTC-USDT ") {
		t.Fatalf("audited %v", got)
	}
	pair.Leverage, pair.WarnLevel, pair.LiquidationLevel = 5, d("1.2"), d("1.1")
	res, err = h.svc.SetMarginPair(ctx, boss, "BTC-USDT", pair, 2, "5x on BTC")
	if err != nil || res.Approval == nil || res.Approval.Payload["changed"] != "leverage,warn_level,liquidation_level" {
		t.Fatalf("a pair's request %+v %v", res, err)
	}
	if done, err := h.svc.DecideApproval(ctx, second, res.Approval.ID, true, "agreed"); err != nil || done.Status != domain.ApprovalExecuted ||
		m.pairs["BTC-USDT"].Leverage != 5 || m.pairs["BTC-USDT"].UpdatedBy != "boss@example.com" {
		t.Fatalf("a pair's change approved %+v %v %+v", done, err, m.pairs["BTC-USDT"])
	}

	// The cross account's terms: every change waits.
	cross := m.cross
	cross.WarnLevel = d("1.35")
	if _, err := h.svc.SetMarginSettings(ctx, boss, MarginTerms{Leverage: 3, WarnLevel: d("1.1"), LiquidationLevel: d("1.1"), LiquidationFee: d("0.02")}, 1,
		"levels crossed"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("the warning level at the liquidation level: %v", err)
	}
	req, err := h.svc.SetMarginSettings(ctx, boss, cross, 1, "warn earlier")
	if err != nil || req.Payload["target"] != "cross" || m.crossV != 1 {
		t.Fatalf("the cross terms' request %+v %v", req, err)
	}
	if raw, _ := h.svc.MarginSettings(ctx, auditor); !strings.Contains(string(raw), `"pending_approval_id":"`+req.ID+`"`) {
		t.Fatalf("the settings' waiting request not shown: %s", raw)
	}

	// Not decided within a day, it lapses: listed as expired, no longer in
	// the way of another, and approving it fails it and sets nothing.
	h.now = h.now.Add(marginApprovalTTL)
	list, _, err := h.svc.Approvals(ctx, boss, domain.ApprovalPending, "", 10)
	if err != nil || len(list) != 1 || !list[0].Lapsed {
		t.Fatalf("listed %+v %v", list, err)
	}
	again, err := h.svc.SetMarginSettings(ctx, boss, cross, 1, "warn earlier, asked again")
	if err != nil || again.ID == req.ID {
		t.Fatalf("asked again once lapsed %+v %v", again, err)
	}
	done, err = h.svc.DecideApproval(ctx, second, req.ID, true, "a day late")
	if err != nil || done.Status != domain.ApprovalFailed || !strings.HasPrefix(done.Result, "expired at ") || m.crossV != 1 {
		t.Fatalf("a lapsed request %+v %v", done, err)
	}
	if done, err := h.svc.DecideApproval(ctx, second, again.ID, true, "agreed"); err != nil || done.Status != domain.ApprovalExecuted ||
		!m.cross.WarnLevel.Equal(d("1.35")) || m.crossBy != "boss@example.com" {
		t.Fatalf("the cross terms approved %+v %v", done, err)
	}
}

// TestMarginAccounts: freezing and unfreezing at once (a retry after a
// lost answer finds its own freeze), audited on the user; a liquidation by
// hand waits for a second administrator with derivatives.write, only while
// margin-service liquidates, only of an account that owes.
func TestMarginAccounts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	m := newFakeMargin()
	features := onFlags{}
	h.svc.Margin, h.svc.Features = m, features
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "ops@example.com", domain.RoleOperator)
	h.admin(t, "finance@example.com", domain.RoleFinance)
	boss, ops, finance := h.login(t, "boss@example.com"), h.login(t, "ops@example.com"), h.login(t, "finance@example.com")

	raw, err := h.svc.MarginAccounts(ctx, finance, MarginAccountQuery{})
	if err != nil || strings.Contains(string(raw), `"frozen_reason":null`) || !strings.Contains(string(raw), `"frozen_reason":""`) {
		t.Fatalf("listed %s %v", raw, err)
	}
	if _, err := h.svc.MarginAccount(ctx, finance, marginUser, "MARGIN_ISOLATED"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("an isolated account without its pair: %v", err)
	}
	if _, err := h.svc.FreezeMarginAccount(ctx, finance, marginUser, "MARGIN_CROSS", "suspicious trading"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("FINANCE freezes: %v", err)
	}
	if _, err := h.svc.FreezeMarginAccount(ctx, ops, marginUser, "MARGIN_CROSS", strings.Repeat("x", 501)); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("a reason too long: %v", err)
	}
	m.lose = true
	if _, err := h.svc.FreezeMarginAccount(ctx, ops, marginUser, "MARGIN_CROSS", "suspicious trading"); err == nil {
		t.Fatal("a lost answer froze it")
	}
	raw, err = h.svc.FreezeMarginAccount(ctx, ops, marginUser, "MARGIN_CROSS", "suspicious trading")
	if err != nil || !strings.Contains(string(raw), `"status":"FROZEN"`) {
		t.Fatalf("frozen again after a lost answer %s %v", raw, err)
	}
	if got := h.auditsOf("admin.margin.account_frozen"); len(got) != 1 || !strings.HasPrefix(got[0], "user:"+marginUser+" suspicious trading") ||
		!strings.Contains(got[0], `"earlier_attempt":true`) || !strings.Contains(got[0], `"account":"MARGIN_CROSS"`) {
		t.Fatalf("audited %v", got)
	}
	if _, err := h.svc.FreezeMarginAccount(ctx, boss, marginUser, "MARGIN_CROSS", "frozen twice"); code(err) != "MARGIN_FROZEN" {
		t.Fatalf("another's freeze again: %v", err)
	}
	if _, err := h.svc.UnfreezeMarginAccount(ctx, ops, marginUser, "MARGIN_CROSS", "cleared"); err != nil || len(h.auditsOf("admin.margin.account_unfrozen")) != 1 {
		t.Fatalf("unfrozen %v", err)
	}
	if _, err := h.svc.UnfreezeMarginAccount(ctx, ops, marginUser, "MARGIN_CROSS", "cleared again"); code(err) != "MARGIN_NOT_FROZEN" {
		t.Fatalf("unfrozen twice: %v", err)
	}

	// A liquidation by hand: not while margin-service liquidates nothing.
	if _, err := h.svc.LiquidateMarginAccount(ctx, ops, marginUser, "MARGIN_CROSS", "cannot repay", "k1"); code(err) != "ADMIN_MARGIN_LIQUIDATION_OFF" {
		t.Fatalf("liquidations off: %v", err)
	}
	features[flags.KeyMarginLiquidation] = true
	if _, err := h.svc.LiquidateMarginAccount(ctx, ops, marginUser, "margin_isolated:eth-usdt", "owes nothing", "k0"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("a lower-case account: %v", err)
	}
	if _, err := h.svc.LiquidateMarginAccount(ctx, ops, marginUser, "MARGIN_ISOLATED:eth-usdt", "owes nothing", "k0"); code(err) != "ADMIN_MARGIN_NOTHING_OWED" {
		t.Fatalf("nothing owed: %v", err)
	}
	a, err := h.svc.LiquidateMarginAccount(ctx, ops, marginUser, "MARGIN_CROSS", "cannot repay", "k1")
	if err != nil || a.Kind != domain.KindMarginLiquidate || a.Escalation != domain.EscalationMarginRisk || a.ValueUSDT == nil || a.ValueUSDT.String() != "1000" ||
		a.Payload["account"] != "MARGIN_CROSS" || a.Payload["margin_level"] != "1.25" || len(m.liquidated) != 0 {
		t.Fatalf("the request %+v %v", a, err)
	}
	if same, err := h.svc.LiquidateMarginAccount(ctx, ops, marginUser, "MARGIN_CROSS", "cannot repay", "k1"); err != nil || same.ID != a.ID {
		t.Fatalf("the same request again %+v %v", same, err)
	}
	if _, err := h.svc.LiquidateMarginAccount(ctx, boss, marginUser, "MARGIN_CROSS", "really cannot repay", "k2"); code(err) != "ADMIN_MARGIN_CHANGE_PENDING" ||
		detailOf(err) != a.ID {
		t.Fatalf("a second request while one waits: %v", err)
	}
	if raw, _ := h.svc.MarginAccount(ctx, finance, marginUser, "MARGIN_CROSS"); !strings.Contains(string(raw), `"pending_approval_id":"`+a.ID+`"`) ||
		!strings.Contains(string(raw), `"liquidations":[{"liquidation_id":"l1","margin_level":null,"status":"SHORTFALL"}]`) {
		t.Fatalf("the waiting request not shown, or a liquidation's missing level not null: %s", raw)
	}
	if _, err := h.svc.DecideApproval(ctx, finance, a.ID, true, "finance approves"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("approved by FINANCE: %v", err)
	}
	done, err := h.svc.DecideApproval(ctx, boss, a.ID, true, "agreed")
	if err != nil || done.Status != domain.ApprovalExecuted || done.Result != "liquidation liq-"+a.ID[:8] || len(m.liquidated) != 1 || m.liquidated[0] != a.ID {
		t.Fatalf("approved %+v %v %v", done, err, m.liquidated)
	}
	if got := h.auditsOf("admin.margin.liquidation_requested"); len(got) != 1 || !strings.HasPrefix(got[0], "user:"+marginUser+" cannot repay") {
		t.Fatalf("the request audited %v", got)
	}
}

// TestLaunchMargin: margin trading off is ready; on, it needs the
// liquidations on and no switch on for everyone without rules.
func TestLaunchMargin(t *testing.T) {
	rules := json.RawMessage(`{"users":{"only":["0199a000-0000-7000-8000-000000000001"]}}`)
	for name, c := range map[string]struct {
		flags []ports.Flag
		want  string
	}{
		"off": {nil, LaunchOK},
		"on for everyone": {[]ports.Flag{
			{Key: flags.KeyMarginEnabled, Enabled: true}, {Key: flags.KeyMarginLiquidation, Enabled: true},
		}, LaunchFail},
		"on by rules, no liquidations": {[]ports.Flag{{Key: flags.KeyMarginEnabled, Enabled: true, Rules: rules}}, LaunchFail},
		"orders borrowing for everyone": {[]ports.Flag{
			{Key: flags.KeyMarginEnabled, Enabled: true, Rules: rules},
			{Key: flags.KeyMarginLiquidation, Enabled: true},
			{Key: flags.KeyMarginAutoBorrow, Enabled: true},
		}, LaunchFail},
		"on by rules": {[]ports.Flag{
			{Key: flags.KeyMarginEnabled, Enabled: true, Rules: rules},
			{Key: flags.KeyMarginLiquidation, Enabled: true},
			{Key: flags.KeyMarginAutoBorrow, Enabled: true, Rules: rules},
		}, LaunchOK},
	} {
		flagged := map[string]ports.Flag{}
		for _, f := range c.flags {
			flagged[f.Key] = f
		}
		if got, value := launchMargin(flagged); got != c.want {
			t.Fatalf("%s: %s %v", name, got, value)
		}
	}
}
