package application

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	walletv1 "github.com/skill/exchange/api/gen/go/exchange/wallet/v1"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/wallet/domain"
	"github.com/skill/exchange/internal/wallet/ports"
)

// The admin console's handling of withdrawals in review and of deposits
// that need a person (design 2026-10-02 §4.2, §4.3): the console checks
// the administrator's role; the wallet records the decision and audits it
// with the administrator as the actor.

// WithdrawalDetail is a withdrawal with what a reviewer weighs: its
// address in the user's address book (nil when gone) and the user's
// withdrawals' worth today and this month (UTC), refused ones left out.
type WithdrawalDetail struct {
	Withdrawal domain.Withdrawal
	Address    *domain.WithdrawAddress
	UsedToday  decimal.Decimal
	UsedMonth  decimal.Decimal
}

// Detail returns a withdrawal for a reviewer.
func (s *Service) Detail(ctx context.Context, id string) (WithdrawalDetail, error) {
	if _, err := uuid.Parse(id); err != nil {
		return WithdrawalDetail{}, apperr.NotFound("no such withdrawal")
	}
	r := s.Store.Read()
	w, err := r.Withdrawals().Get(ctx, id)
	if err != nil {
		return WithdrawalDetail{}, err
	}
	if w == nil {
		return WithdrawalDetail{}, apperr.NotFound("no such withdrawal")
	}
	out := WithdrawalDetail{Withdrawal: *w}
	if out.Address, err = r.WithdrawAddresses().Find(ctx, w.UserID, w.Network, w.Address); err != nil {
		return WithdrawalDetail{}, err
	}
	now := s.Now().UTC()
	if out.UsedToday, err = r.Withdrawals().ValueSince(ctx, w.UserID, now.Truncate(24*time.Hour)); err != nil {
		return WithdrawalDetail{}, err
	}
	y, m, _ := now.Date()
	if out.UsedMonth, err = r.Withdrawals().ValueSince(ctx, w.UserID, time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		return WithdrawalDetail{}, err
	}
	return out, nil
}

// HoldWithdrawal puts a withdrawal in review on hold with a note (hold),
// or takes it off hold, audited as wallet.withdrawal.hold or
// wallet.withdrawal.unhold.
func (s *Service) HoldWithdrawal(ctx context.Context, id string, hold bool, reviewer, note string) (domain.Withdrawal, error) {
	if _, err := uuid.Parse(id); err != nil {
		return domain.Withdrawal{}, apperr.NotFound("no such withdrawal")
	}
	if strings.TrimSpace(reviewer) == "" {
		return domain.Withdrawal{}, apperr.Invalid("the reviewer is required")
	}
	var w domain.Withdrawal
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Withdrawals().GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if cur == nil {
			return apperr.NotFound("no such withdrawal")
		}
		action := "wallet.withdrawal.unhold"
		if hold {
			action = "wallet.withdrawal.hold"
			if err := cur.Hold(reviewer, note, s.Now()); err != nil {
				return err
			}
		} else if !cur.Unhold(s.Now()) {
			w = *cur
			return nil
		}
		w = *cur
		if err := r.Withdrawals().Update(ctx, w); err != nil {
			return err
		}
		details, _ := json.Marshal(map[string]string{"user_id": w.UserID, "asset": w.Asset, "amount": w.Amount.String()})
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "withdrawal:" + w.ID, Action: action, Actor: reviewer, Reason: strings.TrimSpace(note), Details: string(details),
		}, reviewer)
	})
	return w, err
}

// AdminDeposits returns a page of deposits for the admin console, newest
// first, and the cursor of the next ("" on the last).
func (s *Service) AdminDeposits(ctx context.Context, f ports.DepositFilter) ([]domain.Deposit, string, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	want := f.Limit
	f.Limit++
	list, err := s.Store.Read().Deposits().Page(ctx, f)
	if err != nil || len(list) <= want {
		return list, "", err
	}
	list = list[:want]
	return list, list[want-1].ID, nil
}

// AdminDeposit returns one deposit.
func (s *Service) AdminDeposit(ctx context.Context, id string) (domain.Deposit, error) {
	d, err := s.Store.Read().Deposits().Get(ctx, id)
	if err != nil {
		return domain.Deposit{}, err
	}
	if d == nil {
		return domain.Deposit{}, apperr.NotFound("no such deposit")
	}
	return *d, nil
}

