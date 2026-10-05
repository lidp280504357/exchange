package domain

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

// Margin trading (margin design 2026-10-06 §3; E0 contract §7). A user's
// margin account — the cross account, or the isolated account of a pair —
// holds three rows per asset: the assets (MARGIN_CROSS, MARGIN_ISOLATED;
// available and frozen as on SPOT), the principal owed (..._DEBT) and the
// interest owed (..._INTEREST), the debts as negative available balances.
// The rows of an isolated account carry its pair as their scope.
// margin-service decides what may be borrowed, repaid or moved; the
// ledger books it, refusing what would take an asset row below zero or a
// debt row above it.
const (
	AccountMarginCross            = "MARGIN_CROSS"
	AccountMarginCrossDebt        = "MARGIN_CROSS_DEBT"
	AccountMarginCrossInterest    = "MARGIN_CROSS_INTEREST"
	AccountMarginIsolated         = "MARGIN_ISOLATED"
	AccountMarginIsolatedDebt     = "MARGIN_ISOLATED_DEBT"
	AccountMarginIsolatedInterest = "MARGIN_ISOLATED_INTEREST"
	// AccountMarginInterestIncome is HOUSE's interest income, booked when
	// the interest is charged.
	AccountMarginInterestIncome = "MARGIN_INTEREST_INCOME"
)

// Margin entry types (design §3.2).
const (
	EntryMarginTransferIn  = "MARGIN_TRANSFER_IN"
	EntryMarginTransferOut = "MARGIN_TRANSFER_OUT"
	EntryMarginBorrow      = "MARGIN_BORROW"
	EntryMarginInterest    = "MARGIN_INTEREST"
	EntryMarginRepay       = "MARGIN_REPAY"
	EntryMarginTradeSettle = "MARGIN_TRADE_SETTLE"
	EntryMarginLiquidate   = "MARGIN_LIQUIDATE"
)

var marginTypes = []string{
	AccountMarginCross, AccountMarginCrossDebt, AccountMarginCrossInterest,
	AccountMarginIsolated, AccountMarginIsolatedDebt, AccountMarginIsolatedInterest,
}

// MarginType reports whether t is a row of a user's margin account.
func MarginType(t string) bool { return slices.Contains(marginTypes, t) }

func isolatedType(t string) bool { return strings.HasPrefix(t, AccountMarginIsolated) }

// Debt reports whether the account is a margin debt or interest row,
// whose balance is what is owed, negative.
func (k AccountKey) Debt() bool {
	switch k.Type {
	case AccountMarginCrossDebt, AccountMarginCrossInterest, AccountMarginIsolatedDebt, AccountMarginIsolatedInterest:
		return true
	}
	return false
}

// MarginRef names a user's margin account: MARGIN_CROSS, or
// MARGIN_ISOLATED with its pair as scope.
type MarginRef struct {
	UserID      string
	AccountType string
	Scope       string
}

// ParseMarginRef checks a margin account as callers name it.
func ParseMarginRef(userID, accountType, scope string) (MarginRef, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return MarginRef{}, apperr.Invalid("user_id must be a UUID")
	}
	switch accountType {
	case AccountMarginCross:
		if scope != "" {
			return MarginRef{}, apperr.Invalid("the cross account takes no scope")
		}
	case AccountMarginIsolated:
		if base, quote, ok := strings.Cut(scope, "-"); !ok || base == "" || quote == "" {
			return MarginRef{}, apperr.Invalid("an isolated account's scope is its pair, e.g. BTC-USDT")
		}
	default:
		return MarginRef{}, apperr.Invalid(fmt.Sprintf("account_type must be %s or %s", AccountMarginCross, AccountMarginIsolated))
	}
	return MarginRef{UserID: userID, AccountType: accountType, Scope: scope}, nil
}

func (m MarginRef) row(suffix, asset string) AccountKey {
	return AccountKey{OwnerType: OwnerUser, OwnerID: m.UserID, Type: m.AccountType + suffix, Scope: m.Scope, Asset: asset}
}

// Assets is the account's asset row of asset.
func (m MarginRef) Assets(asset string) AccountKey { return m.row("", asset) }

// DebtRow is the account's principal row of asset.
func (m MarginRef) DebtRow(asset string) AccountKey { return m.row("_DEBT", asset) }

// InterestRow is the account's interest row of asset.
func (m MarginRef) InterestRow(asset string) AccountKey { return m.row("_INTEREST", asset) }

func (m MarginRef) String() string {
	if m.Scope == "" {
		return m.AccountType
	}
	return m.AccountType + ":" + m.Scope
}

