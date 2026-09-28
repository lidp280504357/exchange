package application

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	walletv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/wallet/v1"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/evm"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
)

// withdrawals runs the withdrawal steps of a round: requests left
// REQUESTED by an outage, releases of refused funds, approved withdrawals
// (internal transfers in the ledger, the others signed and broadcast),
// settlements, and the receipts, confirmations and replacements of
// broadcast ones.
func (p *Processor) withdrawals(ctx context.Context, native domain.Network) error {
	return errors.Join(p.recoverRequested(ctx), p.release(ctx), p.dispatch(ctx, native), p.settle(ctx), p.track(ctx, native))
}

func (p *Processor) recoverRequested(ctx context.Context) error {
	list, err := p.Store.Read().Withdrawals().ByStatus(ctx, p.Network, domain.WithdrawalRequested)
	if err != nil {
		return err
	}
	var errs []error
	for _, w := range list {
		if p.Now().Sub(w.UpdatedAt) < time.Minute {
			continue
		}
		if _, err := FreezeWithdrawal(ctx, p.Store, p.Ledger, w.ID, p.Now); err != nil && !apperr.Is(err, apperr.CodeUnavailable) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (p *Processor) release(ctx context.Context) error {
	list, err := p.Store.Read().Withdrawals().Unreleased(ctx, p.Network)
	if err != nil {
		return err
	}
	var errs []error
	for _, w := range list {
		if _, err := ReleaseWithdrawal(ctx, p.Store, p.Ledger, w); err != nil {
			errs = append(errs, fmt.Errorf("release withdrawal %s: %w", w.ID, err))
		}
	}
	return errors.Join(errs...)
}

// dispatch sends the approved withdrawals, oldest first. One that waits
// (fee above the cap, hot wallet short) holds up the ones behind it, which
// keeps the hot wallet's nonces in order.
func (p *Processor) dispatch(ctx context.Context, native domain.Network) error {
	list, err := p.Store.Read().Withdrawals().ByStatus(ctx, p.Network, domain.WithdrawalApproved)
	if err != nil {
		return err
	}
	waiting := 0
	defer func() { p.waiting.Set(float64(waiting)) }()
	for i, w := range list {
		if w.InternalUserID != "" {
			if err := p.transferInternal(ctx, w); err != nil {
				return err
			}
			continue
		}
		sent, err := p.send(ctx, w, native)
		if err != nil {
			return err
		}
		if !sent {
			waiting = len(list) - i
			return nil
		}
	}
	return nil
}

// transferInternal completes a withdrawal to another user's deposit
// address in the ledger and records the payee's internal deposit.
func (p *Processor) transferInternal(ctx context.Context, w domain.Withdrawal) error {
	journal, err := p.Ledger.TransferInternal(ctx, w.ID, w.UserID, w.InternalUserID, w.Asset, w.Amount, w.ID)
	if err != nil {
		return fmt.Errorf("internal transfer %s: %w", w.ID, err)
	}
	return p.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Withdrawals().GetForUpdate(ctx, w.ID)
		if err != nil || cur == nil || cur.Status != domain.WithdrawalApproved {
			return err
		}
		now := p.Now()
		cur.SettleJournal, cur.Status, cur.ConfirmedAt, cur.UpdatedAt = journal, domain.WithdrawalConfirmed, now, now
		if err := r.Withdrawals().Update(ctx, *cur); err != nil {
			return err
		}
		if err := r.EmitWithdrawal(ctx, &walletv1.WithdrawalConfirmed{Withdrawal: WithdrawalProto(*cur)}, cur.UserID); err != nil {
			return err
		}
		own, err := r.Addresses().Get(ctx, cur.InternalUserID, cur.Network)
		if err != nil {
			return err
		}
		address := cur.Address
		if own != nil {
			address = own.Address
		}
		raw, err := evm.ToWei(cur.Amount, evm.NativeDecimals)
		if err != nil {
			return err
		}
		d := domain.Deposit{
			ID: uuid.Must(uuid.NewV7()).String(), Kind: domain.KindInternal, UserID: cur.InternalUserID, Asset: cur.Asset,
			Network: cur.Network, Address: address, TxHash: "internal:" + cur.ID, LogIndex: domain.NativeLog, Amount: cur.Amount,
			RawAmount: decimal.NewFromBigInt(raw, 0), Status: domain.StatusCredited, JournalID: journal, DetectedAt: now,
			ConfirmedAt: now, CreditedAt: now, CreditRequested: now,
		}
		if err := r.Deposits().Insert(ctx, d); err != nil {
			return err
		}
		p.Log.InfoContext(ctx, "internal withdrawal completed", "withdrawal_id", cur.ID, "deposit_id", d.ID, "journal_id", journal)
		return r.Emit(ctx, &walletv1.DepositCredited{Deposit: ToProto(d), JournalId: journal}, d.UserID)
	})
}

