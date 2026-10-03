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

// DefaultCallbackWindow is how far a callback's timestamp may be from now.
const DefaultCallbackWindow = 5 * time.Minute

// custodyAddress returns the user's address on a custodian's network,
// asking the custodian for one on first use. The custodian's call (up to
// its timeout) is made outside any transaction; the address is recorded
// under the lock, so of two first requests at once the second takes the
// first's address, and the custodian keeps one that nobody is shown.
func (s *Service) custodyAddress(ctx context.Context, userID string, net domain.Network) (domain.Address, error) {
	c := s.Custodians[net.Provider]
	if c == nil {
		return domain.Address{}, domain.ErrNotConfigured
	}
	if a, err := s.Store.Read().Addresses().Get(ctx, userID, net.Network); err != nil || a != nil {
		if a != nil {
			return *a, nil
		}
		return domain.Address{}, err
	}
	addr, err := c.CreateAddress(ctx, net, userID)
	if err != nil {
		s.Log.WarnContext(ctx, "the custodian did not create a deposit address", "network", net.Network, "error", err)
		return domain.Address{}, apperr.Wrap(err, apperr.KindUnavailable, "WALLET_UNAVAILABLE", "the deposit address cannot be created now, try again shortly")
	}
	var out domain.Address
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.Addresses().Lock(ctx, userID, net.Network); err != nil {
			return err
		}
		if a, err := r.Addresses().Get(ctx, userID, net.Network); err != nil || a != nil {
			if a != nil {
				out = *a
			}
			return err
		}
		out = domain.Address{UserID: userID, Network: net.Network, Provider: net.Provider, Address: addr, CreatedAt: s.Now()}
		if err := r.Addresses().Insert(ctx, out); err != nil {
			return err
		}
		return r.Emit(ctx, &walletv1.DepositAddressAssigned{
			UserId: userID, Network: net.Network, Address: addr, Provider: net.Provider,
		}, userID)
	})
	return out, err
}

// HandleCallback records a custodian's callback as received and applies
// it: a verified one once per trade and status (the custodian's retries
// count as attempts and change nothing more), one that fails its
// signature or age check is logged and refused. A callback that cannot
// be applied now is recorded FAILED and its error returned, for the
// custodian to try again. remoteIP, where the delivery came from, is kept
// with it (the allow list is drawn from these).
func (s *Service) HandleCallback(ctx context.Context, provider, contentType, remoteIP string, raw []byte) (domain.Callback, error) {
	c := s.Custodians[provider]
	if c == nil {
		return domain.Callback{}, apperr.NotFound("no such custodian")
	}
	now := s.Now()
	window := s.CallbackWindow
	if window <= 0 {
		window = DefaultCallbackWindow
	}
	t, perr := c.Parse(contentType, raw, now, window)
	cb := callbackOf(provider, t, string(raw), now)
	if remoteIP != "" {
		cb.RemoteIPs = []string{remoteIP}
	}
	if perr != nil {
		if s.CallbacksRejected != nil {
			s.CallbacksRejected.Inc()
		}
		cb.Result, cb.Detail = domain.CallbackRejected, apperr.From(perr).Message
		if err := s.keepRejected(ctx, cb); err != nil {
			return domain.Callback{}, errors.Join(perr, err)
		}
		s.Log.WarnContext(ctx, "custodian callback refused", "provider", provider, "remote_ip", remoteIP, "error", perr)
		return cb, perr
	}
	cb.SignatureOK = true
	stored, fresh, err := s.receive(ctx, cb)
	if err != nil {
		return domain.Callback{}, err
	}
	if !fresh && stored.Settled() {
		return stored, nil
	}
	return s.apply(ctx, c, stored, t)
}

// A refused callback is kept for a person to look at, but a flood of them
// (forged, or signed with another key) must not fill the table: at most
// rejectedPerHour an hour, each cut to rejectedRaw bytes; the rest are only
// counted (CallbacksRejected).
const (
	rejectedPerHour = 100
	rejectedRaw     = 2048
)