// Margin moves, the steps of a margin posting (design §3.2):
//
//   - TRANSFER_IN: the user's SPOT to the assets (MARGIN_TRANSFER_IN);
//   - TRANSFER_OUT: the assets to SPOT (MARGIN_TRANSFER_OUT), never what
//     the asset's own debt and interest hold: the asset row stays at or
//     above what is owed of the asset;
//   - BORROW: assets +a, debt -a (MARGIN_BORROW);
//   - INTEREST: interest -a, HOUSE's MARGIN_INTEREST_INCOME +a
//     (MARGIN_INTEREST);
//   - REPAY: assets -a, interest +i, debt +(a - i) (MARGIN_REPAY), i the
//     part of a that pays interest; never more than is owed.
const (
	MarginTransferIn  = "TRANSFER_IN"
	MarginTransferOut = "TRANSFER_OUT"
	MarginBorrow      = "BORROW"
	MarginInterest    = "INTEREST"
	MarginRepay       = "REPAY"
)

var marginMoves = []string{MarginTransferIn, MarginTransferOut, MarginBorrow, MarginInterest, MarginRepay}

// MaxMarginMoves bounds a request.
const MaxMarginMoves = 16

// MarginMove is one step of a margin posting.
type MarginMove struct {
	Type   string
	Asset  string
	Amount decimal.Decimal
	// Interest is the part of a REPAY that pays interest.
	Interest decimal.Decimal
}

// MarginRequest is what margin-service asks the ledger to book for one of
// its operations: the moves in order, one journal each, all or none.
type MarginRequest struct {
	IdemKey   string
	Account   MarginRef
	Reference string
	Moves     []MarginMove
}

// Validate checks the request's shape; amounts against the assets'
// decimals are the caller's to check.
func (r MarginRequest) Validate() error {
	switch {
	case r.IdemKey == "" || len(r.IdemKey) > 150:
		return apperr.Invalid("an idempotency key of at most 150 bytes is required")
	case len(r.Reference) > 300:
		return apperr.Invalid("the reference is too long")
	case len(r.Moves) == 0 || len(r.Moves) > MaxMarginMoves:
		return apperr.Invalid(fmt.Sprintf("1 to %d moves are required", MaxMarginMoves))
	}
	if _, err := ParseMarginRef(r.Account.UserID, r.Account.AccountType, r.Account.Scope); err != nil {
		return err
	}
	for i, m := range r.Moves {
		fail := func(msg string) error { return apperr.Invalid(fmt.Sprintf("move %d (%s): %s", i+1, m.Type, msg)) }
		switch {
		case !slices.Contains(marginMoves, m.Type):
			return apperr.Invalid(fmt.Sprintf("move %d: unknown type %q", i+1, m.Type))
		case m.Asset == "":
			return fail("the asset is required")
		case !m.Amount.IsPositive():
			return fail("the amount must be positive")
		case m.Type != MarginRepay && !m.Interest.IsZero():
			return fail("only REPAY has an interest part")
		case m.Interest.IsNegative() || m.Interest.GreaterThan(m.Amount):
			return fail("the interest part is 0 to the amount")
		}
	}
	return nil
}

// Hash digests the request, telling a replay from a different request
// under the same key.
func (r MarginRequest) Hash() []byte {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%s\n", r.IdemKey, r.Account.UserID, r.Account, r.Reference)
	for _, m := range r.Moves {
		fmt.Fprintf(h, "%s|%s|%s|%s\n", m.Type, m.Asset, m.Amount.String(), m.Interest.String())
	}
	return h.Sum(nil)
}

// Assets returns the distinct assets of the moves.
func (r MarginRequest) Assets() []string {
	var out []string
	for _, m := range r.Moves {
		if !slices.Contains(out, m.Asset) {
			out = append(out, m.Asset)
		}
	}
	return out
}

// MarginAccounts returns the accounts the moves touch, and the debt rows a
// transfer out is checked against, in lock order.
func (r MarginRequest) MarginAccounts() []AccountKey {
	var p Posting
	add := func(keys ...AccountKey) {
		for _, k := range keys {
			p.Lines = append(p.Lines, Line{Account: k})
		}
	}
	for _, m := range r.Moves {
		a := r.Account
		switch m.Type {
		case MarginTransferIn:
			add(UserAccount(a.UserID, AccountSpot, m.Asset), a.Assets(m.Asset))
		case MarginTransferOut:
			add(UserAccount(a.UserID, AccountSpot, m.Asset), a.Assets(m.Asset), a.DebtRow(m.Asset), a.InterestRow(m.Asset))
		case MarginBorrow:
			add(a.Assets(m.Asset), a.DebtRow(m.Asset))
		case MarginInterest:
			add(a.InterestRow(m.Asset), SystemAccount(AccountMarginInterestIncome, m.Asset))
		case MarginRepay:
			add(a.Assets(m.Asset), a.DebtRow(m.Asset), a.InterestRow(m.Asset))
		}
	}
	return p.Accounts()
}