func needDecider(actor, reason string) error {
	if strings.TrimSpace(actor) == "" {
		return apperr.Invalid("the actor is required")
	}
	if len(strings.TrimSpace(reason)) < 3 {
		return apperr.Invalid("a reason of at least 3 characters is required")
	}
	return nil
}

// CreditDeposit gives an unclaimed deposit, booked to UNCLAIMED_DEPOSIT,
// to its user once an administrator decided it is theirs: the ledger
// moves its asset and amount to the user (audited there as
// ledger.unclaimed_released) and the deposit becomes CREDITED. A repeat
// after a failure in between books nothing twice (the deposit ID keys
// the release).
func (s *Service) CreditDeposit(ctx context.Context, id, actor, reason string) (domain.Deposit, error) {
	if err := needDecider(actor, reason); err != nil {
		return domain.Deposit{}, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return domain.Deposit{}, apperr.NotFound("no such deposit")
	}
	// The deposit stays locked from its check to its record, the ledger's
	// release in between: no dismissal or callback's discrepancy slips in
	// after the check, so the funds never move for a deposit that is not
	// released. The release is idempotent (deposit-release:<id>): when the
	// record fails after it, the next attempt finds the same journal.
	var out domain.Deposit
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Deposits().GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if cur == nil {
			return apperr.NotFound("no such deposit")
		}
		if cur.UserID == domain.NoOwner {
			return domain.ErrNoOwner // AssignDeposit names its user first (B7a)
		}
		var journal string
		recorded := false
		if err := cur.Releasable(); err != nil {
			// Released by the ledger before, unrecorded, and in doubt since
			// (a discrepancy marked after it): the funds moved, so the
			// release is recorded; no new one is asked for (C5.5 ⑮).
			if cur.Status != domain.StatusRejected || !cur.Unclaimed || cur.JournalID == "" || cur.Resolution != "" {
				return err
			}
			released, _, lerr := s.W.Ledger.UnclaimedRelease(ctx, cur.ID)
			if lerr != nil {
				return lerr
			}
			if released == "" {
				return err
			}
			journal, recorded = released, true
			if err := cur.RecordRelease(journal, actor, strings.TrimSpace(reason), s.Now()); err != nil {
				return err
			}
		} else {
			if journal, err = s.W.Ledger.ReleaseUnclaimed(ctx, cur.ID, cur.UserID, cur.Asset, cur.Amount, actor, strings.TrimSpace(reason)); err != nil {
				return err
			}
			if err := cur.Release(journal, actor, strings.TrimSpace(reason), s.Now()); err != nil {
				return err
			}
		}
		out = *cur
		if err := r.Deposits().Update(ctx, out); err != nil {
			return err
		}
		if recorded {
			// The ledger audited its release when it made it; this records
			// who found it unrecorded and closed the deposit (C5.5 ⑰).
			details, _ := json.Marshal(map[string]string{
				"deposit_id": out.ID, "user_id": out.UserID, "asset": out.Asset, "amount": out.Amount.String(), "journal_id": journal,
				"discrepancy": out.Discrepancy,
			})
			if err := r.Audit(ctx, &auditv1.AdminActionPerformed{
				Target: "user:" + out.UserID, Action: "wallet.deposit.release_recorded", Actor: actor, Reason: strings.TrimSpace(reason),
				Details: string(details),
			}, actor); err != nil {
				return err
			}
		}
		return r.Emit(ctx, &walletv1.DepositCredited{Deposit: ToProto(out), JournalId: journal}, out.UserID)
	})
	return out, err
}

// ErrReleasedUnrecorded refuses closing an unclaimed deposit the ledger
// released already: crediting it again records the release.
var ErrReleasedUnrecorded = apperr.New(apperr.KindConflict, "WALLET_DEPOSIT_RELEASED",
	"the ledger released this deposit to its user already: credit it again to record the release")

// ErrReleasedToAnother refuses a deposit of nobody (B7a) the ledger
// released already, in an assignment whose record failed after it, to
// any user but the one it paid (details user_id and journal_id), and
// refuses closing it: assigning it to that user records the release
// (review AJ).
var ErrReleasedToAnother = apperr.New(apperr.KindConflict, "WALLET_DEPOSIT_RELEASED_TO_USER",
	"the ledger released this deposit to a user already: assign it to that user (user_id) to record the release")

