package application

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/pg"
)

// Margin trading's console (margin design 2026-10-06 §8, batch E5):
// margin-service's terms and accounts through its internal API, and the
// read models' liquidations and interest. Only what stops new borrowing
// applies at once (borrowing or isolated accounts switched off, a lower
// cap); every other change of the terms waits for a second ADMIN with
// instruments.trading in either approval mode (MARGIN_PARAMS, escalation
// MARGIN_RISK: the coordinator's strict scope of 2026-10-06 03:24 ②), as
// of the version read, one request a target at a time, lapsing after a
// day. A liquidation by hand always waits for a second administrator with
// derivatives.write (MARGIN_LIQUIDATE). margin-service checks its own
// policy (the leverages it allows) when the change is made.

// ErrMarginChangePending refuses a request for a target another request
// waits for (details approval_id).
var ErrMarginChangePending = apperr.New(apperr.KindConflict, "ADMIN_MARGIN_CHANGE_PENDING",
	"a request for it waits already: decide or withdraw that one first")

// ErrMarginNothingOwed refuses to liquidate an account that owes nothing.
var ErrMarginNothingOwed = apperr.New(apperr.KindConflict, "ADMIN_MARGIN_NOTHING_OWED", "the account owes nothing: there is nothing to liquidate")

// ErrMarginLiquidationOff refuses a liquidation by hand while the flag
// margin.liquidation is off: margin-service liquidates nothing then.
var ErrMarginLiquidationOff = apperr.New(apperr.KindConflict, "ADMIN_MARGIN_LIQUIDATION_OFF",
	"liquidations are switched off (margin.liquidation)")

// errMarginParamsChanged is margin-service's answer to terms whose version
// moved since they were read, given before asking it.
var errMarginParamsChanged = apperr.New(apperr.KindConflict, "MARGIN_PARAMS_CHANGED", "the terms changed since they were read")

// marginApprovalTTL: a margin request is decided within a day, while what
// it was asked against still holds.
const marginApprovalTTL = 24 * time.Hour

// marginKind reports whether an approval is margin trading's.
func marginKind(kind string) bool {
	return kind == domain.KindMarginParams || kind == domain.KindMarginLiquidate
}

// The targets of MARGIN_PARAMS: an asset's terms, a pair's isolated terms,
// the cross account's terms.
const (
	marginTargetAsset = "asset"
	marginTargetPair  = "pair"
	marginTargetCross = "cross"
)

// MarginAssetTerms are what the console sets of an asset: margin-service's
// asset_terms columns.
type MarginAssetTerms struct {
	Borrowable    bool            `json:"borrowable"`
	Collateral    bool            `json:"collateral"`
	Haircut       decimal.Decimal `json:"haircut"`
	PoolCap       decimal.Decimal `json:"pool_cap"`
	UserCap       decimal.Decimal `json:"user_cap"`
	InterestModel string          `json:"interest_model"`
	FixedRate     decimal.Decimal `json:"fixed_rate"`
	FloatBase     decimal.Decimal `json:"float_base"`
	FloatKink     decimal.Decimal `json:"float_kink"`
	FloatKinkRate decimal.Decimal `json:"float_kink_rate"`
	FloatMaxRate  decimal.Decimal `json:"float_max_rate"`
}

// MarginTerms are an account's leverage, warning and liquidation levels
// and liquidation fee: the cross account's, or a pair's isolated
// accounts'.
type MarginTerms struct {
	Leverage         int             `json:"leverage"`
	WarnLevel        decimal.Decimal `json:"warn_level"`
	LiquidationLevel decimal.Decimal `json:"liquidation_level"`
	LiquidationFee   decimal.Decimal `json:"liquidation_fee"`
}

// MarginPairTerms are a pair's isolated terms and whether it takes
// isolated accounts.
type MarginPairTerms struct {
	Isolated bool `json:"isolated"`
	MarginTerms
}

// MarginResult is a change of margin terms: applied (Value, margin-service's
// answer), or waiting for a second ADMIN (Approval).
type MarginResult struct {
	Value    json.RawMessage
	Approval *domain.Approval
}

// marginField is one field of a change: its name, whether it changes, and
// whether the change only stops new borrowing.
type marginField struct {
	name     string
	changed  bool
	stopping bool
}

// marginChange names the fields a change changes and reports whether it
// only stops new borrowing.
func marginChange(fields []marginField) (changed []string, stopping bool) {
	stopping = true
	for _, f := range fields {
		if f.changed {
			changed = append(changed, f.name)
			stopping = stopping && f.stopping
		}
	}
	return changed, stopping && len(changed) > 0
}

// assetChange compares an asset's terms: switching borrowing off and
// lowering a cap stop new borrowing; the rest (the interest, the haircut,
// collateral either way, borrowing on, a higher cap) does not.
func assetChange(cur, next MarginAssetTerms) ([]string, bool) {
	return marginChange([]marginField{
		{"borrowable", cur.Borrowable != next.Borrowable, !next.Borrowable},
		{"collateral", cur.Collateral != next.Collateral, false},
		{"haircut", !cur.Haircut.Equal(next.Haircut), false},
		{"pool_cap", !cur.PoolCap.Equal(next.PoolCap), next.PoolCap.LessThan(cur.PoolCap)},
		{"user_cap", !cur.UserCap.Equal(next.UserCap), next.UserCap.LessThan(cur.UserCap)},
		{"interest_model", cur.InterestModel != next.InterestModel, false},
		{"fixed_rate", !cur.FixedRate.Equal(next.FixedRate), false},
		{"float_base", !cur.FloatBase.Equal(next.FloatBase), false},
		{"float_kink", !cur.FloatKink.Equal(next.FloatKink), false},
		{"float_kink_rate", !cur.FloatKinkRate.Equal(next.FloatKinkRate), false},
		{"float_max_rate", !cur.FloatMaxRate.Equal(next.FloatMaxRate), false},
	})
}

