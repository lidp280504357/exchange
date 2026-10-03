package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	walletv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/wallet/v1"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
)

// FeatureWithdraw is the eligibility withdrawals need (it carries the
// wallet.withdraw switch, ADR-0005).
const FeatureWithdraw = "WITHDRAW"

// Withdrawals are the wallet's withdrawal dependencies and settings
// (§5.10, §11.6).
type Withdrawals struct {
	StepUps  ports.StepUps
	Profiles ports.Profiles
	Prices   ports.Prices
	Ledger   ports.Ledger
	// Cooldown is the cooling-off period of a new address (24 hours).
	Cooldown time.Duration
}

// AddressInput is a new entry of the address book.
type AddressInput struct {
	Network string
	Address string
	Label   string
	StepUp  string
}

// AddAddress adds an address to the user's book after a step-up; it can
// be used once its cooling-off period ends. The same address again
// returns the entry it has. The address is checked like ValidateAddress
// does (its form, the custodian, not the user's own).
func (s *Service) AddAddress(ctx context.Context, userID string, in AddressInput) (domain.WithdrawAddress, error) {
	label := strings.TrimSpace(in.Label)
	if len(label) > 50 {
		return domain.WithdrawAddress{}, apperr.Invalid("the label has at most 50 characters")
	}
	v, err := s.ValidateAddress(ctx, userID, "", in.Network, in.Address, "")
	switch {
	case err != nil:
		return domain.WithdrawAddress{}, err
	case v.Reason == domain.ReasonAddressOwn:
		return domain.WithdrawAddress{}, domain.ErrOwnAddress
	case !v.Valid:
		return domain.WithdrawAddress{}, domain.ErrInvalidAddress.WithDetail("reason", v.Reason)
	}
	in.Address = v.Normalized
	r := s.Store.Read()
	if known, err := r.WithdrawAddresses().Find(ctx, userID, in.Network, in.Address); err != nil || known != nil {
		if known != nil {
			return *known, nil
		}
		return domain.WithdrawAddress{}, err
	}
	if _, err := s.W.StepUps.Consume(ctx, userID, in.StepUp); err != nil {
		return domain.WithdrawAddress{}, err
	}
	now := s.Now()
	a := domain.WithdrawAddress{
		ID: uuid.Must(uuid.NewV7()).String(), UserID: userID, Network: in.Network, Address: in.Address, Label: label,
		CreatedAt: now, UsableAt: now.Add(s.W.Cooldown),
	}
	return a, s.Store.Tx(ctx, func(r ports.Repos) error { return r.WithdrawAddresses().Insert(ctx, a) })
}

// Addresses lists the user's address book.
func (s *Service) Addresses(ctx context.Context, userID string) ([]domain.WithdrawAddress, error) {
	return s.Store.Read().WithdrawAddresses().List(ctx, userID)
}

// DeleteAddress removes an entry of the user's book.
func (s *Service) DeleteAddress(ctx context.Context, userID, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return apperr.NotFound("no such address")
	}
	ok, err := s.Store.Read().WithdrawAddresses().Delete(ctx, userID, id, s.Now())
	if err == nil && !ok {
		err = apperr.NotFound("no such address")
	}
	return err
}

// WithdrawalInput is a withdrawal request.
type WithdrawalInput struct {
	Asset   string
	Network string
	Address string
	Amount  decimal.Decimal
	StepUp  string
}

// price returns the day's USDT price of asset, fixed at its first use of
// the UTC day (§11.6).
func (s *Service) price(ctx context.Context, asset string, now time.Time) (decimal.Decimal, error) {
	if asset == "USDT" {
		return decimal.NewFromInt(1), nil
	}
	day := now.UTC().Truncate(24 * time.Hour)
	if p, err := s.Store.Read().Prices().Get(ctx, day, asset); err != nil || p != nil {
		if p != nil {
			return *p, nil
		}
		return decimal.Zero, err
	}
	p, source, err := s.W.Prices.USDT(ctx, asset)
	if err != nil {
		return decimal.Zero, err
	}
	var held decimal.Decimal
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		var err error
		held, err = r.Prices().Put(ctx, day, asset, p, source)
		return err
	})
	return held, err
}

