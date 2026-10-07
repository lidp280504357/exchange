package application

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// The simulated market of the platform coin from the console (ASTRA design
// §6, C5): its state, price events and settings. market-sim keeps the
// guards (one operator's share of price moves in any hour, hard caps,
// docs/runbook/market-sim.md); what goes beyond the share waits here for a
// second administrator with sim.control, whose approval market-sim then
// receives as approved_by over the console's signed key: both names come
// from the sessions, never from the browser.

// Event types market-sim runs. An OVERLAY is a price event on any pair
// (design 2026-10-07, general price control: J3 here, market-sim's J2):
// the followed pairs' reference data times a factor that ramps up, holds
// and comes back to 1, the simulated market's own pair a JUMP.
var simEventTypes = map[string]bool{
	"JUMP": true, "TARGET": true, "TREND": true, "VOLATILITY": true, "PAUSE": true, "HALT": true, "REANCHOR": true, "SPIKE": true,
	"OVERLAY": true,
}

// The bounds of an OVERLAY as market-sim takes it (J0 contract §3.1): ten
// pairs at most, a ramp down of 3 seconds at least, 10 minutes in all
// (OVERLAY_MAX_SECONDS, at most this).
const (
	overlayMaxSymbols  = 10
	overlayMinRampDown = 3
	overlayMaxSeconds  = 600
)

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
	// A threshold target (ASTRA A6): the level's side (ABOVE, BELOW;
	// inferred when empty), what follows the crossing (FOLLOW, HOLD with
	// HoldSeconds) and the spikes it plans.
	Direction string     `json:"direction,omitempty"`
	Then      string     `json:"then,omitempty"`
	Spikes    []SimSpike `json:"spikes,omitempty"`
	// WidthSeconds is a spike's width (SPIKE; 20 when absent).
	WidthSeconds int `json:"width_seconds,omitempty"`
	// An OVERLAY's (J0 contract §3.1): its pairs, its target (a price for
	// one pair, or a share of each pair's reference price in percent), its
	// ramps around HoldSeconds, and whether it reaches the perpetuals and
	// the leverage (Risk; true when absent, user 2026-10-07 03:0x).
	Symbols         []string `json:"symbols,omitempty"`
	TargetPrice     string   `json:"target_price,omitempty"`
	TargetPct       *float64 `json:"target_pct,omitempty"`
	RampUpSeconds   int      `json:"ramp_up_seconds,omitempty"`
	RampDownSeconds int      `json:"ramp_down_seconds,omitempty"`
	Risk            *bool    `json:"risk,omitempty"`
}

// checkOverlay normalizes and checks an OVERLAY's fields, and refuses them
// on any other event; market-sim checks the rest (which pairs it moves,
// how far, HOUSE's loss cap, one operator's share).
func (in *SimEventInput) checkOverlay() error {
	if in.Type != "OVERLAY" {
		if len(in.Symbols) > 0 || in.TargetPrice != "" || in.TargetPct != nil || in.RampUpSeconds != 0 || in.RampDownSeconds != 0 || in.Risk != nil {
			return apperr.Invalid("symbols, a target, ramps and risk belong to an OVERLAY")
		}
		return nil
	}
	if in.Size != nil || in.Price != "" || in.Mu != nil || in.Factor != nil || in.DurationSeconds != 0 {
		return apperr.Invalid("an OVERLAY takes symbols, a target and ramps, not size, price, mu, factor or duration_seconds")
	}
	seen := map[string]bool{}
	for i, symbol := range in.Symbols {
		symbol = strings.ToUpper(strings.TrimSpace(symbol))
		if symbol == "" || seen[symbol] {
			return apperr.Invalid("symbols are distinct pairs")
		}
		seen[symbol], in.Symbols[i] = true, symbol
	}
	if len(in.Symbols) == 0 || len(in.Symbols) > overlayMaxSymbols {
		return apperr.Invalid("an OVERLAY names 1 to 10 pairs")
	}
	in.TargetPrice = strings.TrimSpace(in.TargetPrice)
	switch {
	case (in.TargetPrice == "") == (in.TargetPct == nil):
		return apperr.Invalid("give target_price or target_pct")
	case in.TargetPct != nil && (*in.TargetPct == 0 || math.IsNaN(*in.TargetPct) || math.IsInf(*in.TargetPct, 0)):
		return apperr.Invalid("target_pct is a share in percent, not 0")
	case in.TargetPrice != "" && len(in.Symbols) > 1:
		return apperr.Invalid("several pairs take target_pct, not one target_price")
	}
	if in.TargetPrice != "" {
		if p, err := decimal.NewFromString(in.TargetPrice); err != nil || !p.IsPositive() {
			return apperr.Invalid("target_price is a positive decimal")
		}
	}
	if in.RampUpSeconds < 1 || in.HoldSeconds < 0 || in.RampDownSeconds < overlayMinRampDown {
		return apperr.Invalid("ramp_up_seconds is 1 at least, hold_seconds 0 at least, ramp_down_seconds 3 at least")
	}
	if in.RampUpSeconds+in.HoldSeconds+in.RampDownSeconds > overlayMaxSeconds {
		return apperr.Invalid("an OVERLAY runs 600 seconds at most, its ramps and hold together")
	}
	return nil
}