// termsChange compares an account's terms: none of them stops new
// borrowing alone (a threshold moved either way liquidates or spares
// accounts at once).
func termsChange(cur, next MarginTerms) []marginField {
	return []marginField{
		{"leverage", cur.Leverage != next.Leverage, false},
		{"warn_level", !cur.WarnLevel.Equal(next.WarnLevel), false},
		{"liquidation_level", !cur.LiquidationLevel.Equal(next.LiquidationLevel), false},
		{"liquidation_fee", !cur.LiquidationFee.Equal(next.LiquidationFee), false},
	}
}

// pairChange compares a pair's terms: switching isolated accounts off
// stops new ones; anything else does not.
func pairChange(cur, next MarginPairTerms) ([]string, bool) {
	return marginChange(append([]marginField{{"isolated", cur.Isolated != next.Isolated, !next.Isolated}},
		termsChange(cur.MarginTerms, next.MarginTerms)...))
}

// decimalsAtMost refuses a value with more decimals than margin-service's
// column keeps.
func decimalsAtMost(name string, v decimal.Decimal, decimals int32) error {
	if !v.Equal(v.Truncate(decimals)) {
		return apperr.Invalid(fmt.Sprintf("%s has more than %d decimals", name, decimals))
	}
	return nil
}

// The decimals margin-service keeps of the terms (its columns).
const (
	marginRateDecimals  = 12
	marginLevelDecimals = 4
	marginFeeDecimals   = 6
	marginCapDecimals   = 18
)

// validate holds an asset's terms to asset_terms' constraints before a
// second ADMIN sees them: what margin-service would refuse when the
// request is approved is refused now.
func (t MarginAssetTerms) validate() error {
	one := decimal.NewFromInt(1)
	switch {
	case !t.Haircut.IsPositive() || t.Haircut.GreaterThan(one):
		return apperr.Invalid("the haircut is above 0 and at most 1")
	case t.PoolCap.IsNegative() || t.UserCap.IsNegative():
		return apperr.Invalid("the caps are not negative")
	case t.UserCap.GreaterThan(t.PoolCap):
		return apperr.Invalid("the user cap is at most the pool cap")
	case t.InterestModel != "FIXED" && t.InterestModel != "FLOATING":
		return apperr.Invalid("the interest model is FIXED or FLOATING")
	case t.FixedRate.IsNegative() || t.FloatBase.IsNegative():
		return apperr.Invalid("the rates are not negative")
	case !t.FloatKink.IsPositive() || !t.FloatKink.LessThan(one):
		return apperr.Invalid("the floating kink is a use strictly between 0 and 1")
	case t.FloatKinkRate.LessThan(t.FloatBase) || t.FloatMaxRate.LessThan(t.FloatKinkRate):
		return apperr.Invalid("the floating rates rise: float_base ≤ float_kink_rate ≤ float_max_rate")
	}
	for _, c := range []struct {
		name     string
		v        decimal.Decimal
		decimals int32
	}{
		{"haircut", t.Haircut, marginLevelDecimals},
		{"float_kink", t.FloatKink, marginLevelDecimals},
		{"fixed_rate", t.FixedRate, marginRateDecimals},
		{"float_base", t.FloatBase, marginRateDecimals},
		{"float_kink_rate", t.FloatKinkRate, marginRateDecimals},
		{"float_max_rate", t.FloatMaxRate, marginRateDecimals},
		{"pool_cap", t.PoolCap, marginCapDecimals},
		{"user_cap", t.UserCap, marginCapDecimals},
	} {
		if err := decimalsAtMost(c.name, c.v, c.decimals); err != nil {
			return err
		}
	}
	return nil
}

// validate holds an account's terms to the terms' rules (the leverages
// allowed are margin-service's policy, checked when the change is made).
func (t MarginTerms) validate() error {
	one := decimal.NewFromInt(1)
	switch {
	case !t.LiquidationLevel.GreaterThan(one):
		return apperr.Invalid("the liquidation level is above 1")
	case !t.WarnLevel.GreaterThan(t.LiquidationLevel):
		return apperr.Invalid("the warning level is above the liquidation level")
	case t.LiquidationFee.IsNegative() || t.LiquidationFee.GreaterThan(decimal.RequireFromString("0.1")):
		return apperr.Invalid("the liquidation fee is 0 to 0.1")
	}
	if err := decimalsAtMost("warn_level", t.WarnLevel, marginLevelDecimals); err != nil {
		return err
	}
	if err := decimalsAtMost("liquidation_level", t.LiquidationLevel, marginLevelDecimals); err != nil {
		return err
	}
	return decimalsAtMost("liquidation_fee", t.LiquidationFee, marginFeeDecimals)
}

// marginItem is one item of margin-service's lists, its fields as it
// renders them.
type marginItem = map[string]json.RawMessage

// marginList reads margin-service's {items} (and truncated).
type marginList struct {
	Items     []marginItem `json:"items"`
	Truncated *bool        `json:"truncated,omitempty"`
}