// DismissDeposit closes a deposit that waited for a decision without
// moving funds (unclaimed funds stay in UNCLAIMED_DEPOSIT; a callback's
// discrepancy is taken note of), audited as wallet.deposit.dismissed.
func (s *Service) DismissDeposit(ctx context.Context, id, actor, reason string) (domain.Deposit, error) {
	if err := needDecider(actor, reason); err != nil {
		return domain.Deposit{}, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return domain.Deposit{}, apperr.NotFound("no such deposit")
	}
	var out domain.Deposit
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		d, err := r.Deposits().GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if d == nil {
			return apperr.NotFound("no such deposit")
		}
		// The ledger may have released an unclaimed one whose release was
		// not recorded here (a discrepancy marked since too): it is
		// credited again to record it, never closed (C5.5 ⑦, ⑮). Its row
		// stays locked, so no release slips in.
		if d.Unclaimed && d.JournalID != "" && d.Status != domain.StatusCredited {
			journal, paid, err := s.W.Ledger.UnclaimedRelease(ctx, d.ID)
			if err != nil {
				return err
			}
			if journal != "" && d.UserID == domain.NoOwner {
				return ErrReleasedToAnother.WithDetail("journal_id", journal).WithDetail("user_id", paid)
			}
			if journal != "" {
				return ErrReleasedUnrecorded.WithDetail("journal_id", journal)
			}
		}
		if err := d.Dismiss(actor, strings.TrimSpace(reason), s.Now()); err != nil {
			return err
		}
		out = *d
		if err := r.Deposits().Update(ctx, out); err != nil {
			return err
		}
		details, _ := json.Marshal(map[string]string{
			"deposit_id": d.ID, "user_id": d.UserID, "asset": d.Asset, "amount": d.Amount.String(), "status": d.Status, "reason": d.Reason,
			"discrepancy": d.Discrepancy,
		})
		// On the user, like the backfill and the ledger's release: the user's audit trail shows them all;
		// a deposit of nobody (B7a) on itself.
		target := "user:" + d.UserID
		if d.UserID == domain.NoOwner {
			target = "deposit:" + d.ID
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: target, Action: "wallet.deposit.dismissed", Actor: actor, Reason: strings.TrimSpace(reason),
			Details: string(details),
		}, actor)
	})
	return out, err
}

// ManualDeposit is an administrator's backfill of a deposit a custodian
// received whose callback was lost (design 2026-10-02 §4.3). The
// custodian's gateway cannot be asked about a trade, so the administrator
// checks it in the custodian's console or a block explorer first.
type ManualDeposit struct {
	Network string
	TradeID string
	Address string
	TxHash  string
	Amount  decimal.Decimal
	Actor   string
}

// CheckManualDeposit checks a backfill without booking it and returns the
// deposit it would book: the network is a custodian's, the address a
// user's deposit address on it, the trade (provider:tradeId) and the
// transfer (network, txHash, address) unknown, the amount positive within
// the asset's decimals. A known trade fails with WALLET_DEPOSIT_KNOWN
// naming the deposit.
func (s *Service) CheckManualDeposit(ctx context.Context, in ManualDeposit) (domain.Deposit, error) {
	return s.manualDeposit(ctx, s.Store.Read(), in, s.Now())
}