func (s *Service) keepRejected(ctx context.Context, cb domain.Callback) error {
	n, err := s.Store.Read().Callbacks().RejectedSince(ctx, cb.ReceivedAt.Add(-time.Hour))
	if err != nil || n >= rejectedPerHour {
		return err
	}
	if len(cb.Raw) > rejectedRaw {
		cb.Raw = strings.ToValidUTF8(cb.Raw[:rejectedRaw], "")
	}
	_, _, err = s.receive(ctx, cb)
	return err
}

func (s *Service) receive(ctx context.Context, cb domain.Callback) (domain.Callback, bool, error) {
	var stored domain.Callback
	var fresh bool
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		var err error
		stored, fresh, err = r.Callbacks().Receive(ctx, cb)
		return err
	})
	return stored, fresh, err
}

func callbackOf(provider string, t ports.CustodyTrade, raw string, now time.Time) domain.Callback {
	cb := domain.Callback{
		ID: uuid.Must(uuid.NewV7()).String(), Provider: provider, TradeID: t.TradeID, Kind: t.Kind, Status: t.Status,
		BusinessID: t.BusinessID, Coin: t.Coin, Address: t.Address, TxHash: t.TxHash, Raw: raw, Result: domain.CallbackReceived,
		ReceivedAt: now,
	}
	if t.TradeID != "" {
		amount := t.Amount
		cb.Amount = &amount
	}
	return cb
}

// apply applies a verified callback and records the outcome with it; a
// failure is recorded on its own and returned.
func (s *Service) apply(ctx context.Context, c ports.Custody, cb domain.Callback, t ports.CustodyTrade) (domain.Callback, error) {
	now := s.Now()
	var settle bool
	var after []func() // what is counted and logged once the transaction committed
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		var result, detail string
		var err error
		after = after[:0]
		switch want, known := s.coinDecimals(ctx, c, t.Coin); {
		case known && want != t.Decimals && (t.Amount.IsPositive() || t.Fee.IsPositive()):
			// 25500000 at 0 decimals of a coin with 6 would book 25.5
			// million: a callback that counts in other decimals than the
			// custodian lists for the coin is left to a person.
			result, detail = domain.CallbackUnmatched, fmt.Sprintf("the callback counts %s in %d decimals, the custodian lists %d", t.Coin,
				t.Decimals, want)
		case t.Kind == domain.CallbackDeposit:
			result, detail, err = s.applyDeposit(ctx, r, c.Provider(), t, now, &after)
		case t.Kind == domain.CallbackWithdrawal:
			result, detail, settle, err = s.applyWithdrawal(ctx, r, c.Provider(), t, now, &after)
		default:
			result, detail = domain.CallbackIgnored, "neither a deposit nor a withdrawal"
		}
		if err != nil {
			return err
		}
		cb.Result, cb.Detail, cb.ProcessedAt = result, detail, now
		return r.Callbacks().Finish(ctx, cb.ID, result, detail, now)
	})
	if err != nil {
		s.Log.ErrorContext(ctx, "custodian callback not applied", "callback_id", cb.ID, "trade_id", cb.TradeID, "error", err)
		cb.Result, cb.Detail, cb.ProcessedAt = domain.CallbackFailed, err.Error(), now
		ferr := s.Store.Tx(ctx, func(r ports.Repos) error { return r.Callbacks().Finish(ctx, cb.ID, cb.Result, cb.Detail, now) })
		return cb, errors.Join(err, ferr)
	}
	for _, f := range after {
		f()
	}
	s.Log.InfoContext(ctx, "custodian callback", "callback_id", cb.ID, "trade_id", cb.TradeID, "kind", cb.Kind, "status", cb.Status,
		"result", cb.Result, "detail", cb.Detail)
	if settle {
		// The processor would do it within a round; done now, the user sees
		// the balance settle with the status.
		s.settleOne(ctx, t.BusinessID)
	}
	return cb, nil
}

