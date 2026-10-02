// Package domain holds the ledger's model (requirements §11.4, ADR-0001):
// accounts, journals whose lines sum to zero per asset, and the rules for
// applying them. Amounts are decimals (ADR-0008).
package domain

import (
	"cmp"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Owner types.
const (
	OwnerUser   = "USER"
	OwnerSystem = "SYSTEM"
)

// Account types (§11.4). Users hold SPOT and FUTURES; the rest are system
// accounts, one per asset (PNL_CLEARING is in futures.go).
const (
	AccountSpot              = "SPOT"
	AccountFutures           = "FUTURES"
	AccountFeeRevenue        = "FEE_REVENUE"
	AccountInsuranceFund     = "INSURANCE_FUND"
	AccountDepositPending    = "DEPOSIT_PENDING"
	AccountWithdrawalPending = "WITHDRAWAL_PENDING"
	AccountUnclaimedDeposit  = "UNCLAIMED_DEPOSIT"
	AccountFundingClearing   = "FUNDING_CLEARING"
	AccountMarketMaker       = "MARKET_MAKER"
	AccountGasSupply         = "GAS_SUPPLY"
	AccountAdjustment        = "ADJUSTMENT"
)

// Balance kinds.
const (
	Available = "AVAILABLE"
	Frozen    = "FROZEN"
)

// Entry types (§11.4).
const (
	EntryDepositCredit         = "DEPOSIT_CREDIT"
	EntryWithdrawFreeze        = "WITHDRAW_FREEZE"
	EntryWithdrawSettle        = "WITHDRAW_SETTLE"
	EntryWithdrawUnfreeze      = "WITHDRAW_UNFREEZE"
	EntryOrderFreeze           = "ORDER_FREEZE"
	EntryOrderUnfreeze         = "ORDER_UNFREEZE"
	EntryTradeSettle           = "TRADE_SETTLE"
	EntryHouseTradeSettle      = "HOUSE_TRADE_SETTLE"
	EntryTradeFee              = "TRADE_FEE"
	EntryAccountTransfer       = "ACCOUNT_TRANSFER"
	EntryFundingPayment        = "FUNDING_PAYMENT"
	EntryRealizedPnL           = "REALIZED_PNL"
	EntryLiquidationSettle     = "LIQUIDATION_SETTLE"
	EntryInsuranceContribution = "INSURANCE_CONTRIBUTION"
	EntryADLSettle             = "ADL_SETTLE"
	EntryInternalTransfer      = "INTERNAL_TRANSFER"
	EntryManualAdjustment      = "MANUAL_ADJUSTMENT"
	// An administrator's hold on part of a balance and its release
	// (design 2026-10-02 §4.1, risk control).
	EntryAdminFreeze   = "ADMIN_FREEZE"
	EntryAdminUnfreeze = "ADMIN_UNFREEZE"
)

var entryTypes = []string{
	EntryDepositCredit, EntryWithdrawFreeze, EntryWithdrawSettle, EntryWithdrawUnfreeze, EntryOrderFreeze, EntryOrderUnfreeze,
	EntryTradeSettle, EntryTradeFee, EntryAccountTransfer, EntryFundingPayment, EntryRealizedPnL, EntryLiquidationSettle,
	EntryInsuranceContribution, EntryADLSettle, EntryInternalTransfer, EntryManualAdjustment, EntryHouseTradeSettle,
	EntryAdminFreeze, EntryAdminUnfreeze,
}

var systemAccounts = []string{
	AccountFeeRevenue, AccountInsuranceFund, AccountDepositPending, AccountWithdrawalPending, AccountUnclaimedDeposit,
	AccountFundingClearing, AccountMarketMaker, AccountGasSupply, AccountAdjustment, AccountPnLClearing,
}

// Errors (appendix C).
var (
	ErrInsufficientBalance = apperr.New(apperr.KindUnprocessable, "LEDGER_INSUFFICIENT_BALANCE", "insufficient balance")
	ErrIdempotencyConflict = apperr.New(apperr.KindConflict, apperr.CodeIdempotencyConflict,
		"the idempotency key was used for a different request")
)

// AccountKey identifies an account.
type AccountKey struct {
	OwnerType string
	OwnerID   string
	Type      string
	Asset     string
}

// UserAccount is a user's SPOT or FUTURES account in asset.
func UserAccount(userID, accountType, asset string) AccountKey {
	return AccountKey{OwnerType: OwnerUser, OwnerID: userID, Type: accountType, Asset: asset}
}

// SystemAccount is the platform's account of a type in asset.
func SystemAccount(accountType, asset string) AccountKey {
	return AccountKey{OwnerType: OwnerSystem, OwnerID: OwnerSystem, Type: accountType, Asset: asset}
}

// Validate checks that the owner and the type go together.
func (k AccountKey) Validate() error {
	switch {
	case k.Asset == "":
		return apperr.Invalid("the asset is required")
	case k.OwnerType == OwnerUser && (k.Type == AccountSpot || k.Type == AccountFutures) && k.OwnerID != "":
		return nil
	case k.OwnerType == OwnerSystem && k.OwnerID == OwnerSystem && slices.Contains(systemAccounts, k.Type):
		return nil
	}
	return apperr.Invalid(fmt.Sprintf("invalid account %s/%s/%s", k.OwnerType, k.Type, k.Asset))
}

// HouseBacked are the assets HOUSE must hold to sell (ADR-0013): they can
// be deposited and withdrawn, so its MARKET_MAKER account of them never goes
// below zero. The internal assets it may sell short.
var HouseBacked = map[string]bool{"USDT": true, "BTC": true, "ETH": true}

// MayGoNegative reports whether the account is a counterparty allowed below
// zero (invariant 3): DEPOSIT_PENDING, ADJUSTMENT and PNL_CLEARING, and
// MARKET_MAKER, HOUSE's inventory, of an internal asset. A trade that would
// take HOUSE below zero in a backed asset is refused and parked (the house
// liquidity publisher and the engine keep its rooms within its holdings,
// so this is the last guard).
func (k AccountKey) MayGoNegative() bool {
	return k.Type == AccountDepositPending || k.Type == AccountAdjustment || k.Type == AccountPnLClearing ||
		(k.Type == AccountMarketMaker && !HouseBacked[k.Asset])
}

func (k AccountKey) String() string {
	return k.OwnerType + "/" + k.OwnerID + "/" + k.Type + "/" + k.Asset
}

// compareKeys orders accounts for locking, so concurrent postings cannot
// deadlock.
func compareKeys(a, b AccountKey) int {
	return cmp.Or(cmp.Compare(a.OwnerType, b.OwnerType), cmp.Compare(a.OwnerID, b.OwnerID),
		cmp.Compare(a.Type, b.Type), cmp.Compare(a.Asset, b.Asset))
}

// Account is a balance holder.
type Account struct {
	ID        string
	Key       AccountKey
	Available decimal.Decimal
	Frozen    decimal.Decimal
	Version   int64
	UpdatedAt time.Time
}

// Apply adds a line to the balances; an account that may not go negative
// refuses a line that takes the balance it changes below zero. A line that
// brings a balance up always passes, even while it stays below zero: an
// account that went negative (HOUSE's MARKET_MAKER in a backed asset, by a
// rule that changed) is made whole by credits, and the trades refused
// meanwhile settle on their retry.
func (a *Account) Apply(l Line) error {
	available, frozen := a.Available, a.Frozen
	changed := &available
	if l.Kind == Frozen {
		changed = &frozen
	}
	*changed = changed.Add(l.Amount)
	if !a.Key.MayGoNegative() && l.Amount.IsNegative() && changed.IsNegative() {
		kind := strings.ToLower(l.Kind)
		return ErrInsufficientBalance.WithDetail("asset", a.Key.Asset).WithDetail("account_type", a.Key.Type).WithDetail("balance", kind)
	}
	a.Available, a.Frozen = available, frozen
	a.Version++
	return nil
}

// Line is one leg of a journal.
type Line struct {
	Account AccountKey
	Amount  decimal.Decimal
	Kind    string
}

// Posting is a journal to write.
type Posting struct {
	IdemKey       string
	EntryType     string
	SourceEventID string
	TraceID       string
	Memo          string
	Lines         []Line
}

// Validate checks the posting: a known entry type, at least two non-zero
// lines on valid accounts, and lines summing to zero per asset
// (invariant 1).
func (p Posting) Validate() error {
	switch {
	case p.IdemKey == "" || len(p.IdemKey) > 200:
		return apperr.Invalid("an idempotency key of at most 200 bytes is required")
	case !slices.Contains(entryTypes, p.EntryType):
		return apperr.Invalid(fmt.Sprintf("unknown entry type %q", p.EntryType))
	case len(p.Lines) < 2:
		return apperr.Invalid("a journal has at least two lines")
	case len(p.Memo) > 500:
		return apperr.Invalid("the memo is too long")
	}
	sums := map[string]decimal.Decimal{}
	for _, l := range p.Lines {
		if err := l.Account.Validate(); err != nil {
			return err
		}
		if l.Amount.IsZero() {
			return apperr.Invalid("line amounts must not be zero")
		}
		if l.Kind != Available && l.Kind != Frozen {
			return apperr.Invalid(fmt.Sprintf("unknown balance kind %q", l.Kind))
		}
		sums[l.Account.Asset] = sums[l.Account.Asset].Add(l.Amount)
	}
	for asset, sum := range sums {
		if !sum.IsZero() {
			return apperr.Invalid(fmt.Sprintf("the lines in %s sum to %s, not zero", asset, sum))
		}
	}
	return nil
}

// Hash digests the posting's content, telling a replay of the same request
// from a different request under the same key. Metadata (source event,
// trace) is left out: a redelivery may carry it differently.
func (p Posting) Hash() []byte {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s\n", p.IdemKey, p.EntryType, p.Memo)
	for _, l := range p.Lines {
		fmt.Fprintf(h, "%s|%s|%s\n", l.Account, l.Amount.String(), l.Kind)
	}
	return h.Sum(nil)
}

// Accounts returns the distinct accounts of the posting in lock order.
func (p Posting) Accounts() []AccountKey {
	var keys []AccountKey
	for _, l := range p.Lines {
		if !slices.Contains(keys, l.Account) {
			keys = append(keys, l.Account)
		}
	}
	slices.SortFunc(keys, compareKeys)
	return keys
}

// Assets returns the distinct assets of the posting.
func (p Posting) Assets() []string {
	var out []string
	for _, l := range p.Lines {
		if !slices.Contains(out, l.Account.Asset) {
			out = append(out, l.Account.Asset)
		}
	}
	return out
}

// Journal is a posted journal.
type Journal struct {
	ID            string
	Seq           int64
	IdemKey       string
	RequestHash   []byte
	EntryType     string
	SourceEventID string
	TraceID       string
	Memo          string
	PostedAt      time.Time
}

// PostedLine is a line with the balances it left.
type PostedLine struct {
	Account        Account // the account after the line
	Amount         decimal.Decimal
	Kind           string
	AccountVersion int64
}

// Entry is a line of a user's fund flow.
type Entry struct {
	ID          int64
	JournalID   string
	EntryType   string
	AccountType string
	Asset       string
	Amount      decimal.Decimal
	Kind        string
	Available   decimal.Decimal
	Frozen      decimal.Decimal
	PostedAt    time.Time
}

// ReconciliationRun is one invariant check of one reconciliation: how
// many mismatches it found and the first of them (JSON [{key, detail}]).
type ReconciliationRun struct {
	Check      string
	StartedAt  time.Time
	Mismatches int
	Details    []byte
}
