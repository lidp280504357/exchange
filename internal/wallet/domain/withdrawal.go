package domain

import (
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Withdrawal statuses (§11.6, appendix B). The platform's own wallets
// take an approved withdrawal through SIGNING, BROADCAST and CONFIRMING; a
// custodian's network hands it over (SUBMITTED) until the custodian
// reports (ADR-0011).
const (
	WithdrawalRequested  = "REQUESTED"
	WithdrawalReview     = "PENDING_REVIEW"
	WithdrawalApproved   = "APPROVED"
	WithdrawalSigning    = "SIGNING"
	WithdrawalBroadcast  = "BROADCAST"
	WithdrawalConfirming = "CONFIRMING"
	WithdrawalSubmitted  = "SUBMITTED"
	WithdrawalConfirmed  = "CONFIRMED"
	WithdrawalInternal   = "INTERNAL_TRANSFER"
	WithdrawalRejected   = "REJECTED"
	WithdrawalCanceled   = "CANCELED"
	WithdrawalFailed     = "FAILED"
)

// What the custodian last said of a withdrawal (its provider status):
// handed over but not acknowledged yet, taken, in its review, approved by
// it, refused, sent, or failed on chain. Uncertain is ours: handed over
// again after an unknown outcome and refused, though the custodian may
// hold the first hand-over (its refusal can say "insufficient balance"
// because the first one took the balance), so the withdrawal waits for its
// callback or a person; nothing is released.
const (
	CustodySubmitted = "SUBMITTED"
	CustodyAccepted  = "ACCEPTED"
	CustodyReview    = "REVIEW"
	CustodyApproved  = "APPROVED"
	CustodyRejected  = "REJECTED"
	CustodySuccess   = "SUCCESS"
	CustodyFailed    = "FAILED"
	CustodyUncertain = "UNCERTAIN"
)

// Risk reasons that send a withdrawal to review (§11.6).
const (
	RiskNewAccount     = "NEW_ACCOUNT"
	RiskNewDevice      = "NEW_DEVICE"
	RiskSecurityChange = "SECURITY_CHANGE"
	RiskNewAddress     = "NEW_ADDRESS"
	RiskLargeAmount    = "LARGE_AMOUNT"
	RiskDailyShare     = "DAILY_SHARE"
)

// Errors (appendix C).
var (
	ErrNotWhitelisted = apperr.New(apperr.KindUnprocessable, "WALLET_ADDRESS_NOT_WHITELISTED", "add the address to your withdrawal addresses first")
	ErrCooldown       = apperr.New(apperr.KindUnprocessable, "WALLET_ADDRESS_COOLDOWN", "a new withdrawal address can be used after its cooling-off period")
	ErrLimitExceeded  = apperr.New(apperr.KindUnprocessable, "WALLET_LIMIT_EXCEEDED", "the withdrawal exceeds your limit")
	ErrBelowMinimum   = apperr.New(apperr.KindUnprocessable, "WALLET_BELOW_MINIMUM", "the amount is below the minimum withdrawal")
	ErrInvalidAddress = apperr.New(apperr.KindInvalid, "WALLET_INVALID_ADDRESS", "not a valid address for this network")
	ErrNotCancelable  = apperr.New(apperr.KindConflict, "WALLET_WITHDRAWAL_NOT_CANCELABLE", "the withdrawal is already being sent")
	ErrOwnAddress     = apperr.New(apperr.KindUnprocessable, "WALLET_OWN_ADDRESS", "this is your own deposit address")
	ErrWithdrawClosed = apperr.New(apperr.KindUnprocessable, "WALLET_NETWORK_DISABLED", "withdrawals of this asset on this network are closed")
	// ErrWithdrawSuspended refuses a withdrawal of an asset whose
	// withdrawals are suspended (Suspension).
	ErrWithdrawSuspended = apperr.New(apperr.KindUnprocessable, "WALLET_WITHDRAW_SUSPENDED",
		"withdrawals of this asset are paused while the platform checks its funds")
)

// Suspension stops an asset's withdrawals (design 2026-09-30 §9, review
// B4): the custody check found funds missing that no withdrawal with an
// unknown outcome explains, on two checks minutes apart
// (SuspendedBySystem), or an operator suspended them. New requests are
// refused and approved withdrawals wait; a person lifts it.
type Suspension struct {
	Asset       string
	Shortfall   decimal.Decimal
	Reason      string
	SuspendedBy string
	SuspendedAt time.Time
}

// SuspendedBySystem names the custody check as the suspender.
const SuspendedBySystem = "system:custody-check"

// ShortfallWatch is what the custody checks keep of an asset between
// checks: since when funds are missing beyond its threshold (zero: not at
// the last check), and a difference a person accepted when lifting a
// suspension, which the checks do not count as missing until
// AcceptedUntil (review of ebb8aaa, H2).
type ShortfallWatch struct {
	Asset         string
	SuspectSince  time.Time
	Accepted      decimal.Decimal
	AcceptedUntil time.Time
	AcceptedBy    string
}

// AcceptedAt is the difference accepted at now: zero once it lapsed.
func (w ShortfallWatch) AcceptedAt(now time.Time) decimal.Decimal {
	if !now.Before(w.AcceptedUntil) {
		return decimal.Zero
	}
	return w.Accepted
}

// MaxAcceptFor is the longest a person may accept a difference for: a
// standing one needs a correction in the ledger, not a longer acceptance.
const MaxAcceptFor = 7 * 24 * time.Hour

// Withdrawal is a request to send an asset out.
type Withdrawal struct {
	ID      string
	UserID  string
	Asset   string
	Network string
	Address string
	Amount  decimal.Decimal
	Fee     decimal.Decimal
	// InternalUserID owns the destination when it is a platform deposit
	// address: the withdrawal completes in the ledger.
	InternalUserID string
	// Provider is the custodian that sends it (its network's), empty for
	// the platform's own wallets; ProviderStatus is what it last said.
	Provider          string
	ProviderStatus    string
	Status            string
	RiskScore         int
	RiskReasons       []string
	ApprovalsRequired int
	Approvals         []string
	RejectReason      string
	// ValueUSDT is the amount's worth at the day's price, for the limits.
	ValueUSDT     decimal.Decimal
	Nonce         int64 // -1 until one is assigned
	TxHash        string
	BlockNumber   uint64
	Confirmations uint32
	Required      uint32
	// Journals of the ledger: the freeze, the settlement (or internal
	// transfer) and the release of a refused one.
	FreezeJournal   string
	SettleJournal   string
	UnfreezeJournal string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ApprovedAt      time.Time
	SubmittedAt     time.Time
	BroadcastAt     time.Time
	ConfirmedAt     time.Time
	// HeldAt is set while a reviewer put the withdrawal in review aside
	// (design 2026-10-02 §4.2), by HeldBy with HoldNote.
	HeldAt   time.Time
	HeldBy   string
	HoldNote string
}

// ErrNotInReview refuses a hold of a withdrawal that is not in review.
var ErrNotInReview = apperr.New(apperr.KindConflict, "WALLET_WITHDRAWAL_NOT_IN_REVIEW", "only a withdrawal in review can be put on hold")

// Hold puts a withdrawal in review aside with a note while a reviewer
// looks into it; it stays in review and may still be approved or
// rejected. Holding it again replaces the note.
func (w *Withdrawal) Hold(reviewer, note string, now time.Time) error {
	reviewer, note = strings.TrimSpace(reviewer), strings.TrimSpace(note)
	switch {
	case w.Status != WithdrawalReview:
		return ErrNotInReview
	case reviewer == "":
		return apperr.Invalid("the reviewer is required")
	case len(note) < 3:
		return apperr.Invalid("a note of at least 3 characters is required")
	}
	w.HeldAt, w.HeldBy, w.HoldNote, w.UpdatedAt = now, reviewer, note, now
	return nil
}

// Unhold takes a withdrawal off hold; it reports whether it was held.
func (w *Withdrawal) Unhold(now time.Time) bool {
	if w.HeldAt.IsZero() {
		return false
	}
	w.HeldAt, w.HeldBy, w.HoldNote, w.UpdatedAt = time.Time{}, "", "", now
	return true
}

// Custody reports whether a custodian sends the withdrawal.
func (w *Withdrawal) Custody() bool { return w.Provider != "" }

// Frozen is what the request froze: the amount and the fee.
func (w *Withdrawal) Frozen() decimal.Decimal { return w.Amount.Add(w.Fee) }

func (w *Withdrawal) set(status string, now time.Time) {
	w.Status, w.UpdatedAt = status, now
}

// Scored records the risk assessment: approved at once without reasons,
// otherwise waiting for review.
func (w *Withdrawal) Scored(r RiskResult, now time.Time) {
	w.RiskScore, w.RiskReasons, w.ApprovalsRequired = r.Score, r.Reasons, r.Approvals
	if r.Approvals == 0 {
		w.set(WithdrawalApproved, now)
		w.ApprovedAt = now
		return
	}
	w.set(WithdrawalReview, now)
}

// Approve adds a reviewer's approval; it reports whether the withdrawal
// is now approved. Every approval must come from another reviewer.
func (w *Withdrawal) Approve(reviewer string, now time.Time) (bool, error) {
	reviewer = strings.TrimSpace(reviewer)
	switch {
	case w.Status != WithdrawalReview:
		return false, apperr.New(apperr.KindConflict, apperr.CodeConflict, fmt.Sprintf("the withdrawal is %s, not waiting for review", w.Status))
	case reviewer == "":
		return false, apperr.Invalid("the reviewer is required")
	case slices.Contains(w.Approvals, reviewer):
		return false, apperr.New(apperr.KindConflict, apperr.CodeConflict, "each approval must come from another reviewer")
	}
	w.Approvals = append(w.Approvals, reviewer)
	w.UpdatedAt = now
	if len(w.Approvals) < w.ApprovalsRequired {
		return false, nil
	}
	w.set(WithdrawalApproved, now)
	w.ApprovedAt = now
	return true, nil
}

// ApproveAlone is Approve by a reviewer whose approval alone completes the
// review of a withdrawal worth at most limit USDT, however many reviewers
// it needed (the admin console's single-person mode); a larger one still
// waits for its other reviewers, and a zero limit changes nothing.
func (w *Withdrawal) ApproveAlone(reviewer string, limit decimal.Decimal, now time.Time) (bool, error) {
	if limit.IsPositive() && !w.ValueUSDT.GreaterThan(limit) && w.Status == WithdrawalReview {
		w.ApprovalsRequired = min(w.ApprovalsRequired, len(w.Approvals)+1)
	}
	return w.Approve(reviewer, now)
}

// MaxApprovalsRaised bounds what RequireApprovals raises to: two
// reviewers is all the console asks for, and a larger number sent by
// mistake would keep the withdrawal in review for good (C5.5 ⑰).
const MaxApprovalsRaised = 2

// RequireApprovals raises the approvals a withdrawal in review needs to at
// least n (at most MaxApprovalsRaised), never lowering them: the admin
// console in single-person mode asks for 2 when it is worth more than an
// approval may complete alone at the current price, or has no fresh price
// (C5.5 ⑮).
func (w *Withdrawal) RequireApprovals(n int) {
	n = min(n, MaxApprovalsRaised)
	if w.Status == WithdrawalReview && n > w.ApprovalsRequired {
		w.ApprovalsRequired = n
	}
}

// Reject refuses a withdrawal that is not being sent yet.
func (w *Withdrawal) Reject(reason string, now time.Time) error {
	if !w.Cancelable() {
		return ErrNotCancelable
	}
	w.RejectReason = reason
	w.set(WithdrawalRejected, now)
	return nil
}

// Cancelable reports whether the withdrawal can still be withdrawn: until
// it enters SIGNING (§11.6) or goes to the custodian.
func (w *Withdrawal) Cancelable() bool {
	return w.Status == WithdrawalRequested || w.Status == WithdrawalReview || w.Status == WithdrawalApproved
}

// Cancel withdraws the request on the user's behalf.
func (w *Withdrawal) Cancel(now time.Time) error {
	if !w.Cancelable() {
		return ErrNotCancelable
	}
	w.set(WithdrawalCanceled, now)
	return nil
}

// NeedsRelease reports whether a refused withdrawal, or one that failed
// before anything was broadcast, still has frozen funds to release.
func (w *Withdrawal) NeedsRelease() bool {
	refused := w.Status == WithdrawalRejected || w.Status == WithdrawalCanceled || (w.Status == WithdrawalFailed && w.TxHash == "")
	return refused && w.FreezeJournal != "" && w.UnfreezeJournal == ""
}

// Submit hands an approved withdrawal to its custodian; it reports false
// when it is not APPROVED any more (a cancellation won).
func (w *Withdrawal) Submit(now time.Time) bool {
	if w.Status != WithdrawalApproved || !w.Custody() {
		return false
	}
	w.ProviderStatus, w.SubmittedAt = CustodySubmitted, now
	w.set(WithdrawalSubmitted, now)
	return true
}

// Custodian applies what the custodian says of a SUBMITTED withdrawal: it
// took it or reviews it (it stays SUBMITTED), sent it in tx (CONFIRMED,
// for the ledger to settle), or refused or failed it (FAILED: nothing
// left the platform, so the frozen funds go back). It reports whether
// anything changed; a word on a withdrawal past SUBMITTED changes nothing
// (a repeat, or the review arriving after the outcome).
func (w *Withdrawal) Custodian(word, tx string, now time.Time) bool {
	if w.Status != WithdrawalSubmitted {
		return false
	}
	switch word {
	case CustodyAccepted, CustodyReview, CustodyApproved:
		if w.ProviderStatus == word || (word == CustodyAccepted && w.ProviderStatus != CustodySubmitted) {
			return false
		}
		w.ProviderStatus, w.UpdatedAt = word, now
	case CustodySuccess:
		w.ProviderStatus, w.TxHash, w.Confirmations, w.ConfirmedAt, w.RejectReason = word, tx, w.Required, now, ""
		w.set(WithdrawalConfirmed, now)
	case CustodyRejected, CustodyFailed:
		w.ProviderStatus, w.RejectReason = word, "CUSTODY_"+word
		if tx != "" {
			w.RejectReason += ": " + tx
		}
		w.set(WithdrawalFailed, now)
	default:
		return false
	}
	return true
}

// Uncertain marks a withdrawal with the custodian whose hand-over was
// refused on a retry (CustodyUncertain); reason is the refusal.
func (w *Withdrawal) Uncertain(reason string, now time.Time) bool {
	// Only while no word came from the custodian: one that answered since
	// (ACCEPTED, REVIEW, APPROVED) holds it, and its word stands.
	if w.Status != WithdrawalSubmitted || w.ProviderStatus != CustodySubmitted {
		return false
	}
	w.ProviderStatus, w.RejectReason, w.UpdatedAt = CustodyUncertain, "UNCERTAIN: "+reason, now
	return true
}

// Broadcasted records the first broadcast of nonce with tx.
func (w *Withdrawal) Broadcasted(nonce uint64, tx string, now time.Time) {
	w.Nonce, w.TxHash, w.BroadcastAt = int64(nonce), tx, now //nolint:gosec // nonces fit
	w.set(WithdrawalBroadcast, now)
}

// Mined records the block of the mined attempt tx and the confirmations
// seen at head; it reports whether the withdrawal is now CONFIRMED.
func (w *Withdrawal) Mined(tx string, block, head uint64, now time.Time) bool {
	if w.Status != WithdrawalBroadcast && w.Status != WithdrawalConfirming {
		return false
	}
	w.TxHash, w.BlockNumber = tx, block
	conf := uint64(0)
	if head >= block {
		conf = head - block + 1
	}
	w.Confirmations = uint32(min(conf, uint64(w.Required))) //nolint:gosec // at most Required
	if conf < uint64(w.Required) {
		w.set(WithdrawalConfirming, now)
		return false
	}
	w.set(WithdrawalConfirmed, now)
	w.ConfirmedAt = now
	return true
}

// Attempt is one signed transaction of a withdrawal; replacements share
// its nonce with a higher fee.
type Attempt struct {
	TxHash       string
	WithdrawalID string
	Nonce        uint64
	MaxFee       *big.Int
	MaxTip       *big.Int
	Raw          string
	CreatedAt    time.Time
}

// WithdrawAddress is an entry of a user's withdrawal address book.
type WithdrawAddress struct {
	ID        string
	UserID    string
	Network   string
	Address   string
	Label     string
	CreatedAt time.Time
	// UsableAt ends the cooling-off period (§5.10: 24 hours).
	UsableAt time.Time
}

// RiskInput is what the withdrawal risk rules weigh (§11.6).
type RiskInput struct {
	Now               time.Time
	AccountCreated    time.Time
	DeviceFirstSeen   time.Time // zero when unknown: treated as new
	IdentityChanged   time.Time
	PasswordChanged   time.Time
	TOTPChanged       time.Time // the authenticator app removed (by the user or an administrator)
	AddressAdded      time.Time
	ValueUSDT         decimal.Decimal
	DailyUSDT         decimal.Decimal // today's withdrawals including this one
	DailyLimit        decimal.Decimal
	LargeUSDT         decimal.Decimal // review above this (1000)
	DoubleReviewUSDT  decimal.Decimal // two reviewers above this (20000)
	NewAccountPeriod  time.Duration   // 72h
	NewDevicePeriod   time.Duration   // 24h
	SecurityPeriod    time.Duration   // 24h
	NewAddressPeriod  time.Duration   // 72h
	ReviewDailyShare  decimal.Decimal // review past this share of the daily limit (0.5)
	InternalRecipient bool
}

// RiskResult is the assessment: a score, the reasons, and how many
// reviewers must approve.
type RiskResult struct {
	Score     int
	Reasons   []string
	Approvals int
}

// DefaultRisk fills in the thresholds of §11.6.
func DefaultRisk(in RiskInput) RiskInput {
	d := decimal.NewFromInt
	if in.LargeUSDT.IsZero() {
		in.LargeUSDT = d(1000)
	}
	if in.DoubleReviewUSDT.IsZero() {
		in.DoubleReviewUSDT = d(20000)
	}
	if in.ReviewDailyShare.IsZero() {
		in.ReviewDailyShare = decimal.RequireFromString("0.5")
	}
	for _, p := range []*time.Duration{&in.NewAccountPeriod, &in.NewAddressPeriod} {
		if *p == 0 {
			*p = 72 * time.Hour
		}
	}
	for _, p := range []*time.Duration{&in.NewDevicePeriod, &in.SecurityPeriod} {
		if *p == 0 {
			*p = 24 * time.Hour
		}
	}
	return in
}

// Assess applies the risk rules: each reason adds to the score and sends
// the withdrawal to one reviewer; a very large one needs two.
func Assess(in RiskInput) RiskResult {
	in = DefaultRisk(in)
	var r RiskResult
	hit := func(reason string, score int) {
		r.Reasons = append(r.Reasons, reason)
		r.Score += score
	}
	recent := func(t time.Time, within time.Duration) bool { return t.IsZero() || in.Now.Sub(t) < within }
	if recent(in.AccountCreated, in.NewAccountPeriod) {
		hit(RiskNewAccount, 40)
	}
	if recent(in.DeviceFirstSeen, in.NewDevicePeriod) {
		hit(RiskNewDevice, 30)
	}
	latest := in.IdentityChanged
	for _, t := range []time.Time{in.PasswordChanged, in.TOTPChanged} {
		if t.After(latest) {
			latest = t
		}
	}
	if !latest.IsZero() && in.Now.Sub(latest) < in.SecurityPeriod {
		hit(RiskSecurityChange, 30)
	}
	if recent(in.AddressAdded, in.NewAddressPeriod) {
		hit(RiskNewAddress, 20)
	}
	if in.ValueUSDT.GreaterThan(in.LargeUSDT) {
		hit(RiskLargeAmount, 30)
	}
	if in.DailyLimit.IsPositive() && in.DailyUSDT.GreaterThan(in.DailyLimit.Mul(in.ReviewDailyShare)) {
		hit(RiskDailyShare, 20)
	}
	r.Score = min(r.Score, 100)
	switch {
	case in.ValueUSDT.GreaterThan(in.DoubleReviewUSDT):
		r.Approvals = 2
	case len(r.Reasons) > 0:
		r.Approvals = 1
	}
	return r
}

// Limits are a user's withdrawal limits in USDT (§11.6, no KYC).
type Limits struct {
	Daily   decimal.Decimal
	Monthly decimal.Decimal
}

// LimitsFor returns the limits of a user: both identities and an
// authenticator app get the full 2,000 a day and 20,000 a month, anyone
// else 20% of that.
func LimitsFor(identities int, totp bool) Limits {
	full := Limits{Daily: decimal.NewFromInt(2000), Monthly: decimal.NewFromInt(20000)}
	if identities >= 2 && totp {
		return full
	}
	share := decimal.RequireFromString("0.2")
	return Limits{Daily: full.Daily.Mul(share), Monthly: full.Monthly.Mul(share)}
}
