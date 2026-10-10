package application

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/skill/exchange/internal/platform/pagecursor"
	"github.com/skill/exchange/internal/platform/pg"
)

// Fund operations (requirements §5.12, design 2026-10-02 §2): manual
// adjustments of a user's balance and contributions to the insurance
// fund. With the flag admin.two_person_approval on, a second
// administrator approves each; off, the requester carries one out alone
// when it is worth at most the single-person limit and keeps them within
// the 24-hour limit, and anything larger (or of unknown worth) still
// waits for a second administrator. Every operation keeps its row in
// approvals and is audited.

// TwoPerson reports whether fund operations need a second administrator
// (the flag admin.two_person_approval; nil Features leave it off).
func (s *Service) TwoPerson() bool {
	return s.Features != nil && s.Features.Enabled(flags.KeyTwoPerson, flags.Subject{})
}

// fundActions names the audit actions of each kind of fund operation;
// unfinished is an attempt to carry one out that did not finish (C5.5 ⑮).
var fundActions = map[string]struct{ requested, approved, rejected, executed, failed, unfinished string }{
	domain.KindLedgerAdjustment: {
		"admin.ledger.adjustment_requested", "admin.ledger.adjustment_approved", "admin.ledger.adjustment_rejected",
		"admin.ledger.adjustment_executed", "admin.ledger.adjustment_failed", "admin.ledger.adjustment_unfinished",
	},
	domain.KindInsuranceFund: {
		"admin.derivatives.insurance_requested", "admin.derivatives.insurance_approved", "admin.derivatives.insurance_rejected",
		"admin.derivatives.insurance_executed", "admin.derivatives.insurance_failed", "admin.derivatives.insurance_unfinished",
	},
	domain.KindDepositBackfill: {
		"admin.deposits.backfill_requested", "admin.deposits.backfill_approved", "admin.deposits.backfill_rejected",
		"admin.deposits.backfill_executed", "admin.deposits.backfill_failed", "admin.deposits.backfill_unfinished",
	},
	domain.KindSimEvent: {
		"admin.sim.event_requested", "admin.sim.event_approved", "admin.sim.event_rejected", "admin.sim.event_created", "admin.sim.event_failed",
		"admin.sim.event_unfinished",
	},
	domain.KindSimParams: {
		"admin.sim.params_requested", "admin.sim.params_approved", "admin.sim.params_rejected", "admin.sim.params_changed",
		"admin.sim.params_failed", "admin.sim.params_unfinished",
	},
	domain.KindSimMint: {
		"admin.sim.mint_requested", "admin.sim.mint_approved", "admin.sim.mint_rejected", "admin.sim.mint_executed", "admin.sim.mint_failed",
		"admin.sim.mint_unfinished",
	},
	domain.KindDepositAssign: {
		"admin.deposits.assign_requested", "admin.deposits.assign_approved", "admin.deposits.assign_rejected",
		"admin.deposits.assign_executed", "admin.deposits.assign_failed", "admin.deposits.assign_unfinished",
	},
	domain.KindWelcomeCredit: {
		"admin.platform.welcome_requested", "admin.platform.welcome_approved", "admin.platform.welcome_rejected",
		"admin.platform.welcome_changed", "admin.platform.welcome_failed", "admin.platform.welcome_unfinished",
	},
	domain.KindMarginParams: {
		"admin.margin.params_requested", "admin.margin.params_approved", "admin.margin.params_rejected",
		"admin.margin.params_changed", "admin.margin.params_failed", "admin.margin.params_unfinished",
	},
	domain.KindMarginLiquidate: {
		"admin.margin.liquidation_requested", "admin.margin.liquidation_approved", "admin.margin.liquidation_rejected",
		"admin.margin.liquidation_started", "admin.margin.liquidation_failed", "admin.margin.liquidation_unfinished",
	},
	domain.KindHouseCaps: {
		"admin.house.caps_requested", "admin.house.caps_approved", "admin.house.caps_rejected",
		"admin.house.caps_changed", "admin.house.caps_failed", "admin.house.caps_unfinished",
	},
}

// simKind reports whether an approval is a simulated market's change.
func simKind(kind string) bool { return kind == domain.KindSimEvent || kind == domain.KindSimParams }

// changeKind reports whether an approval sets something rather than books
// it: a simulated market's change, the welcome credits, margin terms, a
// margin liquidation (margin-service starts one per approval) and HOUSE's
// caps. Its attempt is not marked (the service refuses it twice), a
// failure leaves it as it was.
func changeKind(kind string) bool {
	return simKind(kind) || kind == domain.KindWelcomeCredit || marginKind(kind) || kind == domain.KindHouseCaps
}

// fundTarget is the audit target of an operation.
func fundTarget(a domain.Approval) string {
	switch {
	case a.Kind == domain.KindInsuranceFund:
		return "insurance:" + a.Payload["asset"]
	case simKind(a.Kind), a.Kind == domain.KindSimMint:
		return simAuditTarget
	case a.Kind == domain.KindWelcomeCredit:
		return platformTarget
	case a.Kind == domain.KindMarginParams:
		return "margin:" + a.Payload["target"]
	case a.Kind == domain.KindHouseCaps:
		return houseCapsTarget
	}
	return "user:" + a.Payload["user_id"]
}