func (s *Service) manualDeposit(ctx context.Context, r ports.Repos, in ManualDeposit, now time.Time) (domain.Deposit, error) {
	in.TradeID, in.Address, in.TxHash = strings.TrimSpace(in.TradeID), strings.TrimSpace(in.Address), strings.TrimSpace(in.TxHash)
	switch {
	case in.TradeID == "" || in.Address == "" || in.TxHash == "":
		return domain.Deposit{}, apperr.Invalid("the trade ID, the address and the transaction hash are required")
	case strings.TrimSpace(in.Actor) == "":
		return domain.Deposit{}, apperr.Invalid("the actor is required")
	case !in.Amount.IsPositive():
		return domain.Deposit{}, apperr.Invalid("the amount must be positive")
	}
	nets, err := s.Networks.ForAsset(ctx, "")
	if err != nil {
		return domain.Deposit{}, err
	}
	var net domain.Network
	for _, n := range nets {
		if n.Network == strings.ToUpper(strings.TrimSpace(in.Network)) && n.Custody() {
			net = n
			break
		}
	}
	if net.Network == "" {
		return domain.Deposit{}, apperr.Invalid("no custodian network " + in.Network)
	}
	if !in.Amount.Equal(in.Amount.Truncate(net.Decimals)) {
		return domain.Deposit{}, apperr.New(apperr.KindInvalid, "WALLET_AMOUNT_PRECISION", "the amount has more decimals than the asset").
			WithDetail("decimals", net.Decimals)
	}
	owner, err := r.Addresses().Owner(ctx, net.Network, in.Address)
	if err != nil {
		return domain.Deposit{}, err
	}
	if owner == "" {
		return domain.Deposit{}, apperr.Invalid("no user's deposit address " + in.Address + " on " + net.Network)
	}
	key := net.Provider + ":" + in.TradeID
	if known, err := r.Deposits().ByProviderTx(ctx, key); err != nil {
		return domain.Deposit{}, err
	} else if known != nil {
		return *known, domain.ErrKnown.WithDetail("deposit_id", known.ID)
	}
	if known, err := r.Deposits().ByTransfer(ctx, net.Network, in.TxHash, in.Address); err != nil {
		return domain.Deposit{}, err
	} else if known != nil {
		return *known, domain.ErrKnown.WithDetail("deposit_id", known.ID)
	}
	required := max(net.Confirmations, 1)
	d := domain.Deposit{
		ID: uuid.Must(uuid.NewV7()).String(), Kind: domain.KindChain, UserID: owner, Asset: net.Asset, Network: net.Network,
		Address: in.Address, Contract: net.Contract, TxHash: in.TxHash, LogIndex: domain.NativeLog, Amount: in.Amount,
		// The chain's smallest unit is not known here: the amount in the asset's decimals stands for it.
		RawAmount: in.Amount.Shift(net.Decimals), Confirmations: required, Required: required, Status: domain.StatusConfirmed,
		ProviderTxID: key, DetectedAt: now, ConfirmedAt: now, Source: domain.SourceManual, EnteredBy: strings.TrimSpace(in.Actor),
	}
	if own, err := r.Addresses().Get(ctx, owner, net.Network); err == nil && own != nil {
		d.Address = own.Address
	}
	if d.Amount.LessThan(net.MinDeposit) {
		d.Unclaimed, d.Reason = true, domain.ReasonBelowMinimum
	}
	return d, nil
}

// BookManualDeposit books a backfill checked as CheckManualDeposit does:
// CONFIRMED like a callback's deposit, sent to the ledger by the
// processor (to UNCLAIMED_DEPOSIT below the minimum), source MANUAL with
// the administrator; audited as wallet.deposit.backfilled. When the
// custodian's callback comes later it only confirms it (or records a
// discrepancy). Repeating the same backfill returns its deposit, also when
// the repeat ran at the same time and lost the race to the unique index
// (C5.5 ⑦: the console's second operation is then booked, not stuck).
func (s *Service) BookManualDeposit(ctx context.Context, in ManualDeposit, reason string) (domain.Deposit, error) {
	if err := needDecider(in.Actor, reason); err != nil {
		return domain.Deposit{}, err
	}
	out, err := s.bookManual(ctx, in, reason)
	if _, dup := pg.UniqueViolation(err); dup {
		return s.bookManual(ctx, in, reason)
	}
	return out, err
}

func (s *Service) bookManual(ctx context.Context, in ManualDeposit, reason string) (domain.Deposit, error) {
	var out domain.Deposit
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		d, err := s.manualDeposit(ctx, r, in, s.Now())
		if apperr.Is(err, domain.ErrKnown.Code) && d.Source == domain.SourceManual && d.Amount.Equal(in.Amount) &&
			strings.HasSuffix(d.ProviderTxID, ":"+strings.TrimSpace(in.TradeID)) && strings.EqualFold(d.TxHash, strings.TrimSpace(in.TxHash)) &&
			strings.EqualFold(d.Address, strings.TrimSpace(in.Address)) {
			out = d // the same backfill again
			return nil
		}
		if err != nil {
			return err
		}
		out = d
		if err := r.Deposits().Insert(ctx, d); err != nil {
			return err
		}
		if err := r.Emit(ctx, &walletv1.DepositDetected{Deposit: ToProto(d)}, d.UserID); err != nil {
			return err
		}
		details, _ := json.Marshal(map[string]string{
			"deposit_id": d.ID, "user_id": d.UserID, "network": d.Network, "trade_id": d.ProviderTxID, "tx_hash": d.TxHash,
			"address": d.Address, "asset": d.Asset, "amount": d.Amount.String(),
		})
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "user:" + d.UserID, Action: "wallet.deposit.backfilled", Actor: d.EnteredBy, Reason: strings.TrimSpace(reason),
			Details: string(details),
		}, d.EnteredBy)
	})
	return out, err
}