// RequestWithdrawal checks a withdrawal (§11.6: eligibility, address book
// and cooling-off, minimum, limits, step-up), scores its risk and freezes
// the amount and fee. It comes back APPROVED or PENDING_REVIEW; a freeze
// the ledger refuses leaves it REJECTED and fails with the ledger's code.
func (s *Service) RequestWithdrawal(ctx context.Context, userID string, in WithdrawalInput) (domain.Withdrawal, error) {
	net, err := s.Networks.Network(ctx, in.Asset, in.Network)
	if err != nil {
		return domain.Withdrawal{}, err
	}
	check := domain.CheckAddress(net, in.Address, "")
	switch {
	case !net.WithdrawEnabled || (net.Contract != "" && !net.Custody()):
		// The signer sends only the chain's coin; the custodian sends tokens too.
		return domain.Withdrawal{}, domain.ErrWithdrawClosed
	case !check.Valid:
		return domain.Withdrawal{}, domain.ErrInvalidAddress.WithDetail("reason", check.Reason)
	case !in.Amount.IsPositive() || !in.Amount.Equal(in.Amount.Truncate(net.Decimals)):
		return domain.Withdrawal{}, apperr.New(apperr.KindInvalid, "WALLET_AMOUNT_PRECISION", "the amount must be positive within the asset's decimals").
			WithDetail("decimals", net.Decimals)
	case in.Amount.LessThan(net.MinWithdraw):
		return domain.Withdrawal{}, domain.ErrBelowMinimum.WithDetail("min_withdraw", net.MinWithdraw.String())
	}
	if x, err := s.Store.Read().Suspensions().Get(ctx, net.Asset); err != nil || x != nil {
		if err == nil {
			err = domain.ErrWithdrawSuspended
		}
		return domain.Withdrawal{}, err
	}
	allowed, reason, err := s.Eligibility.Check(ctx, userID, FeatureWithdraw)
	if err != nil {
		return domain.Withdrawal{}, err
	}
	if !allowed {
		return domain.Withdrawal{}, apperr.New(apperr.KindForbidden, reason, "withdrawals are not available to this account now")
	}
	in.Address = check.Normalized
	r := s.Store.Read()
	entry, err := r.WithdrawAddresses().Find(ctx, userID, in.Network, in.Address)
	if err != nil {
		return domain.Withdrawal{}, err
	}
	now := s.Now()
	switch {
	case entry == nil:
		return domain.Withdrawal{}, domain.ErrNotWhitelisted
	case now.Before(entry.UsableAt):
		return domain.Withdrawal{}, domain.ErrCooldown.WithDetail("usable_at", entry.UsableAt.UTC().Format(time.RFC3339))
	}
	owners, err := r.Addresses().Owners(ctx, in.Network)
	if err != nil {
		return domain.Withdrawal{}, err
	}
	payee := owners[strings.ToLower(in.Address)]
	if payee == userID {
		return domain.Withdrawal{}, domain.ErrOwnAddress
	}
	fee := net.WithdrawFee
	if payee != "" {
		fee = decimal.Zero // internal transfers carry no chain fee (§11.6)
	}
	price, err := s.price(ctx, in.Asset, now)
	if err != nil {
		return domain.Withdrawal{}, err
	}
	value := in.Amount.Mul(price)
	su, err := s.W.StepUps.Consume(ctx, userID, in.StepUp)
	if err != nil {
		return domain.Withdrawal{}, err
	}
	created, err := s.W.Profiles.Created(ctx, userID)
	if err != nil {
		return domain.Withdrawal{}, err
	}
	limits := domain.LimitsFor(su.Identities, su.TOTPEnabled)
	today, err := r.Withdrawals().ValueSince(ctx, userID, now.UTC().Truncate(24*time.Hour))
	if err != nil {
		return domain.Withdrawal{}, err
	}
	y, m, _ := now.UTC().Date()
	month, err := r.Withdrawals().ValueSince(ctx, userID, time.Date(y, m, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		return domain.Withdrawal{}, err
	}
	if today.Add(value).GreaterThan(limits.Daily) || month.Add(value).GreaterThan(limits.Monthly) {
		return domain.Withdrawal{}, domain.ErrLimitExceeded.WithDetail("daily_limit", limits.Daily.String()).
			WithDetail("monthly_limit", limits.Monthly.String()).WithDetail("used_today", today.String()).
			WithDetail("used_this_month", month.String()).WithDetail("value_usdt", value.String())
	}
	risk := domain.Assess(domain.RiskInput{
		Now: now, AccountCreated: created, DeviceFirstSeen: su.DeviceFirstSeen, IdentityChanged: su.IdentityChanged,
		PasswordChanged: su.PasswordChanged, TOTPChanged: su.TOTPChanged, AddressAdded: entry.CreatedAt,
		ValueUSDT: value, DailyUSDT: today.Add(value), DailyLimit: limits.Daily,
	})
	w := domain.Withdrawal{
		ID: uuid.Must(uuid.NewV7()).String(), UserID: userID, Asset: in.Asset, Network: in.Network, Address: in.Address,
		Amount: in.Amount, Fee: fee, InternalUserID: payee, Provider: net.Provider, Status: domain.WithdrawalRequested, RiskScore: risk.Score,
		RiskReasons: risk.Reasons, ApprovalsRequired: risk.Approvals, ValueUSDT: value, Nonce: -1, Required: max(net.Confirmations, 1),
		CreatedAt: now, UpdatedAt: now,
	}
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.Withdrawals().Insert(ctx, w); err != nil {
			return err
		}
		return r.EmitWithdrawal(ctx, &walletv1.WithdrawalRequested{Withdrawal: WithdrawalProto(w)}, userID)
	})
	if err != nil {
		return domain.Withdrawal{}, err
	}
	return FreezeWithdrawal(ctx, s.Store, s.W.Ledger, w.ID, s.Now)
}