// unmatched counts and logs a deposit no user's address takes (B7a): a
// person credits it to a user or dismisses it.
func (s *Service) unmatched(ctx context.Context, reason string, t ports.CustodyTrade) {
	if s.Unmatched != nil {
		s.Unmatched.WithLabelValues(reason).Inc()
	}
	s.Log.WarnContext(ctx, "a custodian's deposit no user's address takes: a person credits it to a user or dismisses it",
		"reason", reason, "trade_id", t.TradeID, "coin", t.Coin, "address", t.Address, "amount", t.Amount.String(), "tx_hash", t.TxHash)
}

// coinsEvery is how long the custodian's list of coins (their decimals)
// is kept.
const coinsEvery = 10 * time.Minute

// coinDecimals is how many decimals the custodian lists for coin, false
// when the list cannot be read (the callback's own decimals are then
// taken: the custodian's API being down must not stop the callbacks).
func (s *Service) coinDecimals(ctx context.Context, c ports.Custody, coin string) (int32, bool) {
	k, ok := s.coin(ctx, c, coin)
	return k.Decimals, ok
}

// coin is the custodian's listing of a coin (support-coins, read again
// every coinsEvery), false when it is not listed or the list cannot be
// read. Each custodian's list is its own: two serve different coins.
func (s *Service) coin(ctx context.Context, c ports.Custody, coin string) (ports.CustodyCoin, bool) {
	s.coinsMu.Lock()
	defer s.coinsMu.Unlock()
	cur := s.coinsOf[c.Provider()]
	if cur.At.IsZero() || s.Now().Sub(cur.At) >= coinsEvery {
		list, err := c.Coins(ctx)
		if err != nil {
			s.Log.WarnContext(ctx, "the custodian's coins not read: callbacks taken at their own decimals",
				"provider", c.Provider(), "error", err)
		} else {
			cur = coinList{At: s.Now(), Coins: make(map[string]ports.CustodyCoin, len(list))}
			for _, k := range list {
				cur.Coins[k.Code] = k
			}
			if s.coinsOf == nil {
				s.coinsOf = map[string]coinList{}
			}
			s.coinsOf[c.Provider()] = cur
		}
	}
	k, ok := cur.Coins[coin]
	return k, ok
}

// networkOfCoin finds the custodian's network of a coin.
func (s *Service) networkOfCoin(ctx context.Context, provider, coin string) (domain.Network, bool, error) {
	nets, err := s.Networks.ForAsset(ctx, "")
	if err != nil {
		return domain.Network{}, false, err
	}
	for _, n := range nets {
		if n.Provider == provider && n.ProviderCoin == coin {
			return n, true, nil
		}
	}
	return domain.Network{}, false, nil
}