func readMarginList(raw json.RawMessage) (marginList, error) {
	var l marginList
	if err := json.Unmarshal(raw, &l); err != nil {
		return marginList{}, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "margin-service answered in another shape")
	}
	if l.Items == nil {
		l.Items = []marginItem{}
	}
	return l, nil
}

// text reads a string field of an item ("" when absent or not a string).
func text(item marginItem, key string) string {
	var s string
	_ = json.Unmarshal(item[key], &s)
	return s
}

// marginVersion reads an item's version (0 when absent).
func marginVersion(item marginItem) int64 {
	var v int64
	_ = json.Unmarshal(item["version"], &v)
	return v
}

// pendingParams maps each target to the MARGIN_PARAMS request that waits
// for it against its version now: the one a new request would be refused
// for. versions are the targets' versions now.
func (s *Service) pendingParams(ctx context.Context, versions map[string]int64) (map[string]string, error) {
	list, err := s.Store.Read().Approvals().PendingOfKind(ctx, domain.KindMarginParams)
	if err != nil {
		return nil, err
	}
	now, out := s.Now(), map[string]string{}
	for _, a := range list {
		target := a.Payload["target"]
		v, ok := versions[target]
		if ok && a.Payload["expected_version"] == strconv.FormatInt(v, 10) && !lapsedAt(a, now) {
			out[target] = a.ID
		}
	}
	return out, nil
}

// withPending adds pending_approval_id (null for none) to each item, its
// target named by targetOf.
func (s *Service) withPending(ctx context.Context, raw json.RawMessage, targetOf func(marginItem) string) (json.RawMessage, error) {
	l, err := readMarginList(raw)
	if err != nil {
		return nil, err
	}
	versions := map[string]int64{}
	for _, it := range l.Items {
		versions[targetOf(it)] = marginVersion(it)
	}
	pending, err := s.pendingParams(ctx, versions)
	if err != nil {
		return nil, err
	}
	for _, it := range l.Items {
		it["pending_approval_id"] = nullableID(pending[targetOf(it)])
	}
	return json.Marshal(l)
}

// withPendingOne adds pending_approval_id to one of margin-service's terms
// (an asset or a pair as a change answers it, the settings).
func (s *Service) withPendingOne(ctx context.Context, raw json.RawMessage, target string) (json.RawMessage, error) {
	var it marginItem
	if err := json.Unmarshal(raw, &it); err != nil {
		return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "margin-service answered in another shape")
	}
	pending, err := s.pendingParams(ctx, map[string]int64{target: marginVersion(it)})
	if err != nil {
		return nil, err
	}
	it["pending_approval_id"] = nullableID(pending[target])
	return json.Marshal(it)
}

// nullableID is an ID as JSON, null for none.
func nullableID(id string) json.RawMessage {
	if id == "" {
		return json.RawMessage("null")
	}
	out, _ := json.Marshal(id)
	return out
}

func assetTarget(asset string) string    { return marginTargetAsset + ":" + asset }
func pairTarget(symbol string) string    { return marginTargetPair + ":" + symbol }
func targetOfAsset(it marginItem) string { return assetTarget(text(it, "asset")) }
func targetOfPair(it marginItem) string  { return pairTarget(text(it, "symbol")) }

// MarginAssets returns the margin assets' terms with what is lent of each,
// and the request waiting to change each.
func (s *Service) MarginAssets(ctx context.Context, p Principal) (json.RawMessage, error) {
	if err := p.require(domain.PermInstrumentsRead); err != nil {
		return nil, err
	}
	raw, err := s.Margin.Assets(ctx)
	if err != nil {
		return nil, err
	}
	return s.withPending(ctx, raw, targetOfAsset)
}

// MarginPairs returns the pairs' isolated terms and the request waiting
// to change each.
func (s *Service) MarginPairs(ctx context.Context, p Principal) (json.RawMessage, error) {
	if err := p.require(domain.PermInstrumentsRead); err != nil {
		return nil, err
	}
	raw, err := s.Margin.Pairs(ctx)
	if err != nil {
		return nil, err
	}
	return s.withPending(ctx, raw, targetOfPair)
}

// MarginSettings returns the cross account's terms, the thresholds by
// isolated leverage and the request waiting to change them.
func (s *Service) MarginSettings(ctx context.Context, p Principal) (json.RawMessage, error) {
	if err := p.require(domain.PermInstrumentsRead); err != nil {
		return nil, err
	}
	raw, err := s.Margin.Settings(ctx)
	if err != nil {
		return nil, err
	}
	return s.withPendingOne(ctx, raw, marginTargetCross)
}