// FreezeWithdrawal freezes a REQUESTED withdrawal's funds and applies its
// stored risk result; the processor calls it again for one left
// REQUESTED by an outage (the ledger key makes it once).
func FreezeWithdrawal(ctx context.Context, store ports.Store, ledger ports.Ledger, id string, now func() time.Time) (domain.Withdrawal, error) {
	w, err := store.Read().Withdrawals().Get(ctx, id)
	if err != nil || w == nil {
		return domain.Withdrawal{}, errors.Join(err, errors.New("withdrawal vanished"))
	}
	journal, ferr := ledger.Freeze(ctx, "withdraw:"+w.ID, w.UserID, w.Asset, w.Frozen(), w.ID)
	refused := false
	var refusal *apperr.Error
	if ferr != nil {
		refusal = apperr.From(ferr)
		switch refusal.Kind {
		case apperr.KindInvalid, apperr.KindUnprocessable, apperr.KindConflict, apperr.KindForbidden:
			refused = true
		}
	}
	if ferr != nil && !refused {
		// Unknown outcome: stays REQUESTED for the processor to finish.
		return *w, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "the withdrawal is being checked, try the list shortly").
			WithDetail("withdrawal_id", w.ID)
	}
	var out domain.Withdrawal
	err = store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Withdrawals().GetForUpdate(ctx, id)
		if err != nil || cur == nil || cur.Status != domain.WithdrawalRequested {
			if cur != nil {
				out = *cur
			}
			return err
		}
		if refused {
			cur.RejectReason = refusal.Code
			cur.Status, cur.UpdatedAt = domain.WithdrawalRejected, now()
			out = *cur
			if err := r.Withdrawals().Update(ctx, *cur); err != nil {
				return err
			}
			return r.EmitWithdrawal(ctx, &walletv1.WithdrawalRejected{Withdrawal: WithdrawalProto(*cur)}, cur.UserID)
		}
		cur.FreezeJournal = journal
		cur.Scored(domain.RiskResult{Score: cur.RiskScore, Reasons: cur.RiskReasons, Approvals: cur.ApprovalsRequired}, now())
		out = *cur
		if err := r.Withdrawals().Update(ctx, *cur); err != nil {
			return err
		}
		if err := r.EmitWithdrawal(ctx, &walletv1.WithdrawalRiskScored{
			Withdrawal: WithdrawalProto(*cur), Score: int32(cur.RiskScore), ApprovalsRequired: int32(cur.ApprovalsRequired), //nolint:gosec // small
		}, cur.UserID); err != nil {
			return err
		}
		if cur.Status != domain.WithdrawalApproved {
			return nil
		}
		return r.EmitWithdrawal(ctx, &walletv1.WithdrawalApproved{Withdrawal: WithdrawalProto(*cur), ApprovedBy: []string{riskEngine}}, cur.UserID)
	})
	if err != nil {
		return domain.Withdrawal{}, err
	}
	if refused {
		return out, ferr
	}
	return out, nil
}