// FundRequest is a fund operation to carry out or to ask a second
// administrator for.
type FundRequest struct {
	Kind string // domain.KindLedgerAdjustment or domain.KindInsuranceFund
	// UserID owns the balance an adjustment changes, in its SPOT account
	// unless AccountType is FUTURES.
	UserID      string
	AccountType string
	Asset       string
	// Amount credits (positive) or debits (negative) an adjustment; a
	// contribution is positive.
	Amount decimal.Decimal
	Reason string
	// Reference is an optional ticket or order number kept with it.
	Reference string
	// Backfill is a deposit backfill's custodian trade (Kind
	// DEPOSIT_BACKFILL): wallet-service finds the user and the asset.
	Backfill *ports.ManualDeposit
	// Shares are a mint's (SIM_MINT) amounts per bot, of the bots of Role
	// ("" for every bot).
	Shares []MintShare
	Role   string
	// DepositID is the deposit of nobody a DEPOSIT_ASSIGN credits to
	// UserID; its asset and amount are the deposit's.
	DepositID string
	// Direct carries it out at once when single-person mode and its limits
	// allow; without it a second administrator always decides.
	Direct bool
	// Key is the request's Idempotency-Key ("" for none): the same request
	// again returns its operation, finishing one whose attempt did not.
	Key string
}

// fingerprint identifies a request as the administrator made it (a
// mint's shares come from the bots of the moment, so they are left out).
func (in *FundRequest) fingerprint() []byte {
	fields := []string{
		in.Kind, in.UserID, in.AccountType, in.Asset, in.Amount.String(), strings.TrimSpace(in.Reason), in.Reference, in.Role,
		strconv.FormatBool(in.Direct), in.DepositID,
	}
	if b := in.Backfill; b != nil {
		fields = append(fields, b.Network, b.TradeID, b.Address, b.TxHash)
	}
	return fingerprint(fields...)
}

func (in *FundRequest) validate() error {
	if err := needReason(in.Reason); err != nil {
		return err
	}
	in.Asset = strings.ToUpper(strings.TrimSpace(in.Asset))
	in.Reference = strings.TrimSpace(in.Reference)
	if len(in.Reference) > 64 {
		return apperr.Invalid("the reference has at most 64 characters")
	}
	switch in.Kind {
	case domain.KindLedgerAdjustment:
		if in.Amount.IsZero() {
			return apperr.Invalid("the amount must not be zero")
		}
		if in.Asset == "" {
			return apperr.Invalid("the asset is required")
		}
		u, err := uuid.Parse(in.UserID)
		if err != nil {
			return apperr.Invalid("user_id must be a UUID")
		}
		in.UserID = u.String()
		switch in.AccountType = strings.ToUpper(strings.TrimSpace(in.AccountType)); in.AccountType {
		case "":
			in.AccountType = AccountSpot
		case AccountSpot, AccountFutures:
		default:
			return apperr.Invalid("account_type must be SPOT or FUTURES")
		}
	case domain.KindInsuranceFund:
		if !in.Amount.IsPositive() {
			return apperr.Invalid("the amount must be positive")
		}
		if in.Asset == "" {
			in.Asset = "USDT"
		}
	case domain.KindSimMint:
		if !in.Amount.IsPositive() || in.Asset == "" {
			return apperr.Invalid("a mint needs an asset and a positive amount")
		}
		if len(in.Shares) == 0 {
			return ErrNoBots
		}
	case domain.KindDepositAssign:
		// Canonical forms (uuid.Parse also takes upper case, braces,
		// urn:uuid: and bare hex): one spelling per deposit and user, so the
		// key's fingerprint, the holder rule and the index of one live
		// request per deposit see the same IDs (review ㉖).
		d, err := uuid.Parse(in.DepositID)
		if err != nil {
			return apperr.NotFound("no such deposit")
		}
		in.DepositID = d.String()
		u, err := uuid.Parse(in.UserID)
		if err != nil || u == uuid.Nil {
			return apperr.Invalid("user_id must be the ID of the user the deposit is credited to")
		}
		in.UserID = u.String()
	case domain.KindDepositBackfill:
		b := in.Backfill
		if b == nil || !b.Amount.IsPositive() {
			return apperr.Invalid("a backfill needs the custodian's trade and a positive amount")
		}
		b.Network, b.TradeID = strings.ToUpper(strings.TrimSpace(b.Network)), strings.TrimSpace(b.TradeID)
		b.Address, b.TxHash = strings.TrimSpace(b.Address), strings.TrimSpace(b.TxHash)
		if b.Network == "" || b.TradeID == "" || b.Address == "" || b.TxHash == "" {
			return apperr.Invalid("the network, trade ID, address and transaction hash are required")
		}
		in.Amount = b.Amount
	default:
		return fmt.Errorf("fund operation of unknown kind %q", in.Kind)
	}
	return nil
}

// backfillPayload records a backfill's trade in its operation.
func backfillPayload(a *domain.Approval, b ports.ManualDeposit, check ports.ManualCheck) {
	a.Payload["user_id"], a.Payload["asset"] = check.UserID, check.Asset
	a.Payload["network"], a.Payload["trade_id"], a.Payload["address"], a.Payload["tx_hash"] = b.Network, b.TradeID, b.Address, b.TxHash
	a.Payload["entered_by"] = b.Actor
}

// backfillOf reads a backfill's trade back from its operation.
func backfillOf(a domain.Approval) (ports.ManualDeposit, error) {
	amount, err := decimal.NewFromString(a.Payload["amount"])
	if err != nil {
		return ports.ManualDeposit{}, err
	}
	return ports.ManualDeposit{
		Network: a.Payload["network"], TradeID: a.Payload["trade_id"], Address: a.Payload["address"], TxHash: a.Payload["tx_hash"],
		Amount: amount, Actor: a.Payload["entered_by"],
	}, nil
}

// RequestAdjustment records a manual adjustment for a second
// administrator to approve, whatever the mode.
func (s *Service) RequestAdjustment(ctx context.Context, p Principal, in Adjustment) (domain.Approval, error) {
	return s.SubmitFunds(ctx, p, FundRequest{
		Kind: domain.KindLedgerAdjustment, UserID: in.UserID, Asset: in.Asset, Amount: in.Amount, Reason: in.Reason,
	})
}

