package domain

import (
	"fmt"
	"slices"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Journals of wallet-service (§11.4, §11.6). DEPOSIT_PENDING accumulates
// the negative of on-chain inflows and WITHDRAWAL_PENDING the on-chain
// outflows, so −(DEPOSIT_PENDING + WITHDRAWAL_PENDING) is what the ledger
// expects the platform wallets to hold (invariant 4).

// fundable are the system accounts the platform may fund from its own
// on-chain transfers.
var fundable = []string{AccountGasSupply, AccountInsuranceFund, AccountMarketMaker}

// WithdrawSettlePosting books a broadcast withdrawal: amount + fee leave
// the user's frozen SPOT balance, the amount to WITHDRAWAL_PENDING and the
// fee to FEE_REVENUE.
func WithdrawSettlePosting(idemKey, userID, asset string, amount, fee decimal.Decimal, decimals int32, reference string) (Posting, error) {
	if err := checkAmount(amount, decimals); err != nil {
		return Posting{}, err
	}
	if fee.IsNegative() || !fee.Equal(fee.Truncate(decimals)) {
		return Posting{}, apperr.Invalid("the fee must be zero or positive within the asset's decimals")
	}
	lines := []Line{
		{Account: UserAccount(userID, AccountSpot, asset), Amount: amount.Add(fee).Neg(), Kind: Frozen},
		{Account: SystemAccount(AccountWithdrawalPending, asset), Amount: amount, Kind: Available},
	}
	if fee.IsPositive() {
		lines = append(lines, Line{Account: SystemAccount(AccountFeeRevenue, asset), Amount: fee, Kind: Available})
	}
	return Posting{IdemKey: idemKey, EntryType: EntryWithdrawSettle, Memo: "withdrawal " + reference, Lines: lines}, nil
}

// InternalTransferPosting completes a withdrawal to another user's deposit
// address in the ledger: the payer's frozen amount becomes the payee's
// available balance.
func InternalTransferPosting(idemKey, fromUser, toUser, asset string, amount decimal.Decimal, decimals int32, reference string) (Posting, error) {
	if err := checkAmount(amount, decimals); err != nil {
		return Posting{}, err
	}
	if fromUser == toUser {
		return Posting{}, apperr.Invalid("an internal transfer goes to another user")
	}
	return Posting{IdemKey: idemKey, EntryType: EntryInternalTransfer, Memo: "internal withdrawal " + reference, Lines: []Line{
		{Account: UserAccount(fromUser, AccountSpot, asset), Amount: amount.Neg(), Kind: Frozen},
		{Account: UserAccount(toUser, AccountSpot, asset), Amount: amount, Kind: Available},
	}}, nil
}

// ChainFeePosting books gas the platform paid: from GAS_SUPPLY to
// WITHDRAWAL_PENDING, as it is a real on-chain outflow (§5.10).
func ChainFeePosting(idemKey, asset string, amount decimal.Decimal, decimals int32, reference string) (Posting, error) {
	if err := checkAmount(amount, decimals); err != nil {
		return Posting{}, err
	}
	return Posting{IdemKey: idemKey, EntryType: EntryWithdrawSettle, Memo: "chain fee " + reference, Lines: []Line{
		{Account: SystemAccount(AccountGasSupply, asset), Amount: amount.Neg(), Kind: Available},
		{Account: SystemAccount(AccountWithdrawalPending, asset), Amount: amount, Kind: Available},
	}}, nil
}

// GasSupplyPosting sets fee revenue aside for the fees the platform pays
// (FEE_REVENUE to GAS_SUPPLY): with a custodian, what users paid in fees
// is held where its fees are taken from (ADR-0011), so they are paid out
// of it. Neither account is what a wallet should hold, so invariant 4 is
// unchanged.
func GasSupplyPosting(idemKey, asset string, amount decimal.Decimal, decimals int32, reason string) (Posting, error) {
	if err := checkAmount(amount, decimals); err != nil {
		return Posting{}, err
	}
	return Posting{IdemKey: idemKey, EntryType: EntryManualAdjustment, Memo: reason, Lines: []Line{
		{Account: SystemAccount(AccountFeeRevenue, asset), Amount: amount.Neg(), Kind: Available},
		{Account: SystemAccount(AccountGasSupply, asset), Amount: amount, Kind: Available},
	}}, nil
}

// FundingPosting books the platform's own transfer into its hot wallet as
// a deposit to a system account (§11.4: DEPOSIT_CREDIT from
// DEPOSIT_PENDING).
func FundingPosting(idemKey, accountType, asset string, amount decimal.Decimal, decimals int32, reference string) (Posting, error) {
	if err := checkAmount(amount, decimals); err != nil {
		return Posting{}, err
	}
	if !slices.Contains(fundable, accountType) {
		return Posting{}, apperr.Invalid(fmt.Sprintf("only %v can be funded", fundable))
	}
	return Posting{IdemKey: idemKey, EntryType: EntryDepositCredit, Memo: "platform funding " + reference, Lines: []Line{
		{Account: SystemAccount(AccountDepositPending, asset), Amount: amount.Neg(), Kind: Available},
		{Account: SystemAccount(accountType, asset), Amount: amount, Kind: Available},
	}}, nil
}