// riskEngine approves the withdrawals the risk rules let through.
const riskEngine = "risk-engine"

// Withdrawal returns one of the user's withdrawals.
func (s *Service) Withdrawal(ctx context.Context, userID, id string) (domain.Withdrawal, error) {
	if _, err := uuid.Parse(id); err != nil {
		return domain.Withdrawal{}, apperr.NotFound("no such withdrawal")
	}
	w, err := s.Store.Read().Withdrawals().Get(ctx, id)
	if err != nil {
		return domain.Withdrawal{}, err
	}
	if w == nil || w.UserID != userID {
		return domain.Withdrawal{}, apperr.NotFound("no such withdrawal")
	}
	return *w, nil
}

// Withdrawals returns a page of the user's withdrawals, newest first.
func (s *Service) Withdrawals(ctx context.Context, userID, cursor string, limit int) ([]domain.Withdrawal, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if cursor != "" {
		if _, err := uuid.Parse(cursor); err != nil {
			return nil, "", apperr.Invalid("cursor must be a withdrawal ID")
		}
	}
	list, err := s.Store.Read().Withdrawals().ByUser(ctx, userID, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(list) > limit {
		list = list[:limit]
		next = list[limit-1].ID
	}
	return list, next, nil
}

// CancelWithdrawal withdraws a request before it is signed and releases
// its funds (the processor retries the release if the ledger fails).
func (s *Service) CancelWithdrawal(ctx context.Context, userID, id string) (domain.Withdrawal, error) {
	if _, err := uuid.Parse(id); err != nil {
		return domain.Withdrawal{}, apperr.NotFound("no such withdrawal")
	}
	var w domain.Withdrawal
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Withdrawals().GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if cur == nil || cur.UserID != userID {
			return apperr.NotFound("no such withdrawal")
		}
		if err := cur.Cancel(s.Now()); err != nil {
			return err
		}
		w = *cur
		if err := r.Withdrawals().Update(ctx, w); err != nil {
			return err
		}
		return r.EmitWithdrawal(ctx, &walletv1.WithdrawalCanceled{Withdrawal: WithdrawalProto(w)}, userID)
	})
	if err != nil {
		return domain.Withdrawal{}, err
	}
	if released, err := ReleaseWithdrawal(ctx, s.Store, s.W.Ledger, w); err == nil {
		w = released
	}
	return w, nil
}

