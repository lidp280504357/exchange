package application

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/platform/apperr"
)

// Closing spot halts the platform coin's spot pair, and its perpetuals,
// whose index comes from it, go reduce-only as a stale index makes them;
// opening spot again lifts nothing - a person decides the market is sound
// (requirements §11.7). The product lines' card lists the contracts that
// went reduce-only while spot was closed, still so, and lifts them
// together (A92).

// spotClosureGrace is how long after spot reopened a contract may still go
// reduce-only for the closure: its mark price counts as stale 10 seconds
// after it stopped.
const spotClosureGrace = time.Minute

// spotHistory is how many of spot's latest changes are read for its last
// closure: enough that edits of the flag's rules or note, each a row with
// the switch as it was, do not hide it (A107).
const spotHistory = 500

// ReducedContract is a contract under reduce-only: why and since when.
type ReducedContract struct {
	Symbol string
	Reason string
	Since  time.Time
}

// SpotClosure is spot's last closure once it reopened - when it closed and
// when it opened again (nil while it is closed or never was) - and the
// contracts that went reduce-only meanwhile and still are.
type SpotClosure struct {
	ClosedAt  *time.Time
	OpenedAt  *time.Time
	Contracts []ReducedContract
}

// ContractLift is how lifting one contract's reduce-only went: lifted
// false when it was not reduce-only any more; Error the service's code and
// message when it failed.
type ContractLift struct {
	Symbol string
	Lifted bool
	Error  string
}

// SpotReduceOnly returns spot's last closure with the contracts it left
// reduce-only (A92).
func (s *Service) SpotReduceOnly(ctx context.Context, p Principal) (SpotClosure, error) {
	if err := p.require(domain.PermInstrumentsRead); err != nil {
		return SpotClosure{}, err
	}
	return s.spotClosure(ctx)
}

// LiftSpotReduceOnly lifts the reduce-only of the contracts spot's last
// closure left so (as SpotReduceOnly lists them now): one administrator
// with derivatives.write and a reason, each contract audited as
// admin.contracts.resumed. A contract that fails does not stop the others.
func (s *Service) LiftSpotReduceOnly(ctx context.Context, p Principal, reason string) ([]ContractLift, error) {
	if err := p.require(domain.PermDerivativesEdit); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	closure, err := s.spotClosure(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ContractLift, 0, len(closure.Contracts))
	for _, c := range closure.Contracts {
		lift := ContractLift{Symbol: c.Symbol}
		raw, err := s.Derivatives.LiftReduceOnly(ctx, c.Symbol, p.Admin.Email)
		if err != nil {
			// What the service said, never an address of ours (the console
			// shows it; the log has the failure in full).
			e := apperr.From(err)
			lift.Error = e.Code + ": " + e.Message
			s.Log.WarnContext(ctx, "spot closure: a contract's reduce-only not lifted", "symbol", c.Symbol, "error", err)
		} else {
			var answer struct {
				Lifted bool `json:"lifted"`
			}
			if err := json.Unmarshal(raw, &answer); err != nil {
				// Asked and maybe done: audited as a failure like the others,
				// and the next contract asked all the same (A107).
				lift.Error = apperr.CodeUnavailable + ": derivatives-service answered in another shape"
				s.Log.WarnContext(ctx, "spot closure: a lift's answer not read", "symbol", c.Symbol, "error", err)
			}
			lift.Lifted = answer.Lifted
		}
		s.auditLift(ctx, p, closure, c, lift, reason)
		out = append(out, lift)
	}
	return out, nil
}

// auditLift records a lift (or its failure) in the administrator's name:
// the closure it followed and the contract's reduce-only.
func (s *Service) auditLift(ctx context.Context, p Principal, closure SpotClosure, c ReducedContract, lift ContractLift, reason string) {
	// The lift is asked by then: its record outlives a caller gone meanwhile.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), productAuditTimeout)
	defer cancel()
	d := map[string]any{
		"product": "spot", "closed_at": closure.ClosedAt, "opened_at": closure.OpenedAt,
		"reduce_only_reason": c.Reason, "reduce_only_since": c.Since, "lifted": lift.Lifted,
	}
	if lift.Error != "" {
		d["error"] = lift.Error
	}
	details, _ := json.Marshal(d)
	if err := s.audit(ctx, p, "contract:"+c.Symbol, "admin.contracts.resumed", reason, string(details)); err != nil {
		s.Log.ErrorContext(ctx, "spot closure: a lift not audited", "symbol", c.Symbol, "error", err)
	}
}

// spotClosure finds spot's last closure in its flag's history (newest
// first) - where it last went from on to off and from off to on again: the
// earliest of the latest run of changes on and the earliest of the run off
// before it, as a change of the flag's rules or note writes a row with the
// switch as it was (A107) - and the contracts that went reduce-only
// between them (or within spotClosureGrace after).
func (s *Service) spotClosure(ctx context.Context) (SpotClosure, error) {
	out := SpotClosure{Contracts: []ReducedContract{}}
	changes, err := s.Flags.History(ctx, productFlag("spot"), spotHistory)
	if err != nil {
		return out, err
	}
	i := 0
	var opened time.Time
	for ; i < len(changes) && changes[i].Enabled; i++ {
		opened = changes[i].At // the earliest of the latest openings
	}
	if i == 0 || i == len(changes) {
		return out, nil // closed now, never switched, or never closed as far as read
	}
	closed := changes[i].At
	for j := i + 1; j < len(changes) && !changes[j].Enabled; j++ {
		closed = changes[j].At // the earliest of the closings before it
	}
	out.ClosedAt, out.OpenedAt = &closed, &opened
	raw, err := s.Derivatives.Contracts(ctx)
	if err != nil {
		return out, err
	}
	var states struct {
		Contracts []struct {
			Symbol     string     `json:"symbol"`
			ReduceOnly bool       `json:"reduce_only"`
			Reason     string     `json:"reduce_only_reason"`
			Since      *time.Time `json:"reduce_only_since"`
		} `json:"contracts"`
	}
	if err := json.Unmarshal(raw, &states); err != nil {
		return out, fmt.Errorf("derivatives: contracts: %w", err)
	}
	for _, c := range states.Contracts {
		if !c.ReduceOnly || c.Since == nil || c.Since.Before(closed) || c.Since.After(opened.Add(spotClosureGrace)) {
			continue
		}
		out.Contracts = append(out.Contracts, ReducedContract{Symbol: c.Symbol, Reason: c.Reason, Since: c.Since.UTC()})
	}
	slices.SortFunc(out.Contracts, func(a, b ReducedContract) int { return strings.Compare(a.Symbol, b.Symbol) })
	return out, nil
}