// SetMarginAsset changes an asset's terms as of expectedVersion: at once
// when it only stops new borrowing, otherwise as a request for a second
// ADMIN.
func (s *Service) SetMarginAsset(ctx context.Context, p Principal, asset string, next MarginAssetTerms, expectedVersion int64, reason string) (
	MarginResult, error,
) {
	if err := p.require(domain.PermInstrumentsTrading); err != nil {
		return MarginResult{}, err
	}
	if err := needReason(reason); err != nil {
		return MarginResult{}, err
	}
	if err := next.validate(); err != nil {
		return MarginResult{}, err
	}
	asset = strings.ToUpper(strings.TrimSpace(asset))
	cur, version, err := s.marginAsset(ctx, asset)
	if err != nil {
		return MarginResult{}, err
	}
	if version != expectedVersion {
		return MarginResult{}, errMarginParamsChanged.WithDetail("version", version)
	}
	changed, stopping := assetChange(cur, next)
	if len(changed) == 0 {
		return MarginResult{}, apperr.Invalid("nothing changes")
	}
	terms, _ := json.Marshal(next)
	if !stopping {
		return s.requestMarginParams(ctx, p, assetTarget(asset), cur, next, expectedVersion, changed, reason)
	}
	out, err := s.Margin.SetAsset(ctx, asset, terms, expectedVersion, p.Admin.Email)
	if err != nil {
		return MarginResult{}, err
	}
	if err := s.auditMarginChange(ctx, p, assetTarget(asset), "admin.margin.asset_changed", cur, next, changed, out, reason); err != nil {
		return MarginResult{}, err
	}
	out, err = s.withPendingOne(ctx, out, assetTarget(asset))
	return MarginResult{Value: out}, err
}

// SetMarginPair changes a pair's isolated terms as of expectedVersion: at
// once when it only switches isolated accounts off, otherwise as a request
// for a second ADMIN.
func (s *Service) SetMarginPair(ctx context.Context, p Principal, symbol string, next MarginPairTerms, expectedVersion int64, reason string) (
	MarginResult, error,
) {
	if err := p.require(domain.PermInstrumentsTrading); err != nil {
		return MarginResult{}, err
	}
	if err := needReason(reason); err != nil {
		return MarginResult{}, err
	}
	if err := next.validate(); err != nil {
		return MarginResult{}, err
	}
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	cur, version, err := s.marginPair(ctx, symbol)
	if err != nil {
		return MarginResult{}, err
	}
	if version != expectedVersion {
		return MarginResult{}, errMarginParamsChanged.WithDetail("version", version)
	}
	changed, stopping := pairChange(cur, next)
	if len(changed) == 0 {
		return MarginResult{}, apperr.Invalid("nothing changes")
	}
	terms, _ := json.Marshal(next)
	if !stopping {
		return s.requestMarginParams(ctx, p, pairTarget(symbol), cur, next, expectedVersion, changed, reason)
	}
	out, err := s.Margin.SetPair(ctx, symbol, terms, expectedVersion, p.Admin.Email)
	if err != nil {
		return MarginResult{}, err
	}
	if err := s.auditMarginChange(ctx, p, pairTarget(symbol), "admin.margin.pair_changed", cur, next, changed, out, reason); err != nil {
		return MarginResult{}, err
	}
	out, err = s.withPendingOne(ctx, out, pairTarget(symbol))
	return MarginResult{Value: out}, err
}

// SetMarginSettings asks a second ADMIN to change the cross account's
// terms as of expectedVersion: every change of them waits.
func (s *Service) SetMarginSettings(ctx context.Context, p Principal, next MarginTerms, expectedVersion int64, reason string) (domain.Approval, error) {
	if err := p.require(domain.PermInstrumentsTrading); err != nil {
		return domain.Approval{}, err
	}
	if err := needReason(reason); err != nil {
		return domain.Approval{}, err
	}
	if err := next.validate(); err != nil {
		return domain.Approval{}, err
	}
	cur, version, err := s.marginCross(ctx)
	if err != nil {
		return domain.Approval{}, err
	}
	if version != expectedVersion {
		return domain.Approval{}, errMarginParamsChanged.WithDetail("version", version)
	}
	changed, _ := marginChange(termsChange(cur, next))
	if len(changed) == 0 {
		return domain.Approval{}, apperr.Invalid("nothing changes")
	}
	res, err := s.requestMarginParams(ctx, p, marginTargetCross, cur, next, expectedVersion, changed, reason)
	if err != nil {
		return domain.Approval{}, err
	}
	return *res.Approval, nil
}

// marginAsset reads an asset's terms and version from margin-service.
func (s *Service) marginAsset(ctx context.Context, asset string) (MarginAssetTerms, int64, error) {
	raw, err := s.Margin.Assets(ctx)
	if err != nil {
		return MarginAssetTerms{}, 0, err
	}
	l, err := readMarginList(raw)
	if err != nil {
		return MarginAssetTerms{}, 0, err
	}
	for _, it := range l.Items {
		if text(it, "asset") == asset {
			var t MarginAssetTerms
			if err := remarshal(it, &t); err != nil {
				return MarginAssetTerms{}, 0, err
			}
			return t, marginVersion(it), nil
		}
	}
	return MarginAssetTerms{}, 0, apperr.NotFound("no such margin asset")
}

// marginPair reads a pair's isolated terms and version from margin-service.
func (s *Service) marginPair(ctx context.Context, symbol string) (MarginPairTerms, int64, error) {
	raw, err := s.Margin.Pairs(ctx)
	if err != nil {
		return MarginPairTerms{}, 0, err
	}
	l, err := readMarginList(raw)
	if err != nil {
		return MarginPairTerms{}, 0, err
	}
	for _, it := range l.Items {
		if text(it, "symbol") == symbol {
			var t MarginPairTerms
			if err := remarshal(it, &t); err != nil {
				return MarginPairTerms{}, 0, err
			}
			return t, marginVersion(it), nil
		}
	}
	return MarginPairTerms{}, 0, apperr.NotFound("no such margin pair")
}