// ReleaseWithdrawal returns a refused withdrawal's frozen funds.
func ReleaseWithdrawal(ctx context.Context, store ports.Store, ledger ports.Ledger, w domain.Withdrawal) (domain.Withdrawal, error) {
	if !w.NeedsRelease() {
		return w, nil
	}
	journal, err := ledger.Unfreeze(ctx, "withdraw-release:"+w.ID, w.UserID, w.Asset, w.Frozen(), w.ID)
	if err != nil {
		return w, err
	}
	err = store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Withdrawals().GetForUpdate(ctx, w.ID)
		if err != nil || cur == nil {
			return err
		}
		cur.UnfreezeJournal, cur.UpdatedAt = journal, time.Now()
		w = *cur
		return r.Withdrawals().Update(ctx, *cur)
	})
	return w, err
}

// Review is a reviewer's decision on a withdrawal waiting for review.
type Review struct {
	ID       string
	Reviewer string
	Reason   string
	Approve  bool
	// SoleMax, when positive, lets this approval alone complete a
	// withdrawal worth at most that much in USDT (the admin console's
	// single-person mode).
	SoleMax decimal.Decimal
}

// ReviewWithdrawal records a reviewer's approval or rejection (until the
// admin console, from exchangectl) with an audit event. Rejected funds
// are released by the processor.
func ReviewWithdrawal(ctx context.Context, store ports.Store, rv Review, now time.Time) (domain.Withdrawal, error) {
	if _, err := uuid.Parse(rv.ID); err != nil {
		return domain.Withdrawal{}, apperr.NotFound("no such withdrawal")
	}
	if len(strings.TrimSpace(rv.Reason)) < 3 {
		return domain.Withdrawal{}, apperr.Invalid("a reason is required")
	}
	var w domain.Withdrawal
	err := store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Withdrawals().GetForUpdate(ctx, rv.ID)
		if err != nil {
			return err
		}
		if cur == nil {
			return apperr.NotFound("no such withdrawal")
		}
		action := "wallet.withdrawal.reject"
		if rv.Approve {
			action = "wallet.withdrawal.approve"
			done, err := cur.ApproveAlone(rv.Reviewer, rv.SoleMax, now)
			if err != nil {
				return err
			}
			if done {
				if err := r.EmitWithdrawal(ctx, &walletv1.WithdrawalApproved{Withdrawal: WithdrawalProto(*cur), ApprovedBy: cur.Approvals}, cur.UserID); err != nil {
					return err
				}
			}
		} else {
			if err := cur.Reject("REVIEW: "+rv.Reason, now); err != nil {
				return err
			}
			if err := r.EmitWithdrawal(ctx, &walletv1.WithdrawalRejected{Withdrawal: WithdrawalProto(*cur)}, cur.UserID); err != nil {
				return err
			}
		}
		// A review ends a hold: the reviewer looked into it (C5.5 ⑦).
		cur.Unhold(now)
		w = *cur
		if err := r.Withdrawals().Update(ctx, w); err != nil {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "withdrawal:" + w.ID, Action: action, Actor: rv.Reviewer, Reason: rv.Reason,
			Details: fmt.Sprintf(`{"user_id":%q,"asset":%q,"amount":%q,"status":%q,"sole_max_usdt":%q}`, w.UserID, w.Asset,
				w.Amount.String(), w.Status, rv.SoleMax.String()),
		}, rv.Reviewer)
	})
	return w, err
}

// WithdrawalProto renders a withdrawal for events.
func WithdrawalProto(w domain.Withdrawal) *walletv1.Withdrawal {
	return &walletv1.Withdrawal{
		WithdrawalId: w.ID, UserId: w.UserID, Asset: w.Asset, Network: w.Network, Address: w.Address, Amount: w.Amount.String(),
		Fee: w.Fee.String(), Status: w.Status, Internal: w.InternalUserID != "", TxHash: w.TxHash, Confirmations: w.Confirmations,
		RequiredConfirmations: w.Required, RiskReasons: w.RiskReasons, RejectReason: w.RejectReason, Provider: w.Provider,
	}
}