// applyDeposit records a deposit the custodian confirmed: CONFIRMED with
// the network's confirmations, for the processor to send to the ledger;
// unclaimed below the minimum, rejected when nothing of it fits the
// asset's decimals.
func (s *Service) applyDeposit(ctx context.Context, r ports.Repos, provider string, t ports.CustodyTrade, now time.Time, after *[]func(),
) (string, string, error) {
	if t.Word != domain.CustodySuccess {
		return domain.CallbackIgnored, fmt.Sprintf("status %d: credited when the custodian reports success", t.Status), nil
	}
	if known, err := r.Deposits().ByProviderTx(ctx, provider+":"+t.TradeID); err != nil {
		return "", "", err
	} else if known != nil && known.Source == domain.SourceManual && known.CallbackAt.IsZero() {
		return s.matchManual(ctx, r, provider, *known, t, now)
	}
	net, ok, err := s.networkOfCoin(ctx, provider, t.Coin)
	if err != nil {
		return "", "", err
	}
	if !ok {
		*after = append(*after, func() { s.unmatched(ctx, "unknown_coin", t) })
		return domain.CallbackUnmatched, "no network uses the coin " + t.Coin, nil
	}
	owner, err := r.Addresses().Owner(ctx, net.Network, t.Address)
	if err != nil {
		return "", "", err
	}
	// Money for an address no user has (a probe's, a retired stand-in's):
	// booked to UNCLAIMED_DEPOSIT as a deposit of nobody, for an
	// administrator to credit to a user or dismiss; announced to no one
	// (B7a of the real gateway's integration).
	nobody := owner == ""
	if nobody {
		owner = domain.NoOwner
	}
	key := provider + ":" + t.TradeID
	if known, err := r.Deposits().ByProviderTx(ctx, key); err != nil || known != nil {
		if known != nil {
			return domain.CallbackIgnored, "already deposit " + known.ID, nil
		}
		return "", "", err
	}
	// The same transfer under another trade ID: a backfill entered with a
	// wrong one is matched by the transfer (and the trade differs, so a
	// person looks); any other deposit of it is never booked twice.
	if t.TxHash != "" {
		same, err := r.Deposits().ByTransfer(ctx, net.Network, t.TxHash, t.Address)
		if err != nil {
			return "", "", err
		}
		if same != nil && same.Source == domain.SourceManual && same.CallbackAt.IsZero() {
			return s.matchManual(ctx, r, provider, *same, t, now)
		}
		if same != nil {
			return domain.CallbackUnmatched, fmt.Sprintf("the transfer is deposit %s already (%s)", same.ID, same.ProviderTxID), nil
		}
	}
	if !t.RawAmount.IsPositive() {
		return domain.CallbackIgnored, "nothing transferred", nil
	}
	required := max(net.Confirmations, 1)
	d := domain.Deposit{
		ID: uuid.Must(uuid.NewV7()).String(), Kind: domain.KindChain, UserID: owner, Asset: net.Asset, Network: net.Network,
		Address: t.Address, Contract: net.Contract, TxHash: t.TxHash, LogIndex: domain.NativeLog, BlockNumber: t.Block,
		Amount: t.Amount.Truncate(net.Decimals), RawAmount: t.RawAmount, Confirmations: required, Required: required,
		Status: domain.StatusConfirmed, ProviderTxID: key, DetectedAt: now, ConfirmedAt: now,
	}
	if own, err := r.Addresses().Get(ctx, owner, net.Network); !nobody && err == nil && own != nil {
		d.Address = own.Address
	}
	switch {
	case d.Amount.IsZero() && nobody:
		d.Status, d.Reason = domain.StatusRejected, domain.ReasonUnknownAddress
	case d.Amount.IsZero():
		d.Status, d.Reason = domain.StatusRejected, domain.ReasonBelowMinimum
	case nobody:
		d.Unclaimed, d.Reason = true, domain.ReasonUnknownAddress
	case d.Amount.LessThan(net.MinDeposit):
		d.Unclaimed, d.Reason = true, domain.ReasonBelowMinimum
	}
	if err := r.Deposits().Insert(ctx, d); err != nil {
		return "", "", err
	}
	if nobody {
		*after = append(*after, func() { s.unmatched(ctx, "unknown_address", t) })
		return domain.CallbackApplied, fmt.Sprintf("deposit %s of nobody: %s %s to %s, an address no user has on %s, booked unclaimed (%s)",
			d.ID, d.Amount, d.Asset, t.Address, net.Network, d.Reason), nil
	}
	if d.Status == domain.StatusRejected {
		if err := r.Emit(ctx, &walletv1.DepositRejected{Deposit: ToProto(d)}, d.UserID); err != nil {
			return "", "", err
		}
	} else if err := r.Emit(ctx, &walletv1.DepositDetected{Deposit: ToProto(d)}, d.UserID); err != nil {
		return "", "", err
	}
	return domain.CallbackApplied, fmt.Sprintf("deposit %s: %s %s to %s", d.ID, d.Amount, d.Asset, owner), nil
}