// MarginPostings turns the request into its journals against the
// accounts as they stand (locked by the caller, MarginAccounts), one per
// move, each move seeing the balances the ones before it left. A move
// that would take an asset row below zero (LEDGER_INSUFFICIENT_BALANCE)
// or a debt row above it (LEDGER_DEBT_OVERPAID), or a transfer out of
// what the asset's debt holds, refuses the whole request.
func MarginPostings(r MarginRequest, accounts []Account) ([]Posting, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	state := make(map[AccountKey]Account, len(accounts))
	for _, a := range accounts {
		state[a.Key] = a
	}
	out := make([]Posting, 0, len(r.Moves))
	for i, m := range r.Moves {
		assets, debt, interest := r.Account.Assets(m.Asset), r.Account.DebtRow(m.Asset), r.Account.InterestRow(m.Asset)
		var entry string
		var lines []Line
		switch m.Type {
		case MarginTransferIn:
			entry = EntryMarginTransferIn
			lines = []Line{
				{Account: UserAccount(r.Account.UserID, AccountSpot, m.Asset), Amount: m.Amount.Neg(), Kind: Available},
				{Account: assets, Amount: m.Amount, Kind: Available},
			}
		case MarginTransferOut:
			entry = EntryMarginTransferOut
			lines = []Line{
				{Account: assets, Amount: m.Amount.Neg(), Kind: Available},
				{Account: UserAccount(r.Account.UserID, AccountSpot, m.Asset), Amount: m.Amount, Kind: Available},
			}
		case MarginBorrow:
			entry = EntryMarginBorrow
			lines = []Line{{Account: assets, Amount: m.Amount, Kind: Available}, {Account: debt, Amount: m.Amount.Neg(), Kind: Available}}
		case MarginInterest:
			entry = EntryMarginInterest
			lines = []Line{
				{Account: interest, Amount: m.Amount.Neg(), Kind: Available},
				{Account: SystemAccount(AccountMarginInterestIncome, m.Asset), Amount: m.Amount, Kind: Available},
			}
		case MarginRepay:
			entry = EntryMarginRepay
			lines = []Line{{Account: assets, Amount: m.Amount.Neg(), Kind: Available}}
			if m.Interest.IsPositive() {
				lines = append(lines, Line{Account: interest, Amount: m.Interest, Kind: Available})
			}
			if principal := m.Amount.Sub(m.Interest); principal.IsPositive() {
				lines = append(lines, Line{Account: debt, Amount: principal, Kind: Available})
			}
		}
		for _, l := range lines {
			a, ok := state[l.Account]
			if !ok {
				return nil, fmt.Errorf("margin postings: account %s not loaded", l.Account)
			}
			if err := a.Apply(l); err != nil {
				return nil, apperr.From(err).WithDetail("move", i+1)
			}
			state[l.Account] = a
		}
		if m.Type == MarginTransferOut {
			held := state[assets].Available.Add(state[assets].Frozen)
			if owed := state[debt].Available.Add(state[interest].Available).Neg(); held.LessThan(owed) {
				return nil, ErrInsufficientBalance.WithDetail("asset", m.Asset).WithDetail("account_type", r.Account.AccountType).
					WithDetail("balance", "held by the debt").WithDetail("move", i+1)
			}
		}
		out = append(out, Posting{
			IdemKey: fmt.Sprintf("margin:%s:%d", r.IdemKey, i), EntryType: entry, Memo: fmt.Sprintf("%s %s", r.Reference, m.Type),
			Lines: lines,
		})
	}
	return out, nil
}

// MarginPosting is a booked request: a repeat with the same key and
// content gets its journals back.
type MarginPosting struct {
	IdemKey     string
	UserID      string
	RequestHash []byte
	Reference   string
	// Journals has one journal ID per move.
	Journals  []string
	CreatedAt time.Time
}

// InterestLine is one account's interest of an hour.
type InterestLine struct {
	Account MarginRef
	Amount  decimal.Decimal
}

// InterestRequest charges an hour's interest of one asset on many
// accounts (design §4.3): one MARGIN_INTEREST journal, each account's
// interest row -amount and HOUSE's MARGIN_INTEREST_INCOME the sum.
type InterestRequest struct {
	IdemKey   string
	Asset     string
	Reference string
	Lines     []InterestLine
}

// MaxInterestLines bounds the accounts of an interest journal.
const MaxInterestLines = 2000

