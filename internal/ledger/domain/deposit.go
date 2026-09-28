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
	if d.Unclaimed {
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