// matchManual applies the custodian's late callback of a deposit an
// administrator backfilled (design 2026-10-02 §4.3): the same address,
// asset and amount confirm it (APPLIED: booked already); anything else is
// recorded on the deposit as a discrepancy for a person and counted for
// the alert, never corrected or booked again (DISCREPANCY).
func (s *Service) matchManual(ctx context.Context, r ports.Repos, provider string, d domain.Deposit, t ports.CustodyTrade, now time.Time) (string, string, error) {
	asset, amount := "", t.Amount
	if net, ok, err := s.networkOfCoin(ctx, provider, t.Coin); err != nil {
		return "", "", err
	} else if ok {
		asset, amount = net.Asset, t.Amount.Truncate(net.Decimals)
	}
	matched := d.MatchCallback(provider+":"+t.TradeID, t.Address, asset, amount, now)
	if err := r.Deposits().Update(ctx, d); err != nil {
		return "", "", err
	}
	if matched {
		return domain.CallbackApplied, fmt.Sprintf("deposit %s, backfilled by %s, confirmed by the callback", d.ID, d.EnteredBy), nil
	}
	if s.Discrepancies != nil {
		s.Discrepancies.Inc()
	}
	s.Log.ErrorContext(ctx, "custodian callback disagrees with a backfilled deposit", "deposit_id", d.ID, "trade_id", t.TradeID,
		"entered_by", d.EnteredBy, "discrepancy", d.Discrepancy)
	return domain.CallbackDiscrepancy, fmt.Sprintf("deposit %s, backfilled by %s: %s", d.ID, d.EnteredBy, d.Discrepancy), nil
}

// contradicts reports whether the custodian's word denies what a finished
// withdrawal became: sent after it failed (its funds released), or failed
// after it was confirmed.
func contradicts(w *domain.Withdrawal, word string) bool {
	switch w.Status {
	case domain.WithdrawalFailed:
		return word == domain.CustodySuccess
	case domain.WithdrawalConfirmed:
		return word == domain.CustodyFailed || word == domain.CustodyRejected
	}
	return false
}

