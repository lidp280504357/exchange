package domain

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

// Account names one of a user's margin accounts: the cross account, or
// the isolated account of a pair.
type Account struct {
	Type AccountType
	// Symbol is an isolated account's pair; empty for the cross account.
	Symbol string
}

// Cross is the cross account.
func Cross() Account { return Account{Type: AccountCross} }

// Isolated is the isolated account of symbol.
func Isolated(symbol string) Account { return Account{Type: AccountIsolated, Symbol: symbol} }

// ParseAccount checks an account as requests name it: MARGIN_CROSS
// without a symbol, MARGIN_ISOLATED with its pair.
func ParseAccount(accountType, symbol string) (Account, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	switch AccountType(accountType) {
	case AccountCross:
		if symbol != "" {
			return Account{}, apperr.Invalid("the cross account takes no symbol")
		}
		return Cross(), nil
	case AccountIsolated:
		if !validSymbol(symbol) {
			return Account{}, apperr.Invalid("an isolated account needs its pair's symbol, e.g. BTC-USDT")
		}
		return Isolated(symbol), nil
	}
	return Account{}, apperr.Invalid(fmt.Sprintf("account must be %s or %s", AccountCross, AccountIsolated))
}

func validSymbol(s string) bool {
	base, quote, ok := strings.Cut(s, "-")
	return ok && base != "" && quote != "" && !strings.Contains(quote, "-")
}

// IsCross reports whether a is the cross account.
func (a Account) IsCross() bool { return a.Type == AccountCross }

// Key is the account's name in the ledger and in idempotency keys:
// MARGIN_CROSS, or MARGIN_ISOLATED:<symbol> (design §2).
func (a Account) Key() string {
	if a.IsCross() {
		return string(AccountCross)
	}
	return string(AccountIsolated) + ":" + a.Symbol
}

func (a Account) String() string { return a.Key() }

// Holding is what a margin account holds and owes of one asset.
type Holding struct {
	Asset string
	// Free is the available balance, Locked what open orders hold.
	Free   decimal.Decimal
	Locked decimal.Decimal
	// Borrowed is the principal owed, Interest the interest owed.
	Borrowed decimal.Decimal
	Interest decimal.Decimal
}

// Total is what the account holds of the asset.
func (h Holding) Total() decimal.Decimal { return h.Free.Add(h.Locked) }

// Debt is what the account owes of the asset.
func (h Holding) Debt() decimal.Decimal { return h.Borrowed.Add(h.Interest) }

// Net is what the account holds less what it owes of the asset.
func (h Holding) Net() decimal.Decimal { return h.Total().Sub(h.Debt()) }

// Empty reports whether the account neither holds nor owes the asset.
func (h Holding) Empty() bool { return h.Total().IsZero() && h.Debt().IsZero() }

// MarginRow reports whether a ledger account type is one of a margin
// account's rows: its assets, its debts or its interest.
func MarginRow(accountType string) bool {
	switch strings.TrimSuffix(strings.TrimSuffix(accountType, "_DEBT"), "_INTEREST") {
	case string(AccountCross), string(AccountIsolated):
		return true
	}
	return false
}
