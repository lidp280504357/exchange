package domain

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Transfer statuses (appendix B: REQUESTED -> COMPLETED | FAILED).
const (
	TransferRequested = "REQUESTED"
	TransferCompleted = "COMPLETED"
	TransferFailed    = "FAILED"
)

// Transfer moves funds between a user's SPOT and FUTURES accounts.
type Transfer struct {
	ID            string
	UserID        string
	IdemKey       string
	RequestHash   []byte
	Asset         string
	Amount        decimal.Decimal
	From          string
	To            string
	Status        string
	FailureReason string
	JournalID     string
	CreatedAt     time.Time
}

func checkAmount(amount decimal.Decimal, decimals int32) error {
	if !amount.IsPositive() {
		return apperr.Invalid("the amount must be positive")
	}
	if !amount.Equal(amount.Truncate(decimals)) {
		// Too many digits are rejected, never rounded (ADR-0008).
		return apperr.New(apperr.KindInvalid, "LEDGER_AMOUNT_PRECISION",
			fmt.Sprintf("the amount has more than %d decimals", decimals)).WithDetail("decimals", decimals)
	}
	return nil
}

func userAccountType(t string) error {
	if t != AccountSpot && t != AccountFutures {
		return apperr.Invalid(fmt.Sprintf("account type must be SPOT or FUTURES, got %q", t))
	}
	return nil
}

// FreezePosting moves amount from available to frozen on one account.
func FreezePosting(idemKey, entryType, userID, accountType, asset string, amount decimal.Decimal, decimals int32, memo string) (Posting, error) {
	if entryType != EntryOrderFreeze && entryType != EntryWithdrawFreeze {
		return Posting{}, apperr.Invalid("a freeze is ORDER_FREEZE or WITHDRAW_FREEZE")
	}
	return moveWithin(idemKey, entryType, userID, accountType, asset, amount, decimals, memo, Available, Frozen)
}

// UnfreezePosting moves amount from frozen back to available.
func UnfreezePosting(idemKey, entryType, userID, accountType, asset string, amount decimal.Decimal, decimals int32, memo string) (Posting, error) {
	if entryType != EntryOrderUnfreeze && entryType != EntryWithdrawUnfreeze {
		return Posting{}, apperr.Invalid("an unfreeze is ORDER_UNFREEZE or WITHDRAW_UNFREEZE")
	}
	return moveWithin(idemKey, entryType, userID, accountType, asset, amount, decimals, memo, Frozen, Available)
}

func moveWithin(idemKey, entryType, userID, accountType, asset string, amount decimal.Decimal, decimals int32, memo, from, to string) (Posting, error) {
	if err := checkAmount(amount, decimals); err != nil {
		return Posting{}, err
	}
	if err := userAccountType(accountType); err != nil {
		return Posting{}, err
	}
	acc := UserAccount(userID, accountType, asset)
	return Posting{IdemKey: idemKey, EntryType: entryType, Memo: memo, Lines: []Line{
		{Account: acc, Amount: amount.Neg(), Kind: from},
		{Account: acc, Amount: amount, Kind: to},
	}}, nil
}

// TransferPosting moves available funds between a user's accounts.
func TransferPosting(idemKey, userID, asset string, amount decimal.Decimal, decimals int32, from, to string) (Posting, error) {
	if err := checkAmount(amount, decimals); err != nil {
		return Posting{}, err
	}
	if err := userAccountType(from); err != nil {
		return Posting{}, err
	}
	if err := userAccountType(to); err != nil {
		return Posting{}, err
	}
	if from == to {
		return Posting{}, apperr.Invalid("transfer between two different accounts")
	}
	return Posting{IdemKey: idemKey, EntryType: EntryAccountTransfer, Lines: []Line{
		{Account: UserAccount(userID, from, asset), Amount: amount.Neg(), Kind: Available},
		{Account: UserAccount(userID, to, asset), Amount: amount, Kind: Available},
	}}, nil
}

// Credit is an amount of an asset.
type Credit struct {
	Asset    string
	Amount   decimal.Decimal
	Decimals int32
}

// AdjustmentPosting credits (or, with negative amounts, debits) a user's
// SPOT account against the ADJUSTMENT account: operator corrections and
// the simulated funds of phase 1 (§11.4).
func AdjustmentPosting(idemKey, userID string, credits []Credit, memo string) (Posting, error) {
	p := Posting{IdemKey: idemKey, EntryType: EntryManualAdjustment, Memo: memo}
	for _, c := range credits {
		if c.Amount.IsZero() || !c.Amount.Equal(c.Amount.Truncate(c.Decimals)) {
			return Posting{}, apperr.Invalid(fmt.Sprintf("invalid %s amount %s", c.Asset, c.Amount))
		}
		p.Lines = append(p.Lines,
			Line{Account: UserAccount(userID, AccountSpot, c.Asset), Amount: c.Amount, Kind: Available},
			Line{Account: SystemAccount(AccountAdjustment, c.Asset), Amount: c.Amount.Neg(), Kind: Available},
		)
	}
	return p, nil
}