// applyWithdrawal applies the custodian's word on a withdrawal it was
// handed (domain.Withdrawal.Custodian). It reports whether the
// withdrawal is now to be settled; what it counts and logs goes to after,
// for once the transaction committed (a failed one is tried again).
func (s *Service) applyWithdrawal(ctx context.Context, r ports.Repos, provider string, t ports.CustodyTrade, now time.Time, after *[]func(),
) (string, string, bool, error) {
	if _, err := uuid.Parse(t.BusinessID); err != nil {
		return domain.CallbackUnmatched, "businessId " + t.BusinessID + " is not a withdrawal ID", false, nil
	}
	w, err := r.Withdrawals().GetForUpdate(ctx, t.BusinessID)
	if err != nil {
		return "", "", false, err
	}
	switch {
	case w == nil:
		return domain.CallbackUnmatched, "no withdrawal " + t.BusinessID, false, nil
	case w.Provider != provider:
		return domain.CallbackUnmatched, "withdrawal " + w.ID + " is not with " + provider, false, nil
	case t.Word == "":
		return domain.CallbackIgnored, fmt.Sprintf("unknown status %d", t.Status), false, nil
	}
	if contradicts(w, t.Word) {
		// Sent after we released it as failed, or failed after it was
		// confirmed: the only sign that money left while we unfroze it.
		// Nothing is reversed on its own; a person checks with the
		// custodian (Attention, CustodyWithdrawalContradiction).
		status := w.Status
		*after = append(*after, func() {
			if s.Contradictions != nil {
				s.Contradictions.Inc()
			}
			s.Log.ErrorContext(ctx, "the custodian contradicts a finished withdrawal: nothing reversed, a person checks",
				"withdrawal_id", w.ID, "status", status, "custodian_says", t.Word, "tx", t.TxHash)
		})
		return domain.CallbackDiscrepancy, fmt.Sprintf("withdrawal %s is %s but the custodian says %s (tx %q): nothing reversed, a person checks",
			w.ID, w.Status, t.Word, t.TxHash), false, nil
	}
	if !w.Custodian(t.Word, t.TxHash, now) {
		return domain.CallbackIgnored, fmt.Sprintf("withdrawal %s is %s (%s)", w.ID, w.Status, w.ProviderStatus), false, nil
	}
	if err := r.Withdrawals().Update(ctx, *w); err != nil {
		return "", "", false, err
	}
	switch w.Status {
	case domain.WithdrawalConfirmed:
		// What the custodian charged the platform for sending it: booked
		// like gas, from GAS_SUPPLY (ADR-0011), or held for a person.
		fee, feeNote, err := s.custodyFee(ctx, r, provider, *w, t, after)
		if err != nil {
			return "", "", false, err
		}
		if fee != nil {
			if err := r.ChainFees().Insert(ctx, *fee); err != nil {
				return "", "", false, err
			}
		}
		if err := r.EmitWithdrawal(ctx, &walletv1.WithdrawalConfirmed{Withdrawal: WithdrawalProto(*w)}, w.UserID); err != nil {
			return "", "", false, err
		}
		return domain.CallbackApplied, "withdrawal " + w.ID + " sent in " + t.TxHash + feeNote, true, nil
	case domain.WithdrawalFailed:
		s.Log.ErrorContext(ctx, "the custodian did not send a withdrawal", "withdrawal_id", w.ID, "reason", w.RejectReason)
		if err := r.EmitWithdrawal(ctx, &walletv1.WithdrawalFailed{Withdrawal: WithdrawalProto(*w)}, w.UserID); err != nil {
			return "", "", false, err
		}
		return domain.CallbackApplied, "withdrawal " + w.ID + " " + w.RejectReason, false, nil
	}
	return domain.CallbackApplied, "withdrawal " + w.ID + ": " + w.ProviderStatus, false, nil
}

// ResolveCustodyWithdrawal records what a person found out about a
// withdrawal with the custodian whose outcome the platform does not know
// (CustodyUncertain, or a callback that never came): sent in tx, or not
// sent. The processors then settle a sent one and release an unsent one,
// as after the custodian's callback.
func ResolveCustodyWithdrawal(ctx context.Context, store ports.Store, id string, sent bool, tx, actor, reason string, now time.Time,
) (domain.Withdrawal, error) {
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" {
		return domain.Withdrawal{}, apperr.Invalid("an actor and a reason are required")
	}
	if sent && strings.TrimSpace(tx) == "" {
		return domain.Withdrawal{}, apperr.Invalid("a sent withdrawal needs its transaction")
	}
	var out domain.Withdrawal
	err := store.Tx(ctx, func(r ports.Repos) error {
		w, err := r.Withdrawals().GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if w == nil || w.Provider == "" {
			return apperr.NotFound("no such withdrawal with a custodian")
		}
		word := domain.CustodyFailed
		if sent {
			word = domain.CustodySuccess
		}
		// Still being handed over (the processor hands it over again every
		// minute until the custodian answers): called failed now, a later
		// handover the custodian accepts would send funds already released.
		if !sent && w.ProviderStatus == domain.CustodySubmitted {
			return apperr.New(apperr.KindConflict, "WALLET_CUSTODY_HANDOVER_PENDING",
				"the withdrawal is still being handed over to the custodian: wait for its answer (or for UNCERTAIN) before calling it failed")
		}
		from := w.ProviderStatus
		if !w.Custodian(word, tx, now) {
			return apperr.New(apperr.KindConflict, apperr.CodeConflict, "the withdrawal is "+w.Status+", not with the custodian")
		}
		if !sent {
			w.RejectReason = "CUSTODY_FAILED: resolved by " + actor + ": " + reason
		}
		if err := r.Withdrawals().Update(ctx, *w); err != nil {
			return err
		}
		if err := r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "withdrawal:" + w.ID, Action: "wallet.custody.withdrawal.resolve", Actor: actor, Reason: reason,
			Details: fmt.Sprintf(`{"provider":%q,"from":%q,"to":%q,"tx_hash":%q}`, w.Provider, from, word, tx),
		}, actor); err != nil {
			return err
		}
		out = *w
		if sent {
			return r.EmitWithdrawal(ctx, &walletv1.WithdrawalConfirmed{Withdrawal: WithdrawalProto(*w)}, w.UserID)
		}
		return r.EmitWithdrawal(ctx, &walletv1.WithdrawalFailed{Withdrawal: WithdrawalProto(*w)}, w.UserID)
	})
	return out, err
}