// Posting is the request's journal (key margin-interest:<key>); the lines
// are in the request's order, so a repeat hashes the same.
func (r InterestRequest) Posting() (Posting, error) {
	switch {
	case r.IdemKey == "" || len(r.IdemKey) > 150:
		return Posting{}, apperr.Invalid("an idempotency key of at most 150 bytes is required")
	case r.Asset == "":
		return Posting{}, apperr.Invalid("the asset is required")
	case len(r.Lines) == 0 || len(r.Lines) > MaxInterestLines:
		return Posting{}, apperr.Invalid(fmt.Sprintf("1 to %d lines are required", MaxInterestLines))
	case len(r.Reference) > 300:
		return Posting{}, apperr.Invalid("the reference is too long")
	}
	p := Posting{IdemKey: "margin-interest:" + r.IdemKey, EntryType: EntryMarginInterest, Memo: r.Reference}
	total := decimal.Zero
	seen := map[AccountKey]bool{}
	for i, l := range r.Lines {
		if _, err := ParseMarginRef(l.Account.UserID, l.Account.AccountType, l.Account.Scope); err != nil {
			return Posting{}, apperr.Invalid(fmt.Sprintf("line %d: %s", i+1, apperr.From(err).Message))
		}
		if !l.Amount.IsPositive() {
			return Posting{}, apperr.Invalid(fmt.Sprintf("line %d: the amount must be positive", i+1))
		}
		k := l.Account.InterestRow(r.Asset)
		if seen[k] {
			return Posting{}, apperr.Invalid(fmt.Sprintf("line %d: %s twice", i+1, l.Account))
		}
		seen[k] = true
		p.Lines = append(p.Lines, Line{Account: k, Amount: l.Amount.Neg(), Kind: Available})
		total = total.Add(l.Amount)
	}
	p.Lines = append(p.Lines, Line{Account: SystemAccount(AccountMarginInterestIncome, r.Asset), Amount: total, Kind: Available})
	return p, nil
}

// freezable are the user accounts orders and withdrawals freeze funds on:
// SPOT, FUTURES and a margin account's asset row.
func freezable(accountType string) bool {
	switch accountType {
	case AccountSpot, AccountFutures, AccountMarginCross, AccountMarginIsolated:
		return true
	}
	return false
}

// FreezeScopedPosting is FreezePosting on any account orders freeze:
// SPOT, FUTURES, or the asset row of a margin account (MARGIN_CROSS, or
// MARGIN_ISOLATED with its pair as scope).
func FreezeScopedPosting(idemKey, entryType, userID, accountType, scope, asset string, amount decimal.Decimal, decimals int32, memo string) (Posting, error) {
	if entryType != EntryOrderFreeze && entryType != EntryWithdrawFreeze {
		return Posting{}, apperr.Invalid("a freeze is ORDER_FREEZE or WITHDRAW_FREEZE")
	}
	return moveScoped(idemKey, entryType, userID, accountType, scope, asset, amount, decimals, memo, Available, Frozen)
}

// UnfreezeScopedPosting is UnfreezePosting on the accounts
// FreezeScopedPosting freezes.
func UnfreezeScopedPosting(idemKey, entryType, userID, accountType, scope, asset string, amount decimal.Decimal, decimals int32, memo string) (Posting, error) {
	if entryType != EntryOrderUnfreeze && entryType != EntryWithdrawUnfreeze {
		return Posting{}, apperr.Invalid("an unfreeze is ORDER_UNFREEZE or WITHDRAW_UNFREEZE")
	}
	return moveScoped(idemKey, entryType, userID, accountType, scope, asset, amount, decimals, memo, Frozen, Available)
}

func moveScoped(idemKey, entryType, userID, accountType, scope, asset string, amount decimal.Decimal, decimals int32, memo, from, to string) (Posting, error) {
	if !freezable(accountType) {
		return Posting{}, apperr.Invalid(fmt.Sprintf("account type must be SPOT, FUTURES, %s or %s, got %q", AccountMarginCross,
			AccountMarginIsolated, accountType))
	}
	if err := checkAmount(amount, decimals); err != nil {
		return Posting{}, err
	}
	acc := AccountKey{OwnerType: OwnerUser, OwnerID: userID, Type: accountType, Scope: scope, Asset: asset}
	if err := acc.Validate(); err != nil {
		return Posting{}, err
	}
	return Posting{IdemKey: idemKey, EntryType: entryType, Memo: memo, Lines: []Line{
		{Account: acc, Amount: amount.Neg(), Kind: from},
		{Account: acc, Amount: amount, Kind: to},
	}}, nil
}