// marginCross reads the cross account's terms and version from
// margin-service.
func (s *Service) marginCross(ctx context.Context) (MarginTerms, int64, error) {
	raw, err := s.Margin.Settings(ctx)
	if err != nil {
		return MarginTerms{}, 0, err
	}
	var set struct {
		Cross   MarginTerms `json:"cross"`
		Version int64       `json:"version"`
	}
	if err := json.Unmarshal(raw, &set); err != nil {
		return MarginTerms{}, 0, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "margin-service answered in another shape")
	}
	return set.Cross, set.Version, nil
}

// remarshal reads an item of margin-service's into the console's terms.
func remarshal(it marginItem, to any) error {
	raw, _ := json.Marshal(it)
	if err := json.Unmarshal(raw, to); err != nil {
		return apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "margin-service answered the terms in another shape")
	}
	return nil
}

// auditMarginChange records a change applied at once, with the fields
// before and after.
func (s *Service) auditMarginChange(ctx context.Context, p Principal, target, action string, before, after any, changed []string, out json.RawMessage,
	reason string,
) error {
	details, _ := json.Marshal(map[string]any{"old": before, "new": after, "changed": changed, "version": versionOf(out)})
	return s.audit(ctx, p, "margin:"+target, action, strings.TrimSpace(reason), string(details))
}

// requestMarginParams keeps a change of margin terms as a request for a
// second ADMIN (MARGIN_PARAMS): the target, the terms asked for and those
// they replace, the version read and the fields that change. One request
// a target waits at a time, whoever asked: a request asked against an
// older version, or lapsed, stands in no one's way (it can only fail).
func (s *Service) requestMarginParams(ctx context.Context, p Principal, target string, before, after any, version int64, changed []string,
	reason string,
) (MarginResult, error) {
	terms, _ := json.Marshal(after)
	previous, _ := json.Marshal(before)
	a := domain.Approval{
		ID: uuid.Must(uuid.NewV7()).String(), Kind: domain.KindMarginParams, Reason: strings.TrimSpace(reason), Status: domain.ApprovalPending,
		Payload: map[string]string{
			"target": target, "terms": string(terms), "previous": string(previous), "expected_version": strconv.FormatInt(version, 10),
			"changed": strings.Join(changed, ","), "actor": p.Admin.Email,
		},
		RequestedBy: p.Admin.ID, RequestedByEmail: p.Admin.Email, CreatedAt: s.Now(), Mode: domain.ModeTwoPerson,
		Escalation: domain.EscalationMarginRisk,
	}
	err := s.insertMarginRequest(ctx, p, a, "margin:"+target, func(o domain.Approval) bool {
		return o.Payload["target"] == target && o.Payload["expected_version"] == a.Payload["expected_version"]
	})
	if err != nil {
		return MarginResult{}, err
	}
	return MarginResult{Approval: &a}, nil
}

// insertMarginRequest records a margin request unless another waits for
// the same (same): the requests of its kind on lock are taken one at a
// time, so two sent at once see each other.
func (s *Service) insertMarginRequest(ctx context.Context, p Principal, a domain.Approval, lock string, same func(domain.Approval) bool) error {
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.Approvals().LockRequests(ctx, a.Kind, lock); err != nil {
			return err
		}
		pending, err := r.Approvals().PendingOfKind(ctx, a.Kind)
		if err != nil {
			return err
		}
		for _, o := range pending {
			if same(o) && !lapsedAt(o, a.CreatedAt) {
				return ErrMarginChangePending.WithDetail("approval_id", o.ID)
			}
		}
		if err := r.Approvals().Insert(ctx, a); err != nil {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: fundTarget(a), Action: fundActions[a.Kind].requested, Actor: p.Admin.Email, Reason: a.Reason, Details: fundDetails(a),
		}, p.Admin.Email)
	})
}

// executeMarginParams sets the terms an approved MARGIN_PARAMS asks for, as
// of the version it was asked against, in its requester's name (the
// terms' updated_by; the approver is in the request and the audit trail).
// A change since then refuses it (MARGIN_PARAMS_CHANGED) and the request
// fails, unless that change is this request's own earlier attempt whose
// answer was lost.
func (s *Service) executeMarginParams(ctx context.Context, a domain.Approval, _ Principal) (string, error) {
	version, err := strconv.ParseInt(a.Payload["expected_version"], 10, 64)
	if err != nil {
		return "", err
	}
	target, actor, terms := a.Payload["target"], a.Payload["actor"], json.RawMessage(a.Payload["terms"])
	kind, key, _ := strings.Cut(target, ":")
	var out json.RawMessage
	switch kind {
	case marginTargetAsset:
		out, err = s.Margin.SetAsset(ctx, key, terms, version, actor)
	case marginTargetPair:
		out, err = s.Margin.SetPair(ctx, key, terms, version, actor)
	case marginTargetCross:
		out, err = s.Margin.SetCross(ctx, terms, version, actor)
	default:
		return "", fmt.Errorf("approval %s: no margin target %q", a.ID, target)
	}
	if apperr.Is(err, "MARGIN_PARAMS_CHANGED") {
		// An earlier attempt whose answer was lost may have set them: the
		// terms at the next version, as asked, set by this request's
		// actor, are that attempt. When they cannot be read again the
		// outcome is unknown and the request stays pending.
		done, rerr := s.marginSetAlready(ctx, kind, key, terms, version+1, actor)
		if rerr != nil {
			return "", apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "the margin terms changed and cannot be read again: try again")
		}
		if done {
			return fmt.Sprintf("%s version %d (set by an earlier attempt)", target, version+1), nil
		}
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s version %d", target, versionOf(out)), nil
}