// send signs and broadcasts an approved withdrawal from the hot wallet,
// or reports false to wait: a fee above the cap (§5.10) or a hot wallet
// that cannot pay (§11.6).
func (p *Processor) send(ctx context.Context, w domain.Withdrawal, native domain.Network) (bool, error) {
	if w.Asset != native.Asset {
		return false, fmt.Errorf("withdrawal %s: only %s is sent from the hot wallet", w.ID, native.Asset)
	}
	maxFee, tip, err := p.fees(ctx)
	if err != nil {
		return false, err
	}
	if maxFee.Cmp(p.MaxFee) > 0 {
		p.Log.WarnContext(ctx, "chain fees above the cap: withdrawals wait", "max_fee_wei", maxFee.String(), "cap_wei", p.MaxFee.String())
		return false, nil
	}
	value, err := evm.ToWei(w.Amount, evm.NativeDecimals)
	if err != nil {
		return false, err
	}
	balance, err := p.Chain.Balance(ctx, p.hot)
	if err != nil {
		return false, err
	}
	if need := new(big.Int).Add(value, new(big.Int).Mul(maxFee, big.NewInt(transferGas))); balance.Cmp(need) < 0 {
		p.Log.WarnContext(ctx, "the hot wallet cannot pay the next withdrawal: it waits", "withdrawal_id", w.ID,
			"balance", evm.FromWei(balance, evm.NativeDecimals).String(), "needed", evm.FromWei(need, evm.NativeDecimals).String())
		return false, nil
	}
	recorded, err := p.Store.Read().Nonces().Peek(ctx, p.hot)
	if err != nil {
		return false, err
	}
	pending, err := p.Chain.PendingNonce(ctx, p.hot)
	if err != nil {
		return false, err
	}
	nonce := max(recorded, pending)
	// SIGNING closes the door to a cancellation from now on.
	moved, err := p.transition(ctx, w.ID, domain.WithdrawalApproved, domain.WithdrawalSigning)
	if err != nil || !moved {
		return moved, err
	}
	approvedBy := strings.Join(w.Approvals, ",")
	if approvedBy == "" {
		approvedBy = riskEngine
	}
	signed, err := p.Signer.Sign(ctx, ports.SignRequest{
		ID: w.ID + "-0", Purpose: domain.FeeWithdrawal, Reference: w.ID, ApprovedBy: approvedBy, ChainID: p.ChainID, Nonce: nonce,
		To: w.Address, Value: value, GasLimit: transferGas, MaxFee: maxFee, MaxTip: tip,
	})
	if err != nil {
		if apperr.Is(err, "SIGNER_REFUSED") {
			// The signer's own limits say no: a person decides (§11.6).
			return true, p.fail(ctx, w.ID, "SIGNER_REFUSED: "+detail(err))
		}
		_, terr := p.transition(ctx, w.ID, domain.WithdrawalSigning, domain.WithdrawalApproved)
		return false, errors.Join(fmt.Errorf("sign withdrawal %s: %w", w.ID, err), terr)
	}
	hash := strings.ToLower(signed.TxHash)
	err = p.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Withdrawals().GetForUpdate(ctx, w.ID)
		if err != nil || cur == nil {
			return err
		}
		if err := r.Attempts().Insert(ctx, domain.Attempt{
			TxHash: hash, WithdrawalID: w.ID, Nonce: nonce, MaxFee: maxFee, MaxTip: tip, Raw: signed.Raw, CreatedAt: p.Now(),
		}); err != nil {
			return err
		}
		if err := r.Nonces().Advance(ctx, p.hot, nonce+1); err != nil {
			return err
		}
		cur.Broadcasted(nonce, hash, p.Now())
		if err := r.Withdrawals().Update(ctx, *cur); err != nil {
			return err
		}
		return r.EmitWithdrawal(ctx, &walletv1.WithdrawalBroadcast{Withdrawal: WithdrawalProto(*cur), Nonce: nonce}, cur.UserID)
	})
	if err != nil {
		return false, err
	}
	if err := p.Chain.SendRaw(ctx, signed.Raw); err != nil {
		p.Log.WarnContext(ctx, "withdrawal broadcast failed, will retry", "withdrawal_id", w.ID, "error", err)
	}
	p.Log.InfoContext(ctx, "withdrawal broadcast", "withdrawal_id", w.ID, "nonce", nonce, "tx_hash", hash)
	return true, nil
}

