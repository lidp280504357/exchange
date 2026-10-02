// Package application runs wallet-service's deposits and withdrawals
// (requirements §5.10, §11.5, §11.6): per-user deposit addresses derived
// from the xpub or created by the custodian (ADR-0011), a block scanner
// that detects, confirms and orphans deposits on the platform's own
// network, the custodian's callbacks, the credit the ledger books for
// confirmed deposits, and withdrawals signed by the signer or handed to
// the custodian.
package application

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	walletv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/wallet/v1"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
)

// FeatureDeposit is the eligibility deposit addresses need.
const FeatureDeposit = "DEPOSIT"

// Service is the wallet.
type Service struct {
	Store       ports.Store
	Networks    ports.Networks
	Eligibility ports.Eligibility
	// Deriver is nil until WALLET_XPUB is configured.
	Deriver ports.Deriver
	// Custodians serve the networks whose provider they are (ADR-0011).
	Custodians map[string]ports.Custody
	// CallbackWindow is how far a callback's timestamp may be from now
	// (5 minutes).
	CallbackWindow time.Duration
	// W serves withdrawals.
	W   Withdrawals
	Log *slog.Logger
	Now func() time.Time
	// FeesRefused counts custodian fees not booked for being above the
	// amount sent (applyWithdrawal); nil counts nothing.
	FeesRefused prometheus.Counter
	// CallbacksRejected counts the callbacks refused for their signature,
	// age or form, kept or not (HandleCallback); nil counts nothing.
	CallbacksRejected prometheus.Counter
}

// DepositAddress returns the user's deposit address for an asset on a
// network, assigning the next derived address on first use. One address
// serves every asset of a network (§5.10).
func (s *Service) DepositAddress(ctx context.Context, userID, asset, network string) (domain.Address, domain.Network, error) {
	net, err := s.Networks.Network(ctx, asset, network)
	if err != nil {
		return domain.Address{}, domain.Network{}, err
	}
	if !net.Enabled {
		return domain.Address{}, net, domain.ErrDepositsDisabled
	}
	allowed, reason, err := s.Eligibility.Check(ctx, userID, FeatureDeposit)
	if err != nil {
		return domain.Address{}, net, err
	}
	if !allowed {
		return domain.Address{}, net, apperr.New(apperr.KindForbidden, reason, "deposits are not available to this account now")
	}
	if net.Custody() {
		a, err := s.custodyAddress(ctx, userID, net)
		return a, net, err
	}
	if s.Deriver == nil {
		return domain.Address{}, net, domain.ErrNotConfigured
	}
	var out domain.Address
	for attempt := 0; attempt < 2; attempt++ {
		err = s.Store.Tx(ctx, func(r ports.Repos) error {
			if a, err := r.Addresses().Get(ctx, userID, network); err != nil || a != nil {
				if a != nil {
					out = *a
				}
				return err
			}
			idx, err := r.Addresses().NextIndex(ctx, network)
			if err != nil {
				return err
			}
			addr, err := s.Deriver.Address(idx)
			if err != nil {
				return err
			}
			out = domain.Address{UserID: userID, Network: network, Index: idx, Address: addr, CreatedAt: s.Now()}
			if err := r.Addresses().Insert(ctx, out); err != nil {
				return err
			}
			return r.Emit(ctx, &walletv1.DepositAddressAssigned{
				UserId: userID, Network: network, Address: addr, DerivationIndex: idx,
			}, userID)
		})
		// A concurrent first request of the same user won: read its address.
		if _, dup := pg.UniqueViolation(err); !dup {
			break
		}
	}
	return out, net, err
}

// Deposits returns a page of the user's deposits, newest first, and the
// next cursor.
func (s *Service) Deposits(ctx context.Context, userID, cursor string, limit int) ([]domain.Deposit, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if cursor != "" {
		if _, err := uuid.Parse(cursor); err != nil {
			return nil, "", apperr.Invalid("cursor must be a deposit ID")
		}
	}
	list, err := s.Store.Read().Deposits().ByUser(ctx, userID, cursor, limit+1)
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

// DepositKey is the ledger's idempotency key of a deposit's credit.
func DepositKey(id string) string { return "deposit:" + id }

// OnCredited records the ledger's DEPOSIT_CREDIT journal of a deposit and
// publishes DepositCredited; a redelivery changes nothing.
func (s *Service) OnCredited(ctx context.Context, depositID, journalID string) error {
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		d, err := r.Deposits().GetForUpdate(ctx, depositID)
		if err != nil || d == nil {
			return err
		}
		if !d.Credit(journalID, s.Now()) {
			return nil
		}
		if err := r.Deposits().Update(ctx, *d); err != nil {
			return err
		}
		return r.Emit(ctx, &walletv1.DepositCredited{Deposit: ToProto(*d), JournalId: journalID}, d.UserID)
	})
}

func depositKind(k string) string {
	if k == "" {
		return domain.KindChain
	}
	return k
}

// ToProto renders a deposit for events.
func ToProto(d domain.Deposit) *walletv1.Deposit {
	return &walletv1.Deposit{
		DepositId: d.ID, UserId: d.UserID, Asset: d.Asset, Network: d.Network, Address: d.Address, TxHash: d.TxHash,
		LogIndex: d.LogIndex, BlockNumber: d.BlockNumber, Amount: d.Amount.String(), Confirmations: d.Confirmations,
		RequiredConfirmations: d.Required, Unclaimed: d.Unclaimed, Reason: d.Reason, Contract: d.Contract,
		RawAmount: d.RawAmount.String(), Status: d.Status, Kind: depositKind(d.Kind), ProviderTxId: d.ProviderTxID,
	}
}