// marginSetAlready reports whether margin-service holds the terms asked
// for at version, set by actor.
func (s *Service) marginSetAlready(ctx context.Context, kind, key string, terms json.RawMessage, version int64, actor string) (bool, error) {
	// item is the target as margin-service lists it; same compares its terms with those asked for.
	var (
		item marginItem
		same func(marginItem) (bool, error)
	)
	switch kind {
	case marginTargetCross:
		raw, err := s.Margin.Settings(ctx)
		if err != nil {
			return false, err
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return false, err
		}
		same = func(it marginItem) (bool, error) {
			var cur, asked MarginTerms
			if json.Unmarshal(it["cross"], &cur) != nil || json.Unmarshal(terms, &asked) != nil {
				return false, fmt.Errorf("the cross terms came in another shape")
			}
			changed, _ := marginChange(termsChange(cur, asked))
			return len(changed) == 0, nil
		}
	case marginTargetAsset, marginTargetPair:
		list, field := s.Margin.Assets, "asset"
		same = func(it marginItem) (bool, error) {
			var cur, asked MarginAssetTerms
			if remarshal(it, &cur) != nil || json.Unmarshal(terms, &asked) != nil {
				return false, fmt.Errorf("the asset terms came in another shape")
			}
			changed, _ := assetChange(cur, asked)
			return len(changed) == 0, nil
		}
		if kind == marginTargetPair {
			list, field = s.Margin.Pairs, "symbol"
			same = func(it marginItem) (bool, error) {
				var cur, asked MarginPairTerms
				if remarshal(it, &cur) != nil || json.Unmarshal(terms, &asked) != nil {
					return false, fmt.Errorf("the pair terms came in another shape")
				}
				changed, _ := pairChange(cur, asked)
				return len(changed) == 0, nil
			}
		}
		raw, err := list(ctx)
		if err != nil {
			return false, err
		}
		l, err := readMarginList(raw)
		if err != nil {
			return false, err
		}
		i := slices.IndexFunc(l.Items, func(it marginItem) bool { return text(it, field) == key })
		if i < 0 {
			return false, nil
		}
		item = l.Items[i]
	default:
		return false, nil
	}
	if marginVersion(item) != version || text(item, "updated_by") != actor {
		return false, nil
	}
	return same(item)
}

// marginAccountKey checks a margin account's key: MARGIN_CROSS or
// MARGIN_ISOLATED:<symbol>, the symbol in capitals.
func marginAccountKey(userID, account string) (string, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return "", apperr.Invalid("user_id must be a UUID")
	}
	kind, symbol, isolated := strings.Cut(strings.TrimSpace(account), ":")
	switch {
	case kind == "MARGIN_CROSS" && !isolated:
		return kind, nil
	case kind == "MARGIN_ISOLATED" && isolated && symbol != "":
		return kind + ":" + strings.ToUpper(symbol), nil
	}
	return "", apperr.Invalid("account is MARGIN_CROSS or MARGIN_ISOLATED:<symbol>")
}

// MarginAccountQuery selects margin accounts for the console.
type MarginAccountQuery = ports.MarginAccountQuery

// normalizeAccount gives an account's frozen_reason as "" when no
// administrator froze it (margin-service's column is NOT NULL DEFAULT ”).
func normalizeAccount(it marginItem) {
	if r, ok := it["frozen_reason"]; !ok || string(r) == "null" {
		it["frozen_reason"] = json.RawMessage(`""`)
	}
}

// pendingLiquidations maps each account (user_id + " " + account) to the
// MARGIN_LIQUIDATE request that waits for it.
func (s *Service) pendingLiquidations(ctx context.Context) (map[string]string, error) {
	list, err := s.Store.Read().Approvals().PendingOfKind(ctx, domain.KindMarginLiquidate)
	if err != nil {
		return nil, err
	}
	now, out := s.Now(), map[string]string{}
	for _, a := range list {
		if !lapsedAt(a, now) {
			out[a.Payload["user_id"]+" "+a.Payload["account"]] = a.ID
		}
	}
	return out, nil
}

// accountKeyOf is an item's account as a MARGIN_LIQUIDATE names it.
func accountKeyOf(it marginItem) string {
	account := text(it, "account")
	if symbol := text(it, "symbol"); account == "MARGIN_ISOLATED" && symbol != "" {
		account += ":" + symbol
	}
	return text(it, "user_id") + " " + account
}

// MarginAccounts returns the margin accounts q selects, riskiest first,
// each with the liquidation by hand that waits for it.
func (s *Service) MarginAccounts(ctx context.Context, p Principal, q MarginAccountQuery) (json.RawMessage, error) {
	if err := p.require(domain.PermDerivativesRead); err != nil {
		return nil, err
	}
	if q.UserID != "" {
		if _, err := uuid.Parse(q.UserID); err != nil {
			return nil, apperr.Invalid("user_id must be a UUID")
		}
	}
	q.Symbol = strings.ToUpper(strings.TrimSpace(q.Symbol))
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 500
	}
	raw, err := s.Margin.Accounts(ctx, q)
	if err != nil {
		return nil, err
	}
	l, err := readMarginList(raw)
	if err != nil {
		return nil, err
	}
	pending, err := s.pendingLiquidations(ctx)
	if err != nil {
		return nil, err
	}
	for _, it := range l.Items {
		normalizeAccount(it)
		it["pending_approval_id"] = nullableID(pending[accountKeyOf(it)])
	}
	if l.Truncated == nil {
		f := false
		l.Truncated = &f
	}
	return json.Marshal(l)
}