func detail(err error) string {
	if e := apperr.From(err); e != nil {
		if why, ok := e.Details["reason"].(string); ok {
			return why
		}
		return e.Message
	}
	return err.Error()
}

// transition moves a withdrawal from one status to another, reporting
// false when it is no longer in the first (a cancellation won).
func (p *Processor) transition(ctx context.Context, id, from, to string) (bool, error) {
	moved := false
	err := p.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Withdrawals().GetForUpdate(ctx, id)
		if err != nil || cur == nil || cur.Status != from {
			return err
		}
		cur.Status, cur.UpdatedAt = to, p.Now()
		moved = true
		return r.Withdrawals().Update(ctx, *cur)
	})
	return moved, err
}

// fail marks a withdrawal FAILED for manual handling; one that never
// reached the chain gets its funds released.
func (p *Processor) fail(ctx context.Context, id, reason string) error {
	return p.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Withdrawals().GetForUpdate(ctx, id)
		if err != nil || cur == nil {
			return err
		}
		cur.Status, cur.RejectReason, cur.UpdatedAt = domain.WithdrawalFailed, reason, p.Now()
		if err := r.Withdrawals().Update(ctx, *cur); err != nil {
			return err
		}
		p.Log.ErrorContext(ctx, "withdrawal failed", "withdrawal_id", id, "reason", reason)
		return r.EmitWithdrawal(ctx, &walletv1.WithdrawalFailed{Withdrawal: WithdrawalProto(*cur)}, cur.UserID)
	})
}

// settle books broadcast withdrawals in the ledger (WITHDRAW_SETTLE: §11.6
// settles on broadcast).
func (p *Processor) settle(ctx context.Context) error {
	list, err := p.Store.Read().Withdrawals().Unsettled(ctx, p.Network)
	if err != nil {
		return err
	}
	var errs []error
	for _, w := range list {
		journal, err := p.Ledger.Settle(ctx, w.ID, w.UserID, w.Asset, w.Amount, w.Fee, w.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("settle withdrawal %s: %w", w.ID, err))
			continue
		}
		err = p.Store.Tx(ctx, func(r ports.Repos) error {
			cur, err := r.Withdrawals().GetForUpdate(ctx, w.ID)
			if err != nil || cur == nil {
				return err
			}
			cur.SettleJournal = journal
			return r.Withdrawals().Update(ctx, *cur)
		})
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// track follows broadcast withdrawals: a receipt of any attempt counts
// the confirmations (and records the gas); none after ReplaceAfter sends
// a replacement with a higher fee on the same nonce; a transaction the
// node lost is sent again.
func (p *Processor) track(ctx context.Context, native domain.Network) error {
	list, err := p.Store.Read().Withdrawals().ByStatus(ctx, p.Network, domain.WithdrawalBroadcast, domain.WithdrawalConfirming)
	if err != nil {
		return err
	}
	var errs []error
	for _, w := range list {
		errs = append(errs, p.trackOne(ctx, w, native))
	}
	return errors.Join(errs...)
}

func (p *Processor) trackOne(ctx context.Context, w domain.Withdrawal, native domain.Network) error {
	atts, err := p.Store.Read().Attempts().Of(ctx, w.ID)
	if err != nil || len(atts) == 0 {
		return err
	}
	var mined *ports.Receipt
	var minedHash string
	for _, a := range atts {
		rc, err := p.Chain.Receipt(ctx, a.TxHash)
		if err != nil {
			return err
		}
		if rc != nil {
			mined, minedHash = rc, a.TxHash
			break
		}
	}
	if mined == nil {
		newest := atts[0]
		if p.Now().Sub(newest.CreatedAt) >= p.ReplaceAfter {
			return p.replace(ctx, w, newest, len(atts))
		}
		return p.rebroadcast(ctx, newest.TxHash, newest.Raw, newest.CreatedAt)
	}
	fee := evm.FromWei(new(big.Int).Mul(new(big.Int).SetUint64(mined.GasUsed), mined.EffectiveGasPrice), evm.NativeDecimals)
	if !mined.Succeeded {
		if err := p.Store.Tx(ctx, func(r ports.Repos) error { return p.recordFee(ctx, r, minedHash, fee, native, w.ID) }); err != nil {
			return err
		}
		return p.fail(ctx, w.ID, "REVERTED: "+minedHash)
	}
	head, err := p.Chain.Head(ctx)
	if err != nil {
		return err
	}
	return p.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Withdrawals().GetForUpdate(ctx, w.ID)
		if err != nil || cur == nil {
			return err
		}
		confirmed := cur.Mined(minedHash, mined.BlockNumber, head, p.Now())
		if err := r.Withdrawals().Update(ctx, *cur); err != nil {
			return err
		}
		if err := p.recordFee(ctx, r, minedHash, fee, native, w.ID); err != nil {
			return err
		}
		if !confirmed {
			return nil
		}
		p.Log.InfoContext(ctx, "withdrawal confirmed", "withdrawal_id", cur.ID, "tx_hash", minedHash)
		return r.EmitWithdrawal(ctx, &walletv1.WithdrawalConfirmed{Withdrawal: WithdrawalProto(*cur)}, cur.UserID)
	})
}

