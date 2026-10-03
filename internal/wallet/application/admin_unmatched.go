package application

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	walletv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/wallet/v1"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
)

// AssignDeposit credits a deposit of nobody (a custodian's deposit to an
// address no user has, booked to UNCLAIMED_DEPOSIT, B7a) to the user an
// administrator found it belongs to, in one audited operation: the
// deposit gets that owner and the ledger releases it to them
// (ReleaseUnclaimed, keyed by the deposit: a repeat books nothing twice,
// and naming another user then is refused). The user must be one who may
// take deposits now. The console asks a second administrator above the
// usual limit before it calls this.
func (s *Service) AssignDeposit(ctx context.Context, id, userID, actor, reason string) (domain.Deposit, error) {
	if err := needDecider(actor, reason); err != nil {
		return domain.Deposit{}, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return domain.Deposit{}, apperr.NotFound("no such deposit")
	}
	if u, err := uuid.Parse(userID); err != nil || u == uuid.Nil {
		return domain.Deposit{}, apperr.Invalid("name the user, by ID, the deposit is credited to")
	}
	userID = strings.ToLower(userID)
	allowed, code, err := s.Eligibility.Check(ctx, userID, FeatureDeposit)
	if err != nil {
		return domain.Deposit{}, err
	}
	if !allowed {
		return domain.Deposit{}, apperr.New(apperr.KindForbidden, code, "the user may not take deposits now")
	}
	var out domain.Deposit
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Deposits().GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if cur == nil {
			return apperr.NotFound("no such deposit")
		}
		if cur.UserID != domain.NoOwner {
			return apperr.New(apperr.KindConflict, apperr.CodeConflict, "the deposit has its user: credit it as it is")
		}
		cur.UserID = userID
		if err := cur.Releasable(); err != nil {
			return err
		}
		journal, err := s.W.Ledger.ReleaseUnclaimed(ctx, cur.ID, userID, cur.Asset, cur.Amount, actor, strings.TrimSpace(reason))
		if err != nil {
			return err
		}
		if err := cur.Release(journal, actor, strings.TrimSpace(reason), s.Now()); err != nil {
			return err
		}
		out = *cur
		if err := r.Deposits().Update(ctx, out); err != nil {
			return err
		}
		former, err := r.Addresses().RetiredOwner(ctx, out.Network, out.Address)
		if err != nil {
			return err
		}
		details, _ := json.Marshal(map[string]string{
			"deposit_id": out.ID, "asset": out.Asset, "amount": out.Amount.String(), "address": out.Address, "network": out.Network,
			"journal_id": journal, "retired_owner": former,
		})
		if err := r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "user:" + userID, Action: "wallet.deposit.assigned", Actor: actor, Reason: strings.TrimSpace(reason),
			Details: string(details),
		}, actor); err != nil {
			return err
		}
		return r.Emit(ctx, &walletv1.DepositCredited{Deposit: ToProto(out), JournalId: journal}, userID)
	})
	return out, err
}

// AddressOwner is who an address of network belongs to, for a deposit of
// nobody: its user now (an address assigned after its deposit came), else
// the one it was retired from, "" when neither.
func (s *Service) AddressOwner(ctx context.Context, network, address string) (owner string, retired bool, err error) {
	r := s.Store.Read()
	if owner, err = r.Addresses().Owner(ctx, network, address); err != nil || owner != "" {
		return owner, false, err
	}
	owner, err = r.Addresses().RetiredOwner(ctx, network, address)
	return owner, owner != "", err
}
