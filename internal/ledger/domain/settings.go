package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

// AssetAmount is an amount of an asset, as an operator or the environment
// sets it.
type AssetAmount struct {
	Asset  string
	Amount decimal.Decimal
}

// WelcomeCredits is what a new account gets while the flag
// ledger.welcome_credit is on (design 2026-10-04 §4.2): the operators'
// setting, an empty list granting nothing. Version goes up with every
// change.
type WelcomeCredits struct {
	Credits   []AssetAmount
	Version   int64
	UpdatedBy string
	UpdatedAt time.Time
}

// MaxWelcomeCredits bounds the assets of the welcome credits.
const MaxWelcomeCredits = 10

// WelcomeFromEnv is the actor of the first value, taken from WELCOME_FUNDS.
const WelcomeFromEnv = "system:WELCOME_FUNDS"

// ErrSettingsChanged refuses a change made on a version that is no longer
// the current one: someone saved in between.
var ErrSettingsChanged = apperr.New(apperr.KindConflict, "LEDGER_SETTINGS_CHANGED", "the setting changed since it was read")

var assetCodeRE = regexp.MustCompile(`^[A-Z0-9]{2,12}$`)

// ValidWelcomeCredits checks a list of welcome credits: at most ten
// assets, each once, with an amount above zero.
func ValidWelcomeCredits(list []AssetAmount) error {
	if len(list) > MaxWelcomeCredits {
		return apperr.Invalid(fmt.Sprintf("credits: at most %d assets", MaxWelcomeCredits))
	}
	seen := map[string]bool{}
	for _, c := range list {
		switch {
		case !assetCodeRE.MatchString(c.Asset):
			return apperr.Invalid(fmt.Sprintf("credits: %q is not an asset code", c.Asset))
		case seen[c.Asset]:
			return apperr.Invalid(fmt.Sprintf("credits: %s twice", c.Asset))
		case !c.Amount.IsPositive():
			return apperr.Invalid(fmt.Sprintf("credits: the amount of %s must be above 0 (leave the asset out to give none)", c.Asset))
		}
		seen[c.Asset] = true
	}
	return nil
}

// ValidSettingChange checks who changes a setting and why: an actor, and a
// reason of 3-200 characters.
func ValidSettingChange(actor, reason string) error {
	if strings.TrimSpace(actor) == "" {
		return apperr.Invalid("an actor is required")
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(reason)); n < 3 || n > 200 {
		return apperr.Invalid("a reason of 3-200 characters is required")
	}
	return nil
}