func (p *Processor) recordFee(ctx context.Context, r ports.Repos, hash string, fee decimal.Decimal, native domain.Network, id string) error {
	if !fee.IsPositive() {
		return nil
	}
	return r.ChainFees().Insert(ctx, domain.ChainFee{
		TxHash: hash, Network: p.Network, Asset: native.Asset, Amount: fee, Purpose: domain.FeeWithdrawal, Reference: id,
	})
}

// replace re-signs a stuck withdrawal on its nonce with fees raised by a
// quarter (nodes want at least a tenth) or to the market's, whichever is
// higher, within the cap.
func (p *Processor) replace(ctx context.Context, w domain.Withdrawal, prev domain.Attempt, n int) error {
	maxFee, tip, err := p.fees(ctx)
	if err != nil {
		return err
	}
	bump := func(old, market *big.Int) *big.Int {
		raised := new(big.Int).Div(new(big.Int).Mul(old, big.NewInt(125)), big.NewInt(100))
		if market.Cmp(raised) > 0 {
			return market
		}
		return raised
	}
	newFee, newTip := bump(prev.MaxFee, maxFee), bump(prev.MaxTip, tip)
	if newFee.Cmp(p.MaxFee) > 0 {
		p.Log.WarnContext(ctx, "a stuck withdrawal cannot be replaced within the fee cap", "withdrawal_id", w.ID)
		return p.rebroadcast(ctx, prev.TxHash, prev.Raw, prev.CreatedAt)
	}
	value, err := evm.ToWei(w.Amount, evm.NativeDecimals)
	if err != nil {
		return err
	}
	approvedBy := strings.Join(w.Approvals, ",")
	if approvedBy == "" {
		approvedBy = riskEngine
	}
	signed, err := p.Signer.Sign(ctx, ports.SignRequest{
		ID: w.ID + "-" + strconv.Itoa(n), Purpose: domain.FeeWithdrawal, Reference: w.ID, ApprovedBy: approvedBy, ChainID: p.ChainID,
		Nonce: prev.Nonce, To: w.Address, Value: value, GasLimit: transferGas, MaxFee: newFee, MaxTip: newTip,
	})
	if err != nil {
		return fmt.Errorf("sign the replacement of %s: %w", w.ID, err)
	}
	hash := strings.ToLower(signed.TxHash)
	err = p.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Withdrawals().GetForUpdate(ctx, w.ID)
		if err != nil || cur == nil {
			return err
		}
		if err := r.Attempts().Insert(ctx, domain.Attempt{
			TxHash: hash, WithdrawalID: w.ID, Nonce: prev.Nonce, MaxFee: newFee, MaxTip: newTip, Raw: signed.Raw, CreatedAt: p.Now(),
		}); err != nil {
			return err
		}
		cur.TxHash, cur.UpdatedAt = hash, p.Now()
		if err := r.Withdrawals().Update(ctx, *cur); err != nil {
			return err
		}
		return r.EmitWithdrawal(ctx, &walletv1.WithdrawalBroadcast{Withdrawal: WithdrawalProto(*cur), Nonce: prev.Nonce}, cur.UserID)
	})
	if err != nil {
		return err
	}
	p.Log.InfoContext(ctx, "withdrawal replaced with a higher fee", "withdrawal_id", w.ID, "nonce", prev.Nonce, "tx_hash", hash)
	return p.Chain.SendRaw(ctx, signed.Raw)
}