// RequestInsuranceFunding records a contribution of simulated funds to the
// insurance fund for a second administrator to approve.
func (s *Service) RequestInsuranceFunding(ctx context.Context, p Principal, asset string, amount decimal.Decimal, reason string) (domain.Approval, error) {
	return s.SubmitFunds(ctx, p, FundRequest{Kind: domain.KindInsuranceFund, Asset: asset, Amount: amount, Reason: reason})
}

// SubmitFunds carries out a fund operation in single-person mode within
// the limits when asked to (Direct), and otherwise records it for a second
// administrator. The answer is the operation: EXECUTED with its journal,
// FAILED with the ledger's refusal, or PENDING with the reason it waits.
// When the ledger does not answer, the operation stays PENDING, attempted
// (single mode: its requester finishes it later, or repeats the request)
// and the error names it. The same request with the same key returns its
// operation.
func (s *Service) SubmitFunds(ctx context.Context, p Principal, in FundRequest) (domain.Approval, error) {
	perm := domain.PermAdjustRequest
	if in.Kind == domain.KindDepositBackfill || in.Kind == domain.KindDepositAssign {
		perm = domain.PermDepositsReview
	}
	if err := p.require(perm); err != nil {
		return domain.Approval{}, err
	}
	if err := in.validate(); err != nil {
		return domain.Approval{}, err
	}
	c, err := s.claimKey(ctx, p, in.Key, scopeFunds, in.fingerprint())
	if err != nil {
		return domain.Approval{}, err
	}
	if !c.Fresh {
		// Made already, unless the first request stopped before it was recorded.
		if a, err := s.Store.Read().Approvals().Get(ctx, c.Ref); err != nil || a != nil {
			if err != nil {
				return domain.Approval{}, err
			}
			return s.again(ctx, p, *a)
		}
	}
	// Not to an account cleared out (L4); a backfill's account is the one
	// its check finds (A117).
	if in.Kind == domain.KindLedgerAdjustment || in.Kind == domain.KindDepositAssign {
		if err := s.notPurged(ctx, in.UserID); err != nil {
			return domain.Approval{}, err
		}
	}
	var check ports.ManualCheck
	if in.Kind == domain.KindDepositBackfill {
		in.Backfill.Actor = p.Admin.Email
		var err error
		if check, err = s.Deposits.CheckManual(ctx, *in.Backfill); err != nil {
			return domain.Approval{}, err
		}
		if err := s.notPurged(ctx, check.UserID); err != nil {
			return domain.Approval{}, err
		}
		in.Asset = check.Asset
	}
	var nobody depositOfNobody
	if in.Kind == domain.KindDepositAssign {
		var err error
		if nobody, err = s.depositOfNobody(ctx, in.DepositID); err != nil {
			return domain.Approval{}, err
		}
		in.Asset, in.Amount = nobody.Asset, nobody.Amount
	}
	now := s.Now()
	a := domain.Approval{
		ID: c.Ref, Kind: in.Kind, Reason: strings.TrimSpace(in.Reason),
		Payload: map[string]string{"asset": in.Asset, "amount": in.Amount.String()},
		Status:  domain.ApprovalPending, RequestedBy: p.Admin.ID, CreatedAt: now, Mode: domain.ModeTwoPerson,
		RequestedByEmail: p.Admin.Email,
	}
	if in.Kind == domain.KindDepositBackfill {
		backfillPayload(&a, *in.Backfill, check)
	}
	if in.Kind == domain.KindLedgerAdjustment {
		a.Payload["user_id"] = in.UserID
		if in.AccountType != AccountSpot {
			a.Payload["account_type"] = in.AccountType
		}
	}
	if in.Kind == domain.KindDepositAssign {
		a.Payload["user_id"], a.Payload["deposit_id"] = in.UserID, in.DepositID
		a.Payload["network"], a.Payload["address"], a.Payload["tx_hash"] = nobody.Network, nobody.Address, nobody.TxHash
		nobody.holderPayload(a.Payload)
	}
	if in.Kind == domain.KindSimMint {
		shares, err := json.Marshal(in.Shares)
		if err != nil {
			return domain.Approval{}, err
		}
		a.Payload["bots"] = string(shares)
		if in.Role != "" {
			a.Payload["role"] = in.Role
		}
	}
	if in.Reference != "" {
		a.Payload["reference"] = in.Reference
	}
	if value, ok := s.worth(ctx, in.Asset, in.Amount); ok {
		a.ValueUSDT = &value
	}
	var limits domain.Settings
	switch {
	case !in.Direct:
		a.Escalation = domain.EscalationRequested
	case in.Kind == domain.KindDepositAssign && nobody.notHolder(in.UserID):
		a.Escalation = domain.EscalationNotHolder
	case s.TwoPerson():
		a.Escalation = domain.EscalationTwoPerson
	case a.ValueUSDT == nil:
		a.Escalation = domain.EscalationNoPrice
	default:
		var err error
		if limits, err = s.settings(ctx, s.Store.Read()); err != nil {
			return domain.Approval{}, err
		}
		if a.ValueUSDT.GreaterThan(limits.SingleMax) {
			a.Escalation = domain.EscalationSingleMax
		}
	}
	actions := fundActions[in.Kind]
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		if a.Escalation == "" {
			// One operation at a time per administrator, so two cannot both fit the day's limit.
			if _, err := r.Admins().GetForUpdate(ctx, p.Admin.ID); err != nil {
				return err
			}
			used, err := r.Approvals().SingleUsage(ctx, p.Admin.ID, now.Add(-24*time.Hour))
			if err != nil {
				return err
			}
			if used.Add(*a.ValueUSDT).GreaterThan(limits.DailyMax) {
				a.Escalation = domain.EscalationDailyMax
			} else {
				// Carried out at once: attempted from now on, as it may book before its outcome is known.
				a.Mode, a.AttemptedAt = domain.ModeSingle, now
			}
		}
		if err := r.Approvals().Insert(ctx, a); err != nil {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: fundTarget(a), Action: actions.requested, Actor: p.Admin.Email, Reason: a.Reason, Details: fundDetails(a),
		}, p.Admin.Email)
	})
	if _, dup := pg.UniqueViolation(err); dup {
		// The same request, concurrently, recorded it first.
		if cur, gerr := s.Store.Read().Approvals().Get(ctx, a.ID); gerr == nil && cur != nil {
			return s.again(ctx, p, *cur)
		}
	}
	if err != nil || a.Mode != domain.ModeSingle {
		return a, err
	}
	return s.carryOut(ctx, p, a)
}

