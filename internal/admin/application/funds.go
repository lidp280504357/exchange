package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/flags"
	"github.com/lidp280504357/exchange/internal/platform/pagecursor"
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

// fundActions names the audit actions of each kind of fund operation.
var fundActions = map[string]struct{ requested, approved, rejected, executed, failed string }{
	domain.KindLedgerAdjustment: {
		"admin.ledger.adjustment_requested", "admin.ledger.adjustment_approved", "admin.ledger.adjustment_rejected",
		"admin.ledger.adjustment_executed", "admin.ledger.adjustment_failed",
	},
	domain.KindInsuranceFund: {
		"admin.derivatives.insurance_requested", "admin.derivatives.insurance_approved", "admin.derivatives.insurance_rejected",
		"admin.derivatives.insurance_executed", "admin.derivatives.insurance_failed",
	},
	domain.KindDepositBackfill: {
		"admin.deposits.backfill_requested", "admin.deposits.backfill_approved", "admin.deposits.backfill_rejected",
		"admin.deposits.backfill_executed", "admin.deposits.backfill_failed",
	},
	domain.KindSimEvent: {
		"admin.sim.event_requested", "admin.sim.event_approved", "admin.sim.event_rejected", "admin.sim.event_created", "admin.sim.event_failed",
	},
	domain.KindSimParams: {
		"admin.sim.params_requested", "admin.sim.params_approved", "admin.sim.params_rejected", "admin.sim.params_changed",
		"admin.sim.params_failed",
	},
	domain.KindSimMint: {
		"admin.sim.mint_requested", "admin.sim.mint_approved", "admin.sim.mint_rejected", "admin.sim.mint_executed", "admin.sim.mint_failed",
	},
}

// simKind reports whether an approval is a simulated market's change.
func simKind(kind string) bool { return kind == domain.KindSimEvent || kind == domain.KindSimParams }