// settleOne books a confirmed custody withdrawal in the ledger now; the
// processor retries what fails here.
func (s *Service) settleOne(ctx context.Context, id string) {
	w, err := s.Store.Read().Withdrawals().Get(ctx, id)
	if err != nil || w == nil || w.Status != domain.WithdrawalConfirmed || w.SettleJournal != "" {
		return
	}
	o := netOps{Store: s.Store, Ledger: s.W.Ledger, Log: s.Log, Now: s.Now, Network: w.Network}
	if err := o.settleWithdrawal(ctx, *w); err != nil {
		s.Log.WarnContext(ctx, "settling a withdrawal failed, the processor retries", "withdrawal_id", id, "error", err)
	}
}

// Replayable reports whether an operator may apply a stored callback
// again: a verified one that failed, found nothing or was never finished.
func Replayable(c domain.Callback) bool {
	return c.SignatureOK && (c.Result == domain.CallbackFailed || c.Result == domain.CallbackUnmatched || c.Result == domain.CallbackReceived)
}

// ReplayCallback applies a stored callback again for an operator, its
// signature checked again but not its age, with an audit event.
func (s *Service) ReplayCallback(ctx context.Context, id, actor, reason string) (domain.Callback, error) {
	if _, err := uuid.Parse(id); err != nil {
		return domain.Callback{}, apperr.NotFound("no such callback")
	}
	if strings.TrimSpace(actor) == "" || len(strings.TrimSpace(reason)) < 3 {
		return domain.Callback{}, apperr.Invalid("the operator and a reason are required")
	}
	cb, err := s.Store.Read().Callbacks().Get(ctx, id)
	if err != nil {
		return domain.Callback{}, err
	}
	if cb == nil {
		return domain.Callback{}, apperr.NotFound("no such callback")
	}
	if !Replayable(*cb) {
		return domain.Callback{}, apperr.New(apperr.KindConflict, apperr.CodeConflict, "only a verified callback that failed or found nothing is replayed")
	}
	c := s.Custodians[cb.Provider]
	if c == nil {
		return domain.Callback{}, apperr.New(apperr.KindUnavailable, "WALLET_UNAVAILABLE", "the custodian is not configured")
	}
	t, err := c.Parse("", []byte(cb.Raw), s.Now(), 0)
	if err != nil {
		return domain.Callback{}, err
	}
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "custody-callback:" + cb.ID, Action: "wallet.custody.callback.replay", Actor: actor, Reason: reason,
			Details: fmt.Sprintf(`{"provider":%q,"trade_id":%q,"result":%q}`, cb.Provider, cb.TradeID, cb.Result),
		}, actor)
	})
	if err != nil {
		return domain.Callback{}, err
	}
	out, err := s.apply(ctx, c, *cb, t)
	if err != nil {
		return out, apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "the callback could not be applied: "+out.Detail)
	}
	return out, nil
}