// again answers a repeated request with its operation: a single-person
// operation whose attempt did not finish is finished (its requester's own
// key, so its requester), the others are returned as they stand.
func (s *Service) again(ctx context.Context, p Principal, a domain.Approval) (domain.Approval, error) {
	if a.Status == domain.ApprovalPending && a.Mode == domain.ModeSingle && a.RequestedBy == p.Admin.ID && !a.AttemptedAt.IsZero() {
		return s.carryOut(ctx, p, a)
	}
	return a, nil
}

// carryOut carries out a single-person operation and records its outcome.
func (s *Service) carryOut(ctx context.Context, p Principal, a domain.Approval) (domain.Approval, error) {
	actions := fundActions[a.Kind]
	if err := s.execute(ctx, &a, p); err != nil {
		return domain.Approval{}, s.unfinished(ctx, p, a, a.Reason, err)
	}
	return s.record(ctx, a, p, actions.executed, actions.failed)
}

// unfinished records an attempt to carry out a fund operation that did not
// finish: the operation stays pending, attempted, with how the attempt
// ended, and the error names it (C5.5 ⑥). An error of unknown kind is an
// unknown outcome. reason is the attempt's: the requester's in
// single-person mode, the decider's otherwise (C5.5 ⑰).
func (s *Service) unfinished(ctx context.Context, p Principal, a domain.Approval, reason string, err error) error {
	e := apperr.From(err)
	if e.Kind == apperr.KindInternal {
		e = apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "the outcome is unknown")
	}
	note := e.Code + ": " + e.Message
	if booked, ok := e.Details["booked"]; ok {
		note = fmt.Sprintf("booked %v of %v; %v: %s", booked, e.Details["of"], e.Details["bot"], note)
	}
	// Recorded and audited at once: until finished, the trail says it may have booked (C5.5 ⑮).
	merr := s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.Approvals().MarkAttempted(ctx, a.ID, s.Now(), note); err != nil {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "approval:" + a.ID, Action: fundActions[a.Kind].unfinished, Actor: p.Admin.Email, Reason: reason,
			Details: fmt.Sprintf(`{"status":%q,"result":%q,"mode":%q}`, domain.ApprovalPending, note, a.Mode),
		}, p.Admin.Email)
	})
	if merr != nil {
		s.Log.WarnContext(ctx, "fund operation: record an unfinished attempt", "approval_id", a.ID, "error", merr)
	}
	return e.WithDetail("approval_id", a.ID)
}

// record stores the outcome of a single-person operation with its audit
// event; an operation decided meanwhile (its requester retried it) is
// returned as it stands.
func (s *Service) record(ctx context.Context, a domain.Approval, p Principal, executed, failed string) (domain.Approval, error) {
	a.DecidedBy, a.DecidedAt, a.DecidedByEmail = p.Admin.ID, s.Now(), p.Admin.Email
	var current *domain.Approval
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Approvals().GetForUpdate(ctx, a.ID)
		if err != nil {
			return err
		}
		if cur == nil || cur.Status != domain.ApprovalPending {
			current = cur
			return nil
		}
		if err := r.Approvals().Update(ctx, a); err != nil {
			return err
		}
		action := executed
		if a.Status == domain.ApprovalFailed {
			action = failed
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: fundTarget(a), Action: action, Actor: p.Admin.Email, Reason: a.Reason, Details: fundDetails(a),
		}, p.Admin.Email)
	})
	if err != nil {
		return domain.Approval{}, err
	}
	if current != nil {
		if got, err := s.Store.Read().Approvals().Get(ctx, a.ID); err == nil && got != nil {
			return *got, nil
		}
	}
	return a, nil
}

// User account types an adjustment changes.
const (
	AccountSpot    = "SPOT"
	AccountFutures = "FUTURES"
)

// accountOf is the account an adjustment changes (SPOT unless its payload
// names FUTURES).
func accountOf(a domain.Approval) string {
	if t := a.Payload["account_type"]; t != "" {
		return t
	}
	return AccountSpot
}