// MarginAccount returns one margin account in full, with the liquidation
// by hand that waits for it.
func (s *Service) MarginAccount(ctx context.Context, p Principal, userID, account string) (json.RawMessage, error) {
	if err := p.require(domain.PermDerivativesRead); err != nil {
		return nil, err
	}
	account, err := marginAccountKey(userID, account)
	if err != nil {
		return nil, err
	}
	raw, err := s.Margin.Account(ctx, userID, account)
	if err != nil {
		return nil, err
	}
	it, err := s.accountView(ctx, raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(it)
}

// accountView reads an account as margin-service answers it, its
// frozen_reason "" unless an administrator froze it, with the liquidation
// by hand that waits for it.
func (s *Service) accountView(ctx context.Context, raw json.RawMessage) (marginItem, error) {
	var it marginItem
	if err := json.Unmarshal(raw, &it); err != nil {
		return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "margin-service answered in another shape")
	}
	pending, err := s.pendingLiquidations(ctx)
	if err != nil {
		return nil, err
	}
	normalizeAccount(it)
	it["pending_approval_id"] = nullableID(pending[accountKeyOf(it)])
	if raw, ok := it["liquidations"]; ok {
		var list []marginItem
		if json.Unmarshal(raw, &list) == nil {
			for _, l := range list {
				// margin-service renders a liquidation without a level as "".
				if string(l["margin_level"]) == `""` {
					l["margin_level"] = json.RawMessage("null")
				}
			}
			it["liquidations"], _ = json.Marshal(list)
		}
	}
	return it, nil
}

// maxFreezeReason is the longest reason margin-service keeps with a freeze.
const maxFreezeReason = 500

// FreezeMarginAccount freezes a margin account at once: no orders,
// borrowing or transfers out until it is unfrozen; margin-service cancels
// its open orders (E3; a cancel that fails is logged and the freeze
// stands).
func (s *Service) FreezeMarginAccount(ctx context.Context, p Principal, userID, account, reason string) (json.RawMessage, error) {
	if err := p.require(domain.PermDerivativesEdit); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	if len(reason) > maxFreezeReason {
		return nil, apperr.Invalid(fmt.Sprintf("a reason of at most %d bytes", maxFreezeReason))
	}
	account, err := marginAccountKey(userID, account)
	if err != nil {
		return nil, err
	}
	out, err := s.Margin.Freeze(ctx, userID, account, p.Admin.Email, reason)
	again := false
	if apperr.Is(err, "MARGIN_FROZEN") {
		// An earlier attempt whose answer was lost froze it: frozen by this
		// administrator for this reason.
		if raw, rerr := s.Margin.Account(ctx, userID, account); rerr == nil {
			var it marginItem
			if json.Unmarshal(raw, &it) == nil && text(it, "frozen_by") == p.Admin.Email && text(it, "frozen_reason") == reason {
				out, err, again = raw, nil, true
			}
		}
	}
	if err != nil {
		return nil, err
	}
	it, err := s.accountView(ctx, out)
	if err != nil {
		return nil, err
	}
	details, _ := json.Marshal(map[string]any{"account": account, "frozen_at": it["frozen_at"], "earlier_attempt": again})
	if err := s.audit(ctx, p, "user:"+userID, "admin.margin.account_frozen", reason, string(details)); err != nil {
		return nil, err
	}
	return json.Marshal(it)
}

// UnfreezeMarginAccount lifts an administrator's freeze at once.
func (s *Service) UnfreezeMarginAccount(ctx context.Context, p Principal, userID, account, reason string) (json.RawMessage, error) {
	if err := p.require(domain.PermDerivativesEdit); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	account, err := marginAccountKey(userID, account)
	if err != nil {
		return nil, err
	}
	out, err := s.Margin.Unfreeze(ctx, userID, account, p.Admin.Email)
	if err != nil {
		return nil, err
	}
	it, err := s.accountView(ctx, out)
	if err != nil {
		return nil, err
	}
	details, _ := json.Marshal(map[string]any{"account": account})
	if err := s.audit(ctx, p, "user:"+userID, "admin.margin.account_unfrozen", strings.TrimSpace(reason), string(details)); err != nil {
		return nil, err
	}
	return json.Marshal(it)
}

// marginLiquidationOn reports whether margin-service liquidates a user's
// accounts (the flag margin.liquidation for the user, as its monitor reads
// it; nil Features leave it off).
func (s *Service) marginLiquidationOn(userID string) bool {
	return s.Features != nil && s.Features.Enabled(flags.KeyMarginLiquidation, flags.Subject{UserID: userID})
}

// scopeMarginLiquidate is the Idempotency-Key scope of a liquidation by
// hand.
const scopeMarginLiquidate = "margin.liquidate"