// CustodyOverview is the custodian's state for the admin console.
type CustodyOverview struct {
	Provider   string
	Configured bool
	// Error says why the coins could not be read.
	Error string
	Coins []ports.CustodyCoin
	// Networks maps a coin to the networks that use it.
	Networks  map[string][]domain.Network
	Checks    []domain.ChainCheck
	Submitted []domain.Withdrawal
	Attention int
	LastAt    time.Time
}

// Custody describes the custodian provider for the admin console.
func (s *Service) Custody(ctx context.Context, provider string) (CustodyOverview, error) {
	out := CustodyOverview{Provider: provider, Networks: map[string][]domain.Network{}}
	c := s.Custodians[provider]
	out.Configured = c != nil
	nets, err := s.Networks.ForAsset(ctx, "")
	if err != nil {
		return out, err
	}
	for _, n := range nets {
		if n.Provider == provider {
			out.Networks[n.ProviderCoin] = append(out.Networks[n.ProviderCoin], n)
		}
	}
	if c != nil {
		if out.Coins, err = c.Coins(ctx); err != nil {
			out.Error = err.Error()
		}
	}
	r := s.Store.Read()
	checks, err := r.Checks().Latest(ctx, "")
	if err != nil {
		return out, err
	}
	// Its own and the platform's wallets', not another custodian's.
	for _, c := range checks {
		if c.Network == provider || !s.custodian(c.Network) {
			out.Checks = append(out.Checks, c)
		}
	}
	if out.Submitted, err = r.Withdrawals().Submitted(ctx, provider); err != nil {
		return out, err
	}
	out.Attention, out.LastAt, err = r.Callbacks().Attention(ctx, provider)
	return out, err
}

// custodian reports whether a check's holder is a custodian rather than a
// network of the platform's own wallets.
func (s *Service) custodian(holder string) bool {
	_, ok := s.Custodians[holder]
	return ok || holder == domain.ProviderUdun || holder == domain.ProviderUdunMock
}

// Callbacks returns a page of the custodian's callbacks, newest first,
// and the next cursor.
func (s *Service) Callbacks(ctx context.Context, f ports.CallbackFilter) ([]domain.Callback, string, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	limit := f.Limit
	f.Limit++
	list, err := s.Store.Read().Callbacks().Page(ctx, f)
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

// Callback returns one stored callback.
func (s *Service) Callback(ctx context.Context, id string) (domain.Callback, error) {
	if _, err := uuid.Parse(id); err != nil {
		return domain.Callback{}, apperr.NotFound("no such callback")
	}
	cb, err := s.Store.Read().Callbacks().Get(ctx, id)
	if err != nil {
		return domain.Callback{}, err
	}
	if cb == nil {
		return domain.Callback{}, apperr.NotFound("no such callback")
	}
	return *cb, nil
}

// inFlight sums the amounts of the withdrawals with the custodian by
// asset.
// inFlight sums, per asset, the outstanding withdrawals the custodian has
// taken (its balance no longer holds them) and the ledger has not settled
// (it still expects them held). One handed over without an answer yet, or
// uncertain, is not counted: the custodian may not hold it, and counting
// it would hide a shortfall as large.
// unknownOutcome is what of each asset the withdrawals with an unknown
// outcome may have taken from the custodian: handed over unanswered
// (SUBMITTED) or UNCERTAIN.
func unknownOutcome(list []domain.Withdrawal) map[string]decimal.Decimal {
	out := map[string]decimal.Decimal{}
	for _, w := range list {
		if w.ProviderStatus == domain.CustodySubmitted || w.ProviderStatus == domain.CustodyUncertain {
			out[w.Asset] = out[w.Asset].Add(w.Amount)
		}
	}
	return out
}

func inFlight(list []domain.Withdrawal) map[string]decimal.Decimal {
	out := map[string]decimal.Decimal{}
	for _, w := range list {
		switch w.ProviderStatus {
		case domain.CustodyAccepted, domain.CustodyReview, domain.CustodyApproved, domain.CustodySuccess:
			out[w.Asset] = out[w.Asset].Add(w.Amount)
		}
	}
	return out
}