// fundDetails is an operation's audit detail.
func fundDetails(a domain.Approval) string {
	if a.Kind == domain.KindWelcomeCredit {
		d, _ := json.Marshal(map[string]any{
			"approval_id": a.ID, "credits": json.RawMessage(orEmptyList(a.Payload["credits"])), "previous": json.RawMessage(orEmptyList(a.Payload["previous"])),
			"expected_version": a.Payload["expected_version"], "raise_usdt": a.Payload["raise_usdt"], "mode": a.Mode, "escalation": a.Escalation,
			"status": a.Status, "result": a.Result,
		})
		return string(d)
	}
	if simKind(a.Kind) {
		d, _ := json.Marshal(map[string]any{
			"approval_id": a.ID, "change": json.RawMessage(a.Payload["change"]), "move": a.Payload["move"], "mode": a.Mode,
			"escalation": a.Escalation, "status": a.Status, "result": a.Result,
		})
		return string(d)
	}
	switch a.Kind {
	case domain.KindMarginParams:
		d, _ := json.Marshal(map[string]any{
			"approval_id": a.ID, "target": a.Payload["target"], "terms": json.RawMessage(orEmptyObject(a.Payload["terms"])),
			"previous": json.RawMessage(orEmptyObject(a.Payload["previous"])), "changed": a.Payload["changed"],
			"expected_version": a.Payload["expected_version"], "mode": a.Mode, "escalation": a.Escalation, "status": a.Status, "result": a.Result,
		})
		return string(d)
	case domain.KindHouseCaps:
		d, _ := json.Marshal(map[string]any{
			"approval_id": a.ID, "caps": json.RawMessage(orEmptyObject(a.Payload["caps"])),
			"previous": json.RawMessage(orEmptyObject(a.Payload["previous"])), "changed": a.Payload["changed"],
			"expected_version": a.Payload["expected_version"], "mode": a.Mode, "status": a.Status, "result": a.Result,
		})
		return string(d)
	case domain.KindMarginLiquidate:
		d, _ := json.Marshal(map[string]any{
			"approval_id": a.ID, "user_id": a.Payload["user_id"], "account": a.Payload["account"], "account_status": a.Payload["status"],
			"margin_level": a.Payload["margin_level"], "total_asset": a.Payload["total_asset"], "total_liability": a.Payload["total_liability"],
			"mode": a.Mode, "escalation": a.Escalation, "status": a.Status, "result": a.Result,
		})
		return string(d)
	}
	value := "null"
	if a.ValueUSDT != nil {
		value = fmt.Sprintf("%q", a.ValueUSDT.String())
	}
	account := ""
	switch a.Kind {
	case domain.KindLedgerAdjustment:
		account = fmt.Sprintf(`"account_type":%q,`, accountOf(a))
	case domain.KindDepositBackfill:
		account = fmt.Sprintf(`"network":%q,"trade_id":%q,"tx_hash":%q,"address":%q,"custodian_checked":false,`,
			a.Payload["network"], a.Payload["trade_id"], a.Payload["tx_hash"], a.Payload["address"])
	case domain.KindDepositAssign:
		account = fmt.Sprintf(`"deposit_id":%q,"user_id":%q,"former_holder":%q,"address_owner":%q,"network":%q,"tx_hash":%q,"address":%q,`,
			a.Payload["deposit_id"], a.Payload["user_id"], a.Payload["former_holder"], a.Payload["address_owner"], a.Payload["network"],
			a.Payload["tx_hash"], a.Payload["address"])
	case domain.KindSimMint:
		account = fmt.Sprintf(`"role":%q,"bots":%s,`, a.Payload["role"], a.Payload["bots"])
	}
	// The journal on its own (the C1 review): the balances before and
	// after are the ledger journal's.
	return fmt.Sprintf(`{"approval_id":%q,%s"asset":%q,"amount":%q,"mode":%q,"escalation":%q,"value_usdt":%s,"status":%q,"result":%q,"journal_id":%q}`,
		a.ID, account, a.Payload["asset"], a.Payload["amount"], a.Mode, a.Escalation, value, a.Status, a.Result, a.JournalID)
}

// priceMaxAge is how fresh a price values an operation against the
// single-person limits: an older one counts as none, so a second
// administrator decides (C5.5 ⑥).
const priceMaxAge = time.Minute

// worth values an amount of an asset in USDT at its USDT pair's last
// price; false without a fresh one.
func (s *Service) worth(ctx context.Context, asset string, amount decimal.Decimal) (decimal.Decimal, bool) {
	if asset == "USDT" {
		return amount.Abs(), true
	}
	return s.valuer(ctx, priceMaxAge)(asset, amount.Abs())
}

// Approvals returns a page of fund operations in a status ("": all),
// newest first, and the cursor of the next ("" on the last).
func (s *Service) Approvals(ctx context.Context, p Principal, status, cursor string, limit int) ([]domain.Approval, string, error) {
	if err := p.require(domain.PermAdjustRequest); err != nil {
		if p.require(domain.PermAuditRead) != nil {
			return nil, "", err
		}
	}
	at, id, err := pagecursor.Decode(cursor)
	if err != nil {
		return nil, "", apperr.Invalid("bad cursor")
	}
	limit = pageLimit(limit)
	list, err := s.Store.Read().Approvals().List(ctx, status, at, id, limit+1)
	if err != nil {
		return nil, "", err
	}
	now := s.Now()
	for i := range list {
		a := &list[i]
		a.Lapsed = a.Status == domain.ApprovalPending && lapsedAt(*a, now)
	}
	if len(list) <= limit {
		return list, "", nil
	}
	list = list[:limit]
	last := list[limit-1]
	return list, pagecursor.Encode(last.CreatedAt, last.ID), nil
}