// SimSpike is a spike a threshold target plans: when, how far from the
// planned price (a share, e.g. -0.04) and how wide.
type SimSpike struct {
	At           string  `json:"at"`
	Size         float64 `json:"size"`
	WidthSeconds int     `json:"width_seconds,omitempty"`
}

// SimResult is a change done (Event; an OVERLAY's Items, an event a pair;
// Version for the settings) or one waiting for a second administrator
// (Approval).
type SimResult struct {
	Event    json.RawMessage  `json:"event,omitempty"`
	Items    json.RawMessage  `json:"items,omitempty"`
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

// SimPrices returns the last price of every listed pair that has one, for
// the price event form (J3): market-data's tickers, a followed pair's the
// reference market's (times the factor of a price event running on it).
func (s *Service) SimPrices(ctx context.Context, p Principal) (ports.Prices, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return nil, err
	}
	if s.Prices == nil {
		return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "market-data is not configured")
	}
	return s.Prices.Prices(ctx, 0)
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
		return SimResult{}, apperr.Invalid("type must be JUMP, TARGET, TREND, VOLATILITY, PAUSE, HALT, REANCHOR, SPIKE or OVERLAY")
	}
	if err := in.checkTarget(s.Now()); err != nil {
		return SimResult{}, err
	}
	if err := in.checkOverlay(); err != nil {
		return SimResult{}, err
	}
	// asked is a start already past as the operator gave it: the event
	// starts now, and the audit and the request keep what was asked
	// (review 28).
	var asked string
	if in.StartsAt != "" {
		at, err := time.Parse(time.RFC3339, in.StartsAt)
		if err != nil {
			return SimResult{}, apperr.Invalid("starts_at must be an RFC 3339 time")
		}
		// A start already past is now (market-sim takes it so): kept, it
		// would make a request for approval lapse the moment it is made
		// (review ⑭).
		if !at.After(s.Now()) {
			asked, in.StartsAt = at.UTC().Format(time.RFC3339), ""
		}
		if at.After(s.Now().Add(maxLead)) {
			return SimResult{}, ErrSimTooFarAhead
		}
	}
	event := simEventFields(in, strings.TrimSpace(reason))
	raw, err := s.Sim.CreateEvent(ctx, event, p.Admin.Email, "")
	if apperr.Is(err, simEventNeedsApproval) {
		return s.requestSim(ctx, p, domain.KindSimEvent, event, err, reason, map[string]string{"asked_starts_at": asked})
	}
	if err != nil {
		return SimResult{}, err
	}
	res, audit := SimResult{Event: raw}, map[string]any{"event": raw}
	if in.Type == "OVERLAY" {
		// An event a pair (J0 contract §3.1).
		var made struct {
			Items json.RawMessage `json:"items"`
		}
		if json.Unmarshal(raw, &made) != nil || len(made.Items) == 0 {
			return SimResult{}, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "market-sim answered without the events it made")
		}
		res, audit = SimResult{Items: made.Items}, map[string]any{"items": made.Items}
	}
	if asked != "" {
		audit["asked_starts_at"] = asked
	}
	details, _ := json.Marshal(audit)
	return res, s.audit(ctx, p, simAuditTarget, "admin.sim.event_created", strings.TrimSpace(reason), string(details))
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
	if in.Direction != "" {
		out["direction"] = in.Direction
	}
	if in.Then != "" {
		out["then"] = in.Then
	}
	if len(in.Spikes) > 0 {
		out["spikes"] = in.Spikes
	}
	if in.WidthSeconds > 0 {
		out["width_seconds"] = in.WidthSeconds
	}
	if in.Type == "OVERLAY" {
		out["symbols"], out["risk"] = in.Symbols, in.Risk == nil || *in.Risk
		out["ramp_up_seconds"], out["ramp_down_seconds"] = in.RampUpSeconds, in.RampDownSeconds
		if in.TargetPct != nil {
			out["target_pct"] = *in.TargetPct
		} else {
			out["target_price"] = in.TargetPrice
		}
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
		return s.requestSim(ctx, p, domain.KindSimParams, map[string]any{"params": params}, err, reason, nil)
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
// administrator as a request for one, with the move it measured and what
// else the request keeps (extra, its empty values left out).
func (s *Service) requestSim(ctx context.Context, p Principal, kind string, change map[string]any, refusal error, reason string,
	extra map[string]string,
) (SimResult, error) {
	body, err := json.Marshal(change)
	if err != nil {
		return SimResult{}, err
	}
	payload := map[string]string{"change": string(body), "actor": p.Admin.Email}
	for k, v := range extra {
		if v != "" {
			payload[k] = v
		}
	}
	if e := apperr.From(refusal); e != nil {
		// An OVERLAY names the pair beyond the share (J3).
		for _, k := range []string{"move", "volume", "symbol"} {
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
// was asked for, or when its event was to start if sooner, or a target's
// first spike (A6: market-sim refuses a target whose spikes are past).
// Approving it later fails it, nothing sent to market-sim.
func simExpiry(a domain.Approval) time.Time {
	at := a.CreatedAt.Add(simApprovalTTL)
	if a.Kind != domain.KindSimEvent {
		return at
	}
	var e struct {
		StartsAt string `json:"starts_at"`
		Spikes   []struct {
			At string `json:"at"`
		} `json:"spikes"`
	}
	if json.Unmarshal([]byte(a.Payload["change"]), &e) != nil {
		return at
	}
	times := []string{e.StartsAt}
	for _, sp := range e.Spikes {
		times = append(times, sp.At)
	}
	for _, v := range times {
		if t, err := time.Parse(time.RFC3339, v); err == nil && t.Before(at) {
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
// price would do to the perpetual; for a target with spikes, also what
// its worst spike each way would (review 29, as the creator's
// confirmation shows it).
type SimPreview struct {
	ExpiresAt     time.Time
	Expired       bool
	Target        *decimal.Decimal
	Expected      *decimal.Decimal
	Move          *float64
	RequestedMove string
	Impact        json.RawMessage
	SpikeImpacts  []SimSpikeImpact
	// Overlay measures a price event's pairs instead (A81).
	Overlay []SimOverlayLine
}

// SimOverlayLine is one pair of a price event measured now for its
// decider (A81): the platform's price, where the target takes it and the
// move, and HOUSE's worst loss as market-sim estimates it before the event
// starts (nil with LossNote: NO_PRICE, NOT_QUOTED, UNREADABLE).
type SimOverlayLine struct {
	Symbol   string
	Price    *decimal.Decimal
	Target   *decimal.Decimal
	Move     *float64
	Loss     *decimal.Decimal
	LossNote string
}

// SimSpikeImpact is what a target's spike would do to the perpetual: at
// Price, where the mark goes at its tip (spikeMarkShare).
type SimSpikeImpact struct {
	Price  decimal.Decimal
	Impact json.RawMessage
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
	if simOverlay(*a) {
		// A price event on followed pairs: its factor is set against the
		// reference price when it is carried out, and nothing of the
		// simulated market's model moves (J3); each pair measured now
		// (A81).
		out.Overlay = s.overlayLines(ctx, *a)
		return out, nil
	}
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
		if out.Impact, err = s.SimImpact(ctx, p, simMarkAt(*a, target, *expected).Round(8).String()); err != nil {
			s.Log.WarnContext(ctx, "sim preview: no impact", "approval_id", id, "error", err)
			out.Impact = nil
		}
		for _, at := range simSpikeMarks(*a, target) {
			raw, err := s.SimImpact(ctx, p, at.Round(8).String())
			if err != nil {
				s.Log.WarnContext(ctx, "sim preview: no spike impact", "approval_id", id, "error", err)
				continue
			}
			out.SpikeImpacts = append(out.SpikeImpacts, SimSpikeImpact{Price: at, Impact: raw})
		}
	}
	return out, nil
}

// simOverlay reports whether a request is for an OVERLAY.
func simOverlay(a domain.Approval) bool {
	var change struct {
		Type string `json:"type"`
	}
	return a.Kind == domain.KindSimEvent && json.Unmarshal([]byte(a.Payload["change"]), &change) == nil && change.Type == "OVERLAY"
}

// overlayLines measures a price event's pairs now (A81): the platform's
// price (market-data's tickers, the reference market's for a followed
// pair), the target there, the move, and HOUSE's worst loss.
func (s *Service) overlayLines(ctx context.Context, a domain.Approval) []SimOverlayLine {
	var change struct {
		Symbols     []string `json:"symbols"`
		TargetPct   *float64 `json:"target_pct"`
		TargetPrice string   `json:"target_price"`
		Risk        *bool    `json:"risk"`
	}
	if json.Unmarshal([]byte(a.Payload["change"]), &change) != nil {
		return nil
	}
	prices := ports.Prices{}
	if s.Prices != nil {
		var err error
		if prices, err = s.Prices.Prices(ctx, 0); err != nil {
			s.Log.WarnContext(ctx, "sim preview: no prices", "approval_id", a.ID, "error", err)
			prices = ports.Prices{}
		}
	}
	out := make([]SimOverlayLine, 0, len(change.Symbols))
	for _, symbol := range change.Symbols {
		line := SimOverlayLine{Symbol: symbol, LossNote: "NO_PRICE"}
		if p, ok := prices[symbol]; ok && p.IsPositive() {
			target, err := decimal.NewFromString(change.TargetPrice)
			if change.TargetPct != nil {
				target, err = p.Mul(decimal.NewFromFloat(1+*change.TargetPct/100)).Round(8), nil
			}
			if err == nil && target.IsPositive() {
				f, _ := target.Div(p).Float64()
				move := math.Round((f-1)*1e4) / 1e4
				line.Price, line.Target, line.Move = &p, &target, &move
				line.Loss, line.LossNote = s.overlayLoss(ctx, symbol, f, change.Risk == nil || *change.Risk)
			}
		}
		out = append(out, line)
	}
	return out
}

// overlayLoss is HOUSE's worst loss in USDT on a price event of factor f
// on symbol, worked out as market-sim does before the event starts (its
// lossOf, J0 contract §3.3): all HOUSE may still buy (f above 1) or sell
// (below) there and, with risk, on the pair's perpetuals, at what a unit
// is worth, times how far f takes the price and back. For the decider to
// see; market-sim alone holds it against OVERLAY_MAX_LOSS_USDT.
func (s *Service) overlayLoss(ctx context.Context, symbol string, f float64, risk bool) (*decimal.Decimal, string) {
	if s.MarketMaker == nil {
		return nil, "UNREADABLE"
	}
	symbols := []string{symbol}
	if risk {
		symbols = append(symbols, symbol+"-PERP")
		if base, ok := strings.CutSuffix(symbol, "-USDT"); ok {
			symbols = append(symbols, base+"-USD-PERP")
		}
	}
	total := decimal.Zero
	for i, sym := range symbols {
		r, quoted, err := s.MarketMaker.HouseRooms(ctx, sym)
		if err != nil {
			s.Log.WarnContext(ctx, "sim preview: HOUSE's rooms not read", "symbol", sym, "error", err)
			return nil, "UNREADABLE"
		}
		if !quoted {
			if i == 0 {
				return nil, "NOT_QUOTED"
			}
			continue
		}
		units := r.Sell
		if f > 1 {
			units = r.Buy
		}
		total = total.Add(units.Mul(r.UnitValue).Mul(decimal.NewFromFloat(overlayLossShare(f, r.Inverse))).Round(8))
	}
	loss := total.Round(2)
	return &loss, ""
}

// overlayLossShare is how much of a unit's worth a factor f takes back
// once it comes back to 1 (market-sim's domain.OverlayLoss): an inverse
// contract is valued in its coin.
func overlayLossShare(f float64, inverse bool) float64 {
	switch {
	case f >= 1 && inverse:
		return 1 - 1/f
	case f >= 1:
		return f - 1
	case inverse:
		return 1/f - 1
	}
	return 1 - f
}

// simCreated says what market-sim made of an approved event: the event,
// or an OVERLAY's events, one a pair.
func simCreated(raw json.RawMessage) string {
	var made struct {
		ID    string `json:"id"`
		Items []struct {
			Symbol  string `json:"symbol"`
			EventID string `json:"event_id"`
		} `json:"items"`
	}
	_ = json.Unmarshal(raw, &made)
	if len(made.Items) == 0 {
		return "event " + made.ID
	}
	events := make([]string, 0, len(made.Items))
	for _, it := range made.Items {
		events = append(events, it.Symbol+" "+it.EventID)
	}
	return "events " + strings.Join(events, ", ")
}

// simExpected is where a request would take the price from target: a
// jump's, a spike's tip, a target event's price, the settings' new anchor
// (the target moves with P0); nil for changes that move no price directly.
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
	case a.Kind == domain.KindSimEvent && (change.Type == "JUMP" || change.Type == "SPIKE"):
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
		return simCreated(raw), nil
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
