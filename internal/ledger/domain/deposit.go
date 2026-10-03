package domain

import (
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Deposit is a confirmed on-chain deposit the wallet asks the ledger to
// book (§11.5).
type Deposit struct {
	ID      string
	UserID  string
	Asset   string
	Amount  decimal.Decimal
	Network string
	TxHash  string
	// Unclaimed deposits (below the minimum, closed account) go to
	// UNCLAIMED_DEPOSIT instead of the user.
	Unclaimed bool
	Reason    string
}

// DepositKey is the idempotency key of a deposit's credit.
func DepositKey(depositID string) string { return "deposit:" + depositID }

// ReleaseKey is the idempotency key of an unclaimed deposit's release.
func ReleaseKey(depositID string) string { return "deposit-release:" + depositID }

// ReleasePostingOf moves an unclaimed deposit from UNCLAIMED_DEPOSIT to
// its user's SPOT account once an administrator decided it is theirs
// (design 2026-10-02 §4.3): the same asset and amount, DEPOSIT_CREDIT.
func ReleasePostingOf(d Deposit, memo string) (Posting, error) {
	if d.ID == "" || d.Asset == "" || d.UserID == "" || !d.Amount.IsPositive() {
		return Posting{}, apperr.Invalid("a release needs a deposit ID, an asset, a user and a positive amount")
	}
	return Posting{
		IdemKey: ReleaseKey(d.ID), EntryType: EntryDepositCredit, Memo: memo,
		Lines: []Line{
			{Account: SystemAccount(AccountUnclaimedDeposit, d.Asset), Amount: d.Amount.Neg(), Kind: Available},
			{Account: UserAccount(d.UserID, AccountSpot, d.Asset), Amount: d.Amount, Kind: Available},
		},
	}, nil
}

// DepositPosting is the DEPOSIT_CREDIT journal of a deposit:
// DEPOSIT_PENDING pays the user's SPOT account, or UNCLAIMED_DEPOSIT
// (§11.4 sign convention: DEPOSIT_PENDING accumulates the negative of all
// on-chain inflows).
func DepositPosting(d Deposit) (Posting, error) {
	if d.ID == "" || d.Asset == "" || (d.UserID == "" && !d.Unclaimed) || !d.Amount.IsPositive() {
		return Posting{}, apperr.Invalid("a deposit needs an ID, an asset, a user and a positive amount")
	}
	to := UserAccount(d.UserID, AccountSpot, d.Asset)
	memo := fmt.Sprintf("deposit %s %s", d.Network, d.TxHash)
	switch {
	case d.Unclaimed && d.UserID == "":
		to = SystemAccount(AccountUnclaimedDeposit, d.Asset)
		memo = fmt.Sprintf("unclaimed deposit (%s) to an address no user has: %s %s", d.Reason, d.Network, d.TxHash)
	case d.Unclaimed:
		to = SystemAccount(AccountUnclaimedDeposit, d.Asset)
		memo = fmt.Sprintf("unclaimed deposit (%s) of user %s: %s %s", d.Reason, d.UserID, d.Network, d.TxHash)
	}
	return Posting{
		IdemKey: DepositKey(d.ID), EntryType: EntryDepositCredit, Memo: memo,
		Lines: []Line{
			{Account: SystemAccount(AccountDepositPending, d.Asset), Amount: d.Amount.Neg(), Kind: Available},
			{Account: to, Amount: d.Amount, Kind: Available},
		},
	}, nil
}
