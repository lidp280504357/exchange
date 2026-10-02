package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// The simulated market of the platform coin from the console (ASTRA design
// §6, C5): its state, price events and settings. market-sim keeps the
// guards (one operator's share of price moves in any hour, hard caps,
// docs/runbook/market-sim.md); what goes beyond the share waits here for a
// second administrator with sim.control, whose approval market-sim then
// receives as approved_by over the console's signed key: both names come
// from the sessions, never from the browser.

// Event types market-sim runs.
var simEventTypes = map[string]bool{
	"JUMP": true, "TARGET": true, "TREND": true, "VOLATILITY": true, "PAUSE": true, "HALT": true, "REANCHOR": true,
}

// The guards' refusals that a second administrator lifts.
const (
	simEventNeedsApproval  = "SIM_EVENT_NEEDS_APPROVAL"
	simParamsNeedApproval  = "SIM_PARAMS_NEED_APPROVAL"
	simAuditTarget         = "sim"
	simHistoryMaxMinutes   = 24 * 60
	simEventsDefaultLimit  = 50
	simEventsMaxLimit      = 200
	simDefaultHistoryRange = 24 * 60
)

// SimEventInput is a price event as the console asks for it (market-sim's
// fields; the administrators are added here).
type SimEventInput struct {
	Type            string   `json:"type"`
	Size            *float64 `json:"size,omitempty"`
	Price           string   `json:"price,omitempty"`
	Mu              *float64 `json:"mu,omitempty"`
	Factor          *float64 `json:"factor,omitempty"`
	DurationSeconds int      `json:"duration_seconds,omitempty"`
	HoldSeconds     int      `json:"hold_seconds,omitempty"`
	StartsAt        string   `json:"starts_at,omitempty"`
}

// SimResult is a change done (Event, or Version for the settings) or one
// waiting for a second administrator (Approval).
type SimResult struct {
	Event    json.RawMessage  `json:"event,omitempty"`
	Version  *int64           `json:"version,omitempty"`
	Approval *domain.Approval `json:"-"`
}

// SimStatus returns the simulated market's state.
func (s *Service) SimStatus(ctx context.Context, p Principal) (json.RawMessage, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return nil, err
	}
	return s.Sim.Status(ctx)
}

// SimHistory returns the target and last price over the last minutes
// (default and at most a day).
func (s *Service) SimHistory(ctx context.Context, p Principal, minutes int) (json.RawMessage, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return nil, err
	}
	if minutes <= 0 || minutes > simHistoryMaxMinutes {
		minutes = simDefaultHistoryRange
	}
	return s.Sim.History(ctx, minutes)
}

// SimEvents lists the running and scheduled events, or with all the
// latest of every status (default 50, at most 200).
func (s *Service) SimEvents(ctx context.Context, p Principal, all bool, limit int) (json.RawMessage, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > simEventsMaxLimit {
		limit = simEventsDefaultLimit
	}
	return s.Sim.Events(ctx, all, limit)
}

// SimImpact measures the simulated market's perpetual contract at a mark
// price (ASTRA design §6.3): derivatives-service's price impact (the
// positions it would liquidate, their accounts, what the insurance fund
// would bear) with the longs and shorts open now, null when the positions
// cannot all be read.
func (s *Service) SimImpact(ctx context.Context, p Principal, price string) (json.RawMessage, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return nil, err
	}
	raw, err := s.Sim.Status(ctx)
	if err != nil {
		return nil, err
	}
	var st struct {
		Perp string `json:"perp"`
	}
	if err := json.Unmarshal(raw, &st); err != nil || st.Perp == "" {
		return nil, apperr.New(apperr.KindUnprocessable, "ADMIN_SIM_NO_CONTRACT", "the simulated market has no perpetual contract")
	}
	impact, err := s.Derivatives.PriceImpact(ctx, st.Perp, strings.TrimSpace(price))
	if err != nil {
		return nil, err
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(impact, &out); err != nil {
		return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "derivatives-service answered badly")
	}
	out["longs"], out["shorts"] = s.simSides(ctx, st.Perp)
	return json.Marshal(out)
}

// simPositionsMax bounds the positions read to count the sides.
const simPositionsMax = 1000