// fundTarget is the audit target of an operation.
func fundTarget(a domain.Approval) string {
	switch {
	case a.Kind == domain.KindInsuranceFund:
		return "insurance:" + a.Payload["asset"]
	case simKind(a.Kind), a.Kind == domain.KindSimMint:
		return simAuditTarget
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
	// Direct carries it out at once when single-person mode and its limits
	// allow; without it a second administrator always decides.
	Direct bool
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
		if _, err := uuid.Parse(in.UserID); err != nil {
			return apperr.Invalid("user_id must be a UUID")
		}
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
// When the ledger does not answer, the operation stays PENDING (single
// mode: its requester may finish it later) and the error names it.
func (s *Service) SubmitFunds(ctx context.Context, p Principal, in FundRequest) (domain.Approval, error) {
	perm := domain.PermAdjustRequest
	if in.Kind == domain.KindDepositBackfill {
		perm = domain.PermDepositsReview
	}
	if err := p.require(perm); err != nil {
		return domain.Approval{}, err
	}
	if err := in.validate(); err != nil {
		return domain.Approval{}, err
	}
	var check ports.ManualCheck
	if in.Kind == domain.KindDepositBackfill {
		in.Backfill.Actor = p.Admin.Email
		var err error
		if check, err = s.Deposits.CheckManual(ctx, *in.Backfill); err != nil {
			return domain.Approval{}, err
		}
		in.Asset = check.Asset
	}
	now := s.Now()
	a := domain.Approval{
		ID: uuid.Must(uuid.NewV7()).String(), Kind: in.Kind, Reason: strings.TrimSpace(in.Reason),
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
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
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
				a.Mode = domain.ModeSingle
			}
		}
		if err := r.Approvals().Insert(ctx, a); err != nil {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: fundTarget(a), Action: actions.requested, Actor: p.Admin.Email, Reason: a.Reason, Details: fundDetails(a),
		}, p.Admin.Email)
	})
	if err != nil || a.Mode != domain.ModeSingle {
		return a, err
	}
	if err := s.execute(ctx, &a, p); err != nil {
		var e *apperr.Error
		if errors.As(err, &e) {
			err = e.WithDetail("approval_id", a.ID)
		}
		return domain.Approval{}, err
	}
	return s.record(ctx, a, p, actions.executed, actions.failed)
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
	if simKind(a.Kind) {
		d, _ := json.Marshal(map[string]any{
			"approval_id": a.ID, "change": json.RawMessage(a.Payload["change"]), "move": a.Payload["move"], "mode": a.Mode,
			"escalation": a.Escalation, "status": a.Status, "result": a.Result,
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
	case domain.KindSimMint:
		account = fmt.Sprintf(`"role":%q,"bots":%s,`, a.Payload["role"], a.Payload["bots"])
	}
	return fmt.Sprintf(`{"approval_id":%q,%s"asset":%q,"amount":%q,"mode":%q,"escalation":%q,"value_usdt":%s,"status":%q,"result":%q}`,
		a.ID, account, a.Payload["asset"], a.Payload["amount"], a.Mode, a.Escalation, value, a.Status, a.Result)
}

// worth values an amount of an asset in USDT at its USDT pair's last
// price; false without one.
func (s *Service) worth(ctx context.Context, asset string, amount decimal.Decimal) (decimal.Decimal, bool) {
	amount = amount.Abs()
	if asset == "USDT" {
		return amount, true
	}
	if s.Prices == nil {
		return decimal.Zero, false
	}
	prices, err := s.Prices.Prices(ctx)
	if err != nil {
		s.Log.WarnContext(ctx, "fund operation: no prices", "error", err)
		return decimal.Zero, false
	}
	px, ok := prices[asset+"-USDT"]
	if !ok {
		return decimal.Zero, false
	}
	return amount.Mul(px).Round(2), true
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
	if err != nil || len(list) <= limit {
		return list, "", err
	}
	list = list[:limit]
	last := list[limit-1]
	return list, pagecursor.Encode(last.CreatedAt, last.ID), nil
}

// DecideApproval approves (and carries out) or rejects a pending
// operation: another administrator's request, the decider's own
// single-person operation whose outcome was unknown, or (rejecting) the
// decider's own request. The row stays locked while the ledger books it,
// so a second decision waits and then finds it decided.
func (s *Service) DecideApproval(ctx context.Context, p Principal, id string, approve bool, reason string) (domain.Approval, error) {
	// A fund operation needs ledger.adjust.approve, a simulated market's
	// change sim.control (checked once the request is read).
	if p.require(domain.PermAdjustApprove) != nil && p.require(domain.PermSimControl) != nil {
		return domain.Approval{}, p.require(domain.PermAdjustApprove)
	}
	if err := needReason(reason); err != nil {
		return domain.Approval{}, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return domain.Approval{}, apperr.NotFound("no such request")
	}
	var a domain.Approval
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Approvals().GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if cur == nil {
			return apperr.NotFound("no such request")
		}
		perm := domain.PermAdjustApprove
		if simKind(cur.Kind) {
			perm = domain.PermSimControl
		}
		if err := p.require(perm); err != nil {
			return err
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
		case simKind(a.Kind) && !a.DecidedAt.Before(simExpiry(a)):
			// Lapsed: nothing goes to market-sim (C5.5 ④).
			action = actions.failed
			a.Status, a.Result = domain.ApprovalFailed, "expired at "+simExpiry(a).UTC().Format(time.RFC3339)
		default:
			action = actions.approved
			if err := s.execute(ctx, &a, p); err != nil {
				return err
			}
		}
		if err := r.Approvals().Update(ctx, a); err != nil {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "approval:" + a.ID, Action: action, Actor: p.Admin.Email, Reason: reason,
			Details: fmt.Sprintf(`{"status":%q,"result":%q,"mode":%q}`, a.Status, a.Result, a.Mode),
		}, p.Admin.Email)
	})
	if err != nil {
		return domain.Approval{}, err
	}
	if got, err := s.Store.Read().Approvals().Get(ctx, a.ID); err == nil && got != nil {
		return *got, nil
	}
	return a, nil
}

// execute books an operation. An error means the outcome is unknown: it
// stays pending and the idempotency key approval:<id> makes a second
// attempt safe, as the journal's memo comes from the request alone (the
// ledger compares it; the decider's reason is in the audit trail). A
// refusal marks it FAILED.
func (s *Service) execute(ctx context.Context, a *domain.Approval, p Principal) error {
	if simKind(a.Kind) {
		result, err := s.executeSim(ctx, *a, p)
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
	return SettingsView{TwoPerson: s.TwoPerson(), Settings: set, Used: used}, nil
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