// DecideApproval approves (and carries out) or rejects a pending
// operation: another administrator's request, the decider's own
// single-person operation whose outcome was unknown, or (rejecting) the
// decider's own request. The row stays locked while the ledger books it,
// so a second decision waits and then finds it decided. A fund operation
// is marked attempted before it is carried out; when the outcome is
// unknown it stays pending, to be finished, never rejected (C5.5 ⑥). The
// decider's decision repeated returns the operation as it left it.
func (s *Service) DecideApproval(ctx context.Context, p Principal, id string, approve bool, reason string) (domain.Approval, error) {
	// A fund operation needs ledger.adjust.approve, a simulated market's
	// change sim.control, margin terms instruments.trading and a margin
	// liquidation derivatives.write (checked once the request is read).
	deciders := []string{domain.PermAdjustApprove, domain.PermSimControl, domain.PermInstrumentsTrading, domain.PermDerivativesEdit}
	if !slices.ContainsFunc(deciders, func(perm string) bool { return p.require(perm) == nil }) {
		return domain.Approval{}, domain.ErrForbidden.WithDetail("permission", strings.Join(deciders, "|"))
	}
	if err := needReason(reason); err != nil {
		return domain.Approval{}, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return domain.Approval{}, apperr.NotFound("no such request")
	}
	if approve {
		if err := s.approvesPurged(ctx, id); err != nil {
			return domain.Approval{}, err
		}
		if err := s.beginAttempt(ctx, p, id); err != nil {
			return domain.Approval{}, err
		}
	}
	var a domain.Approval
	var unknown error
	repeated := false
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Approvals().GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if cur == nil {
			return apperr.NotFound("no such request")
		}
		perm := domain.PermAdjustApprove
		switch {
		case simKind(cur.Kind):
			perm = domain.PermSimControl
		case cur.Kind == domain.KindWelcomeCredit:
			perm = domain.PermSettingsEdit
		case cur.Kind == domain.KindMarginParams:
			perm = domain.PermInstrumentsTrading
		case cur.Kind == domain.KindMarginLiquidate:
			perm = domain.PermDerivativesEdit
		}
		if err := p.require(perm); err != nil {
			return err
		}
		if decidedAlike(*cur, p.Admin.ID, approve) {
			a, repeated = *cur, true
			return nil
		}
		if err := cur.Decide(p.Admin.ID, approve); err != nil {
			return err
		}
		a = *cur
		a.DecidedBy, a.DecidedAt = p.Admin.ID, s.Now()
		actions, ok := fundActions[a.Kind]
		if !ok {
			return fmt.Errorf("approval %s: unknown kind %q", a.ID, a.Kind)
		}
		action := actions.rejected
		switch {
		case !approve:
			a.Status, a.Result = domain.ApprovalRejected, strings.TrimSpace(reason)
		case lapsedAt(a, a.DecidedAt):
			// Lapsed: nothing goes to market-sim (C5.5 ④) or the ledger (review ㉚).
			action = actions.failed
			expiry, _ := approvalExpiry(a)
			a.Status, a.Result = domain.ApprovalFailed, "expired at "+expiry.UTC().Format(time.RFC3339)
		default:
			action = actions.approved
			if err := s.execute(ctx, &a, p); err != nil {
				if changeKind(a.Kind) {
					return err // a change stays as it was
				}
				unknown = err // still pending, attempted since beginAttempt
				return nil
			}
		}
		if err := r.Approvals().Update(ctx, a); err != nil {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "approval:" + a.ID, Action: action, Actor: p.Admin.Email, Reason: reason, Details: decisionDetails(a),
		}, p.Admin.Email)
	})
	switch {
	case err != nil:
		var e *apperr.Error
		if errors.As(err, &e) && (e.Kind == apperr.KindUnavailable || e.Kind == apperr.KindInternal) {
			return domain.Approval{}, e.WithDetail("approval_id", id)
		}
		return domain.Approval{}, err
	case unknown != nil:
		return domain.Approval{}, s.unfinished(ctx, p, a, strings.TrimSpace(reason), unknown)
	case repeated:
		return a, nil
	}
	if got, err := s.Store.Read().Approvals().Get(ctx, a.ID); err == nil && got != nil {
		return *got, nil
	}
	return a, nil
}

// beginAttempt marks a pending fund operation attempted before its
// decider carries it out, when the decision would: the attempt may book
// before its outcome is known, and from then on the operation is
// finished, never rejected (C5.5 ⑥). The decision itself checks again.
func (s *Service) beginAttempt(ctx context.Context, p Principal, id string) error {
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Approvals().GetForUpdate(ctx, id)
		if err != nil || cur == nil || changeKind(cur.Kind) || !cur.AttemptedAt.IsZero() {
			return err
		}
		if p.require(domain.PermAdjustApprove) != nil || cur.Decide(p.Admin.ID, true) != nil {
			return nil
		}
		return r.Approvals().MarkAttempted(ctx, id, s.Now(), "")
	})
}

// ErrUserPurged refuses to move the money of a test account cleared out
// (L4): closed and emptied for good, it is left alone (L1).
var ErrUserPurged = apperr.New(apperr.KindConflict, "ADMIN_USER_PURGED",
	"the account was cleared out (a closed test account): its money is left alone")

// booksToUser reports whether a fund operation books to a user's account:
// an adjustment, a deposit of nobody credited to a user, a backfilled
// deposit (to its address's user, found by its check: A117).
func booksToUser(kind string) bool {
	return kind == domain.KindLedgerAdjustment || kind == domain.KindDepositAssign || kind == domain.KindDepositBackfill
}

// notPurged refuses an operation that books to an account cleared out
// (L4). A deposit of nobody has no account to ask about; an account
// user-service does not know is the ledger's to judge, as before.
func (s *Service) notPurged(ctx context.Context, userID string) error {
	if userID == "" || userID == domain.NoOwner {
		return nil
	}
	u, err := s.Users.Get(ctx, userID)
	if err != nil {
		if apperr.From(err).Kind == apperr.KindNotFound {
			return nil
		}
		return err
	}
	if u.PurgedAt != nil {
		return ErrUserPurged.WithDetail("purged_at", u.PurgedAt.UTC().Format(time.RFC3339))
	}
	return nil
}

