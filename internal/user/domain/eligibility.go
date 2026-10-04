package domain

import (
	"fmt"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
)

// Features a user may be eligible for (§5.4).
const (
	FeatureSpotTrade        = "SPOT_TRADE"
	FeatureDerivativesTrade = "DERIVATIVES_TRADE"
	FeatureDeposit          = "DEPOSIT"
	FeatureWithdraw         = "WITHDRAW"
	FeatureTransfer         = "TRANSFER"
	// FeatureTestAssets opens the hidden test assets' deposits and
	// withdrawals (ADR-0017): the end-to-end tests' accounts only.
	FeatureTestAssets = "TEST_ASSETS"
)

// Reason codes of a refusal (appendix C).
const (
	ReasonRiskReview  = "USER_RISK_REVIEW"
	ReasonFrozen      = "USER_FROZEN"
	ReasonClosed      = "USER_CLOSED"
	ReasonRegion      = "USER_REGION_NOT_ALLOWED"
	ReasonNotEligible = "USER_NOT_ELIGIBLE"
)

// featureFlags gates the high-risk features behind their switches
// (ADR-0005); spot trading and deposits have none.
var featureFlags = map[string]string{
	FeatureDerivativesTrade: flags.KeyDerivatives,
	FeatureWithdraw:         flags.KeyWithdraw,
	FeatureTransfer:         flags.KeyTransfer,
	FeatureTestAssets:       flags.KeyTestAssets,
}

// statusAllows is the impact matrix of §5.4 for the features that have an
// eligibility check. Deposits of a FROZEN account are credited but cannot
// move; closing positions and canceling orders are not gated here.
var statusAllows = map[string]map[string]bool{
	StatusActive: {
		FeatureSpotTrade: true, FeatureDerivativesTrade: true, FeatureDeposit: true, FeatureWithdraw: true, FeatureTransfer: true,
		FeatureTestAssets: true,
	},
	StatusRiskReview: {FeatureSpotTrade: true, FeatureDeposit: true},
	StatusFrozen:     {FeatureDeposit: true},
	StatusClosed:     {},
}

var statusReasons = map[string]string{
	StatusRiskReview: ReasonRiskReview,
	StatusFrozen:     ReasonFrozen,
	StatusClosed:     ReasonClosed,
}

// ParseFeature accepts one of the feature names.
func ParseFeature(s string) (string, error) {
	switch s {
	case FeatureSpotTrade, FeatureDerivativesTrade, FeatureDeposit, FeatureWithdraw, FeatureTransfer, FeatureTestAssets:
		return s, nil
	}
	return "", apperr.Invalid(fmt.Sprintf("unknown feature %q", s))
}

// FlagLookup returns a feature flag; a missing flag counts as off.
type FlagLookup func(key string) (flags.Flag, bool)

// Eligibility decides whether u may use feature now: first the account
// status, then the feature's switch with its region and other rules.
// KYC levels will join here (§12.5).
func Eligibility(u User, feature, asset, symbol string, lookup FlagLookup) (allowed bool, reason string) {
	allowedByStatus, known := statusAllows[u.Status]
	if !known {
		return false, ReasonNotEligible
	}
	if !allowedByStatus[feature] {
		if r, ok := statusReasons[u.Status]; ok {
			return false, r
		}
		return false, ReasonNotEligible
	}
	key, gated := featureFlags[feature]
	if !gated {
		return true, ""
	}
	f, ok := lookup(key)
	if !ok {
		return false, ReasonNotEligible
	}
	switch f.Denial(flags.Subject{UserID: u.ID, Region: u.Region, Status: u.Status, Asset: asset, Symbol: symbol}) {
	case "":
		return true, ""
	case "region":
		return false, ReasonRegion
	default:
		return false, ReasonNotEligible
	}
}