// LiquidateMarginAccount asks a second administrator with derivatives.write
// to liquidate a margin account, whatever its margin level, in either
// approval mode (MARGIN_LIQUIDATE). The account as it stands is kept with
// the request; one request an account waits at a time. The same request
// with the same key returns its request.
func (s *Service) LiquidateMarginAccount(ctx context.Context, p Principal, userID, account, reason, key string) (domain.Approval, error) {
	if err := p.require(domain.PermDerivativesEdit); err != nil {
		return domain.Approval{}, err
	}
	if err := needReason(reason); err != nil {
		return domain.Approval{}, err
	}
	account, err := marginAccountKey(userID, account)
	if err != nil {
		return domain.Approval{}, err
	}
	reason = strings.TrimSpace(reason)
	c, err := s.claimKey(ctx, p, key, scopeMarginLiquidate, fingerprint(userID, account, reason))
	if err != nil {
		return domain.Approval{}, err
	}
	if !c.Fresh {
		// Made already, unless the first request stopped before it was recorded.
		if a, err := s.Store.Read().Approvals().Get(ctx, c.Ref); err != nil || a != nil {
			if err != nil {
				return domain.Approval{}, err
			}
			return *a, nil
		}
	}
	if !s.marginLiquidationOn(userID) {
		return domain.Approval{}, ErrMarginLiquidationOff
	}
	raw, err := s.Margin.Account(ctx, userID, account)
	if err != nil {
		return domain.Approval{}, err
	}
	var acct struct {
		Status         string          `json:"status"`
		MarginLevel    *string         `json:"margin_level"`
		TotalAsset     decimal.Decimal `json:"total_asset"`
		TotalLiability decimal.Decimal `json:"total_liability"`
	}
	if err := json.Unmarshal(raw, &acct); err != nil {
		return domain.Approval{}, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "margin-service answered in another shape")
	}
	if acct.Status == "LIQUIDATING" {
		return domain.Approval{}, apperr.New(apperr.KindConflict, "MARGIN_FROZEN", "a liquidation of the account is under way").
			WithDetail("status", acct.Status)
	}
	if !acct.TotalLiability.IsPositive() {
		return domain.Approval{}, ErrMarginNothingOwed
	}
	level := ""
	if acct.MarginLevel != nil {
		level = *acct.MarginLevel
	}
	value := acct.TotalLiability.Round(2)
	a := domain.Approval{
		ID: c.Ref, Kind: domain.KindMarginLiquidate, Reason: reason, Status: domain.ApprovalPending,
		Payload: map[string]string{
			"user_id": userID, "account": account, "status": acct.Status, "margin_level": level, "total_asset": acct.TotalAsset.String(),
			"total_liability": acct.TotalLiability.String(), "actor": p.Admin.Email,
		},
		RequestedBy: p.Admin.ID, RequestedByEmail: p.Admin.Email, CreatedAt: s.Now(), Mode: domain.ModeTwoPerson,
		Escalation: domain.EscalationMarginRisk, ValueUSDT: &value,
	}
	err = s.insertMarginRequest(ctx, p, a, "account:"+userID+" "+account, func(o domain.Approval) bool {
		return o.Payload["user_id"] == userID && o.Payload["account"] == account
	})
	if _, dup := pg.UniqueViolation(err); dup {
		// The same request, concurrently, recorded it first.
		if cur, gerr := s.Store.Read().Approvals().Get(ctx, a.ID); gerr == nil && cur != nil {
			return *cur, nil
		}
	}
	return a, err
}

// executeMarginLiquidate starts the liquidation an approved
// MARGIN_LIQUIDATE asks for, in its requester's name: margin-service
// liquidates once per approval, so a second attempt finds the first's
// liquidation.
func (s *Service) executeMarginLiquidate(ctx context.Context, a domain.Approval, _ Principal) (string, error) {
	if !s.marginLiquidationOn(a.Payload["user_id"]) {
		return "", ErrMarginLiquidationOff
	}
	out, err := s.Margin.Liquidate(ctx, a.Payload["user_id"], a.Payload["account"], a.ID, a.Payload["actor"])
	if err != nil {
		return "", err
	}
	var l struct {
		ID string `json:"liquidation_id"`
	}
	if json.Unmarshal(out, &l) != nil || l.ID == "" {
		return "liquidation started", nil
	}
	return "liquidation " + l.ID, nil
}

// MarginLiquidationQuery selects margin liquidations for the console.
type MarginLiquidationQuery = ports.MarginLiquidationQuery

// MarginLiquidations returns a page of margin liquidations from the read
// model, newest first.
func (s *Service) MarginLiquidations(ctx context.Context, p Principal, q MarginLiquidationQuery) ([]ports.MarginLiquidation, string, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return nil, "", err
	}
	if q.UserID != "" {
		if _, err := uuid.Parse(q.UserID); err != nil {
			return nil, "", apperr.Invalid("user_id must be a UUID")
		}
	}
	switch q.Account {
	case "", "MARGIN_CROSS", "MARGIN_ISOLATED":
	default:
		return nil, "", apperr.Invalid("account must be MARGIN_CROSS or MARGIN_ISOLATED")
	}
	q.Trigger = strings.ToUpper(strings.TrimSpace(q.Trigger))
	switch q.Trigger {
	case "", "AUTO", "MANUAL":
	default:
		return nil, "", apperr.Invalid("trigger must be AUTO or MANUAL")
	}
	q.Symbol = strings.ToUpper(strings.TrimSpace(q.Symbol))
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	q.Days = reportDays(q.Days)
	return s.MarginReports.MarginLiquidations(ctx, q)
}

// MarginInterest returns the interest per bucket and asset of the period
// (one asset unless empty), oldest first.
func (s *Service) MarginInterest(ctx context.Context, p Principal, q ReportQuery, asset string) ([]ports.MarginInterestBucket, error) {
	rng, err := s.reportRange(p, q)
	if err != nil {
		return nil, err
	}
	return s.MarginReports.MarginInterest(ctx, rng, strings.ToUpper(strings.TrimSpace(asset)))
}