// approvesPurged refuses to approve an operation on an account cleared out
// since it was requested; one attempted already may have booked, so it is
// finished as any other (C5.5 ⑥). Rejecting it is left open.
func (s *Service) approvesPurged(ctx context.Context, id string) error {
	a, err := s.Store.Read().Approvals().Get(ctx, id)
	if err != nil || a == nil || a.Status != domain.ApprovalPending || !a.AttemptedAt.IsZero() || !booksToUser(a.Kind) {
		return err
	}
	return s.notPurged(ctx, a.Payload["user_id"])
}

// decisionDetails is a decision's audit detail: how it ended and its
// journal, and for a deposit of nobody the deposit, the user it is
// credited to and its address's holder (review ㉕).
func decisionDetails(a domain.Approval) string {
	d := map[string]string{"status": a.Status, "result": a.Result, "mode": a.Mode, "journal_id": a.JournalID}
	if a.Kind == domain.KindDepositAssign {
		for _, k := range []string{"deposit_id", "user_id", "former_holder", "address_owner"} {
			if v := a.Payload[k]; v != "" {
				d[k] = v
			}
		}
	}
	out, _ := json.Marshal(d)
	return string(out)
}

// decidedAlike reports whether the decider took this decision already: a
// repeated request finds the operation as the decision left it.
func decidedAlike(a domain.Approval, decider string, approve bool) bool {
	if a.Status == domain.ApprovalPending || a.DecidedBy != decider {
		return false
	}
	return approve == (a.Status != domain.ApprovalRejected)
}

// execute books an operation. An error means the outcome is unknown: it
// stays pending and the idempotency key approval:<id> makes a second
// attempt safe, as the journal's memo comes from the request alone (the
// ledger compares it; the decider's reason is in the audit trail). A
// refusal marks it FAILED.
func (s *Service) execute(ctx context.Context, a *domain.Approval, p Principal) error {
	if changeKind(a.Kind) {
		run := s.executeSim
		switch a.Kind {
		case domain.KindWelcomeCredit:
			run = s.executeWelcome
		case domain.KindMarginParams:
			run = s.executeMarginParams
		case domain.KindMarginLiquidate:
			run = s.executeMarginLiquidate
		case domain.KindHouseCaps:
			run = s.executeHouseCaps
		}
		result, err := run(ctx, *a, p)
		if err != nil {
			var e *apperr.Error
			if !errors.As(err, &e) || e.Kind == apperr.KindUnavailable || e.Kind == apperr.KindInternal {
				return err
			}
			a.Status, a.Result = domain.ApprovalFailed, e.Code+": "+e.Message
			return nil
		}
		a.Status, a.Result = domain.ApprovalExecuted, result
		return nil
	}
	if a.Kind == domain.KindSimMint {
		return s.mintBots(ctx, a, p)
	}
	amount, err := decimal.NewFromString(a.Payload["amount"])
	if err != nil {
		return err
	}
	key, note := "approval:"+a.ID, a.Reason
	if ref := a.Payload["reference"]; ref != "" {
		note += " [" + ref + "]"
	}
	var journal, deposit string
	switch a.Kind {
	case domain.KindInsuranceFund:
		journal, err = s.Ledger.FundInsurance(ctx, key, a.Payload["asset"], amount, p.Admin.Email, note)
	case domain.KindDepositBackfill:
		deposit, err = s.backfill(ctx, *a)
	case domain.KindDepositAssign:
		journal, err = s.assign(ctx, *a, p)
	default:
		journal, err = s.Ledger.Adjust(ctx, key, a.Payload["user_id"], accountOf(*a), a.Payload["asset"], amount, p.Admin.Email, note)
	}
	if err != nil {
		var e *apperr.Error
		if !errors.As(err, &e) || e.Kind == apperr.KindUnavailable || e.Kind == apperr.KindInternal {
			return err
		}
		a.Status, a.Result = domain.ApprovalFailed, e.Code+": "+e.Message
		return nil
	}
	if deposit != "" {
		// The ledger books it when the wallet's processor sends it, shortly.
		a.Status, a.Result = domain.ApprovalExecuted, "deposit "+deposit
		return nil
	}
	a.Status, a.Result, a.JournalID = domain.ApprovalExecuted, "journal "+journal, journal
	return nil
}

// backfill books an approved deposit backfill in wallet-service and
// returns the deposit's ID; the same backfill again finds its deposit.
func (s *Service) backfill(ctx context.Context, a domain.Approval) (string, error) {
	b, err := backfillOf(a)
	if err != nil {
		return "", err
	}
	raw, err := s.Deposits.BookManual(ctx, b, a.Reason)
	if err != nil {
		return "", err
	}
	var d struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &d); err != nil || d.ID == "" {
		return "", apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "wallet-service answered badly")
	}
	return d.ID, nil
}

// assign credits an approved DEPOSIT_ASSIGN's deposit to its user in
// wallet-service and returns the journal that released it. Again after a
// lost answer: wallet-service refuses a deposit that has its user
// (COMMON_CONFLICT), and the deposit, credited to this same user, says it
// was done (the ledger's release depends on the deposit and the user
// alone, so it was booked once).
func (s *Service) assign(ctx context.Context, a domain.Approval, p Principal) (string, error) {
	id, user := a.Payload["deposit_id"], a.Payload["user_id"]
	raw, err := s.Deposits.Assign(ctx, id, user, p.Admin.Email, a.Reason)
	if err != nil {
		if apperr.From(err).Kind == apperr.KindConflict {
			if cur, gerr := s.Deposits.Get(ctx, id); gerr == nil {
				if journal, ok := assignedTo(cur, user); ok {
					return journal, nil
				}
			}
		}
		return "", err
	}
	journal, ok := assignedTo(raw, user)
	if !ok {
		return "", apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "wallet-service answered badly")
	}
	return journal, nil
}