// simSides counts the contract's open longs and shorts, HOUSE apart (it is
// left out of the impact too): JSON numbers, or null when the positions
// cannot all be read.
func (s *Service) simSides(ctx context.Context, symbol string) (longs, shorts json.RawMessage) {
	unknown := json.RawMessage("null")
	raw, err := s.Derivatives.OpenPositions(ctx, ports.PositionQuery{Symbol: symbol, Limit: simPositionsMax})
	if err != nil {
		s.Log.WarnContext(ctx, "sim impact: the positions are unknown", "error", err)
		return unknown, unknown
	}
	var body struct {
		Positions []struct {
			UserID   string `json:"user_id"`
			Quantity string `json:"quantity"`
		} `json:"positions"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.Truncated {
		return unknown, unknown
	}
	l, sh := 0, 0
	for _, p := range body.Positions {
		switch {
		case s.HouseBook.User != "" && p.UserID == s.HouseBook.User:
		case strings.HasPrefix(p.Quantity, "-"):
			sh++
		default:
			l++
		}
	}
	return json.RawMessage(strconv.Itoa(l)), json.RawMessage(strconv.Itoa(sh))
}

// CreateSimEvent creates a price event; one beyond the operator's share
// becomes a request for a second administrator (SimResult.Approval).
func (s *Service) CreateSimEvent(ctx context.Context, p Principal, in SimEventInput, reason string) (SimResult, error) {
	if err := p.require(domain.PermSimControl); err != nil {
		return SimResult{}, err
	}
	if err := needReason(reason); err != nil {
		return SimResult{}, err
	}
	in.Type = strings.ToUpper(strings.TrimSpace(in.Type))
	if !simEventTypes[in.Type] {
		return SimResult{}, apperr.Invalid("type must be JUMP, TARGET, TREND, VOLATILITY, PAUSE, HALT or REANCHOR")
	}
	if in.StartsAt != "" {
		if _, err := time.Parse(time.RFC3339, in.StartsAt); err != nil {
			return SimResult{}, apperr.Invalid("starts_at must be an RFC 3339 time")
		}
	}
	event := simEventFields(in, strings.TrimSpace(reason))
	raw, err := s.Sim.CreateEvent(ctx, event, p.Admin.Email, "")
	if apperr.Is(err, simEventNeedsApproval) {
		return s.requestSim(ctx, p, domain.KindSimEvent, event, err, reason)
	}
	if err != nil {
		return SimResult{}, err
	}
	details, _ := json.Marshal(map[string]any{"event": raw})
	return SimResult{Event: raw}, s.audit(ctx, p, simAuditTarget, "admin.sim.event_created", strings.TrimSpace(reason), string(details))
}

// simEventFields is an event as market-sim takes it, with the reason.
func simEventFields(in SimEventInput, reason string) map[string]any {
	out := map[string]any{"type": in.Type, "reason": reason}
	if in.Size != nil {
		out["size"] = *in.Size
	}
	if in.Price != "" {
		out["price"] = strings.TrimSpace(in.Price)
	}
	if in.Mu != nil {
		out["mu"] = *in.Mu
	}
	if in.Factor != nil {
		out["factor"] = *in.Factor
	}
	if in.DurationSeconds > 0 {
		out["duration_seconds"] = in.DurationSeconds
	}
	if in.HoldSeconds > 0 {
		out["hold_seconds"] = in.HoldSeconds
	}
	if in.StartsAt != "" {
		out["starts_at"] = in.StartsAt
	}
	return out
}

// EndSimEvent cancels a scheduled event or ends a running one.
func (s *Service) EndSimEvent(ctx context.Context, p Principal, id, reason string) (json.RawMessage, error) {
	if err := p.require(domain.PermSimControl); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, apperr.NotFound("no such event")
	}
	raw, err := s.Sim.EndEvent(ctx, id, p.Admin.Email, strings.TrimSpace(reason))
	if err != nil {
		return nil, err
	}
	details, _ := json.Marshal(map[string]any{"event": raw})
	return raw, s.audit(ctx, p, simAuditTarget, "admin.sim.event_ended", strings.TrimSpace(reason), string(details))
}

// UpdateSimParams replaces the simulated market's settings (every field);
// a change beyond the operator's share becomes a request for a second
// administrator.
func (s *Service) UpdateSimParams(ctx context.Context, p Principal, params json.RawMessage, reason string) (SimResult, error) {
	if err := p.require(domain.PermSimControl); err != nil {
		return SimResult{}, err
	}
	if err := needReason(reason); err != nil {
		return SimResult{}, err
	}
	var fields map[string]json.RawMessage
	if len(params) == 0 || json.Unmarshal(params, &fields) != nil || len(fields) == 0 {
		return SimResult{}, apperr.Invalid("params must be the settings as an object")
	}
	raw, err := s.Sim.UpdateParams(ctx, params, p.Admin.Email, "")
	if apperr.Is(err, simParamsNeedApproval) {
		return s.requestSim(ctx, p, domain.KindSimParams, map[string]any{"params": params}, err, reason)
	}
	if err != nil {
		return SimResult{}, err
	}
	var v struct {
		Version int64 `json:"version"`
	}
	_ = json.Unmarshal(raw, &v)
	details, _ := json.Marshal(map[string]any{"version": v.Version, "params": params})
	return SimResult{Version: &v.Version}, s.audit(ctx, p, simAuditTarget, "admin.sim.params_changed", strings.TrimSpace(reason), string(details))
}

// requestSim keeps a change market-sim refused for want of a second
// administrator as a request for one, with the move it measured.
func (s *Service) requestSim(ctx context.Context, p Principal, kind string, change map[string]any, refusal error, reason string) (SimResult, error) {
	body, err := json.Marshal(change)
	if err != nil {
		return SimResult{}, err
	}
	payload := map[string]string{"change": string(body), "actor": p.Admin.Email}
	if e := apperr.From(refusal); e != nil {
		for _, k := range []string{"move", "volume"} {
			if v, ok := e.Details[k]; ok {
				payload[k] = fmt.Sprint(v)
			}
		}
	}
	a := domain.Approval{
		ID: uuid.Must(uuid.NewV7()).String(), Kind: kind, Payload: payload, Reason: strings.TrimSpace(reason),
		Status: domain.ApprovalPending, RequestedBy: p.Admin.ID, CreatedAt: s.Now(), Mode: domain.ModeTwoPerson,
		Escalation: domain.EscalationSimShare, RequestedByEmail: p.Admin.Email,
	}
	actions := fundActions[kind]
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.Approvals().Insert(ctx, a); err != nil {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: fundTarget(a), Action: actions.requested, Actor: p.Admin.Email, Reason: a.Reason, Details: fundDetails(a),
		}, p.Admin.Email)
	})
	if err != nil {
		return SimResult{}, err
	}
	return SimResult{Approval: &a}, nil
}

// simApprovalTTL is how long a simulated market's change waits for its
// second administrator (C5.5 ④).
const simApprovalTTL = 24 * time.Hour

// simExpiry is when a simulated market's request lapses: a day after it
// was asked for, or when its event was to start if sooner. Approving it
// later fails it, nothing sent to market-sim.
func simExpiry(a domain.Approval) time.Time {
	at := a.CreatedAt.Add(simApprovalTTL)
	if a.Kind != domain.KindSimEvent {
		return at
	}
	var e struct {
		StartsAt string `json:"starts_at"`
	}
	if json.Unmarshal([]byte(a.Payload["change"]), &e) == nil && e.StartsAt != "" {
		if t, err := time.Parse(time.RFC3339, e.StartsAt); err == nil && t.Before(at) {
			at = t
		}
	}
	return at
}

// SimPreview is a simulated market's request measured now, for the
// administrator who decides it (C5.5 ④): when it lapses, the target now,
// where the change would take the price (an event's jump or target, the
// settings' anchor; nil when it moves no price directly), that move now
// and the one market-sim measured when it was asked for, and what the
// price would do to the perpetual.
type SimPreview struct {
	ExpiresAt     time.Time
	Expired       bool
	Target        *decimal.Decimal
	Expected      *decimal.Decimal
	Move          *float64
	RequestedMove string
	Impact        json.RawMessage
}

// SimApprovalPreview measures a pending simulated market's request now.
func (s *Service) SimApprovalPreview(ctx context.Context, p Principal, id string) (SimPreview, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return SimPreview{}, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return SimPreview{}, apperr.NotFound("no such request")
	}
	a, err := s.Store.Read().Approvals().Get(ctx, id)
	if err != nil {
		return SimPreview{}, err
	}
	if a == nil || !simKind(a.Kind) {
		return SimPreview{}, apperr.NotFound("no such simulated market request")
	}
	out := SimPreview{ExpiresAt: simExpiry(*a), RequestedMove: a.Payload["move"]}
	out.Expired = !s.Now().Before(out.ExpiresAt)
	raw, err := s.Sim.Status(ctx)
	if err != nil {
		return SimPreview{}, err
	}
	var st struct {
		Perp   string             `json:"perp"`
		Target *string            `json:"target_price"`
		Params map[string]float64 `json:"params"`
	}
	if err := json.Unmarshal(raw, &st); err != nil || st.Target == nil {
		return SimPreview{}, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "market-sim answered without its target")
	}
	target, err := decimal.NewFromString(*st.Target)
	if err != nil || !target.IsPositive() {
		return SimPreview{}, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "market-sim answered without its target")
	}
	out.Target = &target
	expected := simExpected(*a, target, st.Params["p0"])
	if expected == nil {
		return out, nil
	}
	out.Expected = expected
	move, _ := expected.Div(target).Sub(decimal.NewFromInt(1)).Round(4).Float64()
	out.Move = &move
	if st.Perp != "" {
		if out.Impact, err = s.SimImpact(ctx, p, expected.Round(8).String()); err != nil {
			s.Log.WarnContext(ctx, "sim preview: no impact", "approval_id", id, "error", err)
			out.Impact = nil
		}
	}
	return out, nil
}

// simExpected is where a request would take the price from target: a
// jump's, a target event's price, the settings' new anchor (the target
// moves with P0); nil for changes that move no price directly.
func simExpected(a domain.Approval, target decimal.Decimal, p0 float64) *decimal.Decimal {
	var change struct {
		Type   string          `json:"type"`
		Size   float64         `json:"size"`
		Price  string          `json:"price"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal([]byte(a.Payload["change"]), &change) != nil {
		return nil
	}
	var out decimal.Decimal
	switch {
	case a.Kind == domain.KindSimEvent && change.Type == "JUMP":
		out = target.Mul(decimal.NewFromFloat(1 + change.Size))
	case a.Kind == domain.KindSimEvent && change.Type == "TARGET":
		px, err := decimal.NewFromString(change.Price)
		if err != nil {
			return nil
		}
		out = px
	case a.Kind == domain.KindSimParams && p0 > 0:
		var params struct {
			P0 float64 `json:"p0"`
		}
		if json.Unmarshal(change.Params, &params) != nil || params.P0 <= 0 || params.P0 == p0 {
			return nil
		}
		out = target.Mul(decimal.NewFromFloat(params.P0 / p0))
	default:
		return nil
	}
	if !out.IsPositive() {
		return nil
	}
	return &out
}

// executeSim carries out an approved simulated market change in the
// requester's name with the decider as approver.
func (s *Service) executeSim(ctx context.Context, a domain.Approval, p Principal) (string, error) {
	switch a.Kind {
	case domain.KindSimEvent:
		var event map[string]any
		if err := json.Unmarshal([]byte(a.Payload["change"]), &event); err != nil {
			return "", err
		}
		raw, err := s.Sim.CreateEvent(ctx, event, a.Payload["actor"], p.Admin.Email)
		if err != nil {
			return "", err
		}
		var e struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &e)
		return "event " + e.ID, nil
	default:
		var change struct {
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(a.Payload["change"]), &change); err != nil {
			return "", err
		}
		raw, err := s.Sim.UpdateParams(ctx, change.Params, a.Payload["actor"], p.Admin.Email)
		if err != nil {
			return "", err
		}
		var v struct {
			Version int64 `json:"version"`
		}
		_ = json.Unmarshal(raw, &v)
		return fmt.Sprintf("settings version %d", v.Version), nil
	}
}