// assignedTo is the journal that released a deposit to user ("", false
// when it was not released to them).
func assignedTo(raw json.RawMessage, user string) (string, bool) {
	var d struct {
		UserID           string  `json:"user_id"`
		ReleaseJournalID *string `json:"release_journal_id"`
	}
	if json.Unmarshal(raw, &d) != nil || !strings.EqualFold(d.UserID, user) || d.ReleaseJournalID == nil || *d.ReleaseJournalID == "" {
		return "", false
	}
	return *d.ReleaseJournalID, true
}

// settings returns the console's settings, the defaults until changed.
func (s *Service) settings(ctx context.Context, r ports.Repos) (domain.Settings, error) {
	set, err := r.Settings().Get(ctx)
	if err != nil || set == nil {
		return domain.DefaultSettings(), err
	}
	return *set, nil
}

// SettingsView is the console's settings with the caller's single-person
// total of the last 24 hours.
type SettingsView struct {
	TwoPerson bool
	domain.Settings
	Used decimal.Decimal
	// DelayFloor is the least ChangeDelay may be set to; ChangeDelay is
	// the wait in effect (never below it).
	DelayFloor time.Duration
}

// Settings returns the console's settings; every administrator may read
// them.
func (s *Service) Settings(ctx context.Context, p Principal) (SettingsView, error) {
	r := s.Store.Read()
	set, err := s.settings(ctx, r)
	if err != nil {
		return SettingsView{}, err
	}
	used, err := r.Approvals().SingleUsage(ctx, p.Admin.ID, s.Now().Add(-24*time.Hour))
	if err != nil {
		return SettingsView{}, err
	}
	set.ChangeDelay = max(set.ChangeDelay, s.delayFloor())
	return SettingsView{TwoPerson: s.TwoPerson(), Settings: set, Used: used, DelayFloor: s.delayFloor()}, nil
}

// SettingsPatch changes some settings; nil fields stay.
type SettingsPatch struct {
	TwoPerson     *bool
	SingleMax     *decimal.Decimal
	DailyMax      *decimal.Decimal
	WithdrawalMax *decimal.Decimal
	// ChangeDelay is the wait of trading parameters' changes.
	ChangeDelay *time.Duration
	Reason      string
}

// UpdateSettings changes the console's settings: the limits with an audit
// event (admin.settings.changed), two-person approval through its flag
// (audited by the flags).
func (s *Service) UpdateSettings(ctx context.Context, p Principal, in SettingsPatch) (SettingsView, error) {
	if err := p.require(domain.PermSettingsEdit); err != nil {
		return SettingsView{}, err
	}
	if err := needReason(in.Reason); err != nil {
		return SettingsView{}, err
	}
	if in.SingleMax != nil || in.DailyMax != nil || in.WithdrawalMax != nil || in.ChangeDelay != nil {
		err := s.Store.Tx(ctx, func(r ports.Repos) error {
			before, err := s.settings(ctx, r)
			if err != nil {
				return err
			}
			after := before
			for _, f := range []struct {
				in  *decimal.Decimal
				out *decimal.Decimal
			}{{in.SingleMax, &after.SingleMax}, {in.DailyMax, &after.DailyMax}, {in.WithdrawalMax, &after.WithdrawalMax}} {
				if f.in != nil {
					*f.out = *f.in
				}
			}
			if in.ChangeDelay != nil {
				if floor := s.delayFloor(); *in.ChangeDelay < floor {
					return apperr.Invalid(fmt.Sprintf("change_delay_seconds must be at least %d", int64(floor/time.Second)))
				}
				after.ChangeDelay = *in.ChangeDelay
			}
			if err := after.Validate(); err != nil {
				return err
			}
			after.UpdatedBy, after.UpdatedAt = p.Admin.Email, s.Now()
			if err := r.Settings().Put(ctx, after); err != nil {
				return err
			}
			return r.Audit(ctx, &auditv1.AdminActionPerformed{
				Target: "settings:limits", Action: "admin.settings.changed", Actor: p.Admin.Email, Reason: strings.TrimSpace(in.Reason),
				Details: fmt.Sprintf(`{"before":%s,"after":%s}`, limitsJSON(before), limitsJSON(after)),
			}, p.Admin.Email)
		})
		if err != nil {
			return SettingsView{}, err
		}
	}
	if in.TwoPerson != nil && *in.TwoPerson != s.TwoPerson() {
		if _, err := s.Flags.Switch(ctx, flags.KeyTwoPerson, *in.TwoPerson, p.Admin.Email, strings.TrimSpace(in.Reason)); err != nil {
			return SettingsView{}, err
		}
		// This instance applies it at once; the others within 5 seconds.
		if f, ok := s.Features.(interface{ Refresh(context.Context) error }); ok {
			if err := f.Refresh(ctx); err != nil {
				s.Log.WarnContext(ctx, "settings: refresh flags", "error", err)
			}
		}
	}
	return s.Settings(ctx, p)
}

func limitsJSON(s domain.Settings) string {
	return fmt.Sprintf(`{"single_max_usdt":%q,"daily_max_usdt":%q,"withdrawal_max_usdt":%q,"change_delay_seconds":%d}`,
		s.SingleMax.String(), s.DailyMax.String(), s.WithdrawalMax.String(), int64(s.ChangeDelay/time.Second))
}

// orEmptyList is a JSON list kept in a payload, [] when there is none.
// orEmptyObject is a JSON object kept as text, {} when there is none.
func orEmptyObject(v string) string {
	if v == "" {
		return "{}"
	}
	return v
}

func orEmptyList(v string) string {
	if v == "" {
		return "[]"
	}
	return v
}
