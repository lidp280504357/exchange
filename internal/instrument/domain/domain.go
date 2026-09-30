// Package domain holds instrument-service's reference data: assets and
// their networks, trading pairs and fee schedules (requirements §5.5), and
// the rules they must satisfy. Amounts are decimals (ADR-0008).
package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Errors (appendix C).
var (
	ErrNotFound         = apperr.NotFound("no such instrument")
	ErrStatusTransition = apperr.New(apperr.KindConflict, "INSTRUMENT_STATUS_TRANSITION_INVALID", "this trading pair status change is not allowed")
)

var (
	assetCodeRE = regexp.MustCompile(`^[A-Z0-9]{2,10}$`)
	networkRE   = regexp.MustCompile(`^[A-Z0-9_-]{2,24}$`)
	tierRE      = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)
	reasonRE    = regexp.MustCompile(`^.{3,200}$`)
	categoryRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,23}$`)
)

// maxCategories bounds an asset's sector tags.
const maxCategories = 8

// FeeSchedule is a maker/taker fee tier (§11.3).
type FeeSchedule struct {
	Tier         string          `json:"tier"`
	MakerFeeRate decimal.Decimal `json:"maker_fee_rate"`
	TakerFeeRate decimal.Decimal `json:"taker_fee_rate"`
	Version      int64           `json:"version,omitempty"`
}

// Validate checks the rates: non-negative and below 10%.
func (f FeeSchedule) Validate() error {
	limit := decimal.NewFromFloat(0.1)
	switch {
	case !tierRE.MatchString(f.Tier):
		return apperr.Invalid(fmt.Sprintf("fee tier %q: use 1-32 lower-case letters, digits, - or _", f.Tier))
	case f.MakerFeeRate.IsNegative() || f.MakerFeeRate.GreaterThanOrEqual(limit),
		f.TakerFeeRate.IsNegative() || f.TakerFeeRate.GreaterThanOrEqual(limit):
		return apperr.Invalid(fmt.Sprintf("fee tier %s: rates must be at least 0 and below 0.1", f.Tier))
	}
	return nil
}

// Same reports whether the configuration equals other, ignoring versions.
func (f FeeSchedule) Same(other FeeSchedule) bool {
	return f.Tier == other.Tier && f.MakerFeeRate.Equal(other.MakerFeeRate) && f.TakerFeeRate.Equal(other.TakerFeeRate)
}

// Asset is a currency the exchange knows.
type Asset struct {
	Code            string `json:"asset_code"`
	Name            string `json:"name"`
	Decimals        int32  `json:"decimals"`
	DepositEnabled  bool   `json:"deposit_enabled"`
	WithdrawEnabled bool   `json:"withdraw_enabled"`
	TradingEnabled  bool   `json:"trading_enabled"`
	RiskRestricted  bool   `json:"risk_restricted"`
	// Rank is the market-cap rank at listing, 0 when unranked; Categories
	// are sector tags. Both only sort and filter the market lists.
	Rank       int32    `json:"rank,omitempty"`
	Categories []string `json:"categories,omitempty"`
	Version    int64    `json:"version,omitempty"`
}

// Validate checks the asset on its own.
func (a Asset) Validate() error {
	switch {
	case !assetCodeRE.MatchString(a.Code):
		return apperr.Invalid(fmt.Sprintf("asset code %q: use 2-10 upper-case letters or digits", a.Code))
	case strings.TrimSpace(a.Name) == "" || len(a.Name) > 64:
		return apperr.Invalid(fmt.Sprintf("asset %s: a name of at most 64 bytes is required", a.Code))
	case a.Decimals < 0 || a.Decimals > 18:
		return apperr.Invalid(fmt.Sprintf("asset %s: decimals must be 0 to 18", a.Code))
	case a.Rank < 0 || a.Rank > 100000:
		return apperr.Invalid(fmt.Sprintf("asset %s: rank must be 0 (unranked) to 100000", a.Code))
	case len(a.Categories) > maxCategories:
		return apperr.Invalid(fmt.Sprintf("asset %s: at most %d categories", a.Code, maxCategories))
	}
	for i, c := range a.Categories {
		if !categoryRE.MatchString(c) || slices.Contains(a.Categories[:i], c) {
			return apperr.Invalid(fmt.Sprintf("asset %s: category %q: use distinct 2-24 lower-case letters, digits or -", a.Code, c))
		}
	}
	return nil
}

// Same reports whether the configuration equals other, ignoring versions.
func (a Asset) Same(other Asset) bool {
	return a.Code == other.Code && a.Name == other.Name && a.Decimals == other.Decimals &&
		a.DepositEnabled == other.DepositEnabled && a.WithdrawEnabled == other.WithdrawEnabled &&
		a.TradingEnabled == other.TradingEnabled && a.RiskRestricted == other.RiskRestricted &&
		a.Rank == other.Rank && slices.Equal(a.Categories, other.Categories)
}

// Address formats of networks.
const (
	FormatEVM  = "EVM"  // 0x and 40 hex digits, EIP-55 checksum when mixed case
	FormatTRON = "TRON" // Base58Check, 34 characters starting with T
	FormatBTC  = "BTC"  // bech32/bech32m or Base58Check
)

// Network is an asset on one chain (§5.5).
type Network struct {
	AssetCode       string          `json:"asset_code"`
	Network         string          `json:"network"`
	Chain           string          `json:"chain"`
	ContractAddress string          `json:"contract_address"`
	Confirmations   int32           `json:"confirmations"`
	MinDeposit      decimal.Decimal `json:"min_deposit"`
	MinWithdraw     decimal.Decimal `json:"min_withdraw"`
	WithdrawFee     decimal.Decimal `json:"withdraw_fee"`
	MemoRequired    bool            `json:"memo_required"`
	DepositEnabled  bool            `json:"deposit_enabled"`
	WithdrawEnabled bool            `json:"withdraw_enabled"`
	// DisplayName is what users see (TRC20, BEP20, ERC20, Bitcoin);
	// AddressFormat tells how addresses are checked (FormatEVM when empty).
	DisplayName   string `json:"display_name,omitempty"`
	AddressFormat string `json:"address_format,omitempty"`
	// ETAMinutes is the usual time from the transfer to the credit.
	ETAMinutes int32 `json:"eta_minutes,omitempty"`
	// Explorer links, https with a {tx} or {address} placeholder.
	ExplorerTxURL      string `json:"explorer_tx_url,omitempty"`
	ExplorerAddressURL string `json:"explorer_address_url,omitempty"`
	Version            int64  `json:"version,omitempty"`
}

// Validate checks the network against its asset's precision.
func (n Network) Validate(asset Asset) error {
	name := n.AssetCode + "/" + n.Network
	switch {
	case !networkRE.MatchString(n.Network):
		return apperr.Invalid(fmt.Sprintf("network %q: use 2-24 upper-case letters, digits, - or _", n.Network))
	case strings.TrimSpace(n.Chain) == "":
		return apperr.Invalid(fmt.Sprintf("network %s: the chain is required", name))
	case n.Confirmations < 0 || n.Confirmations > 1000:
		return apperr.Invalid(fmt.Sprintf("network %s: confirmations must be 0 to 1000", name))
	case n.AddressFormat != "" && n.AddressFormat != FormatEVM && n.AddressFormat != FormatTRON && n.AddressFormat != FormatBTC:
		return apperr.Invalid(fmt.Sprintf("network %s: address_format must be EVM, TRON or BTC", name))
	case len(n.DisplayName) > 32:
		return apperr.Invalid(fmt.Sprintf("network %s: display_name is at most 32 bytes", name))
	case n.ETAMinutes < 0 || n.ETAMinutes > 10080:
		return apperr.Invalid(fmt.Sprintf("network %s: eta_minutes must be 0 to 10080", name))
	case !explorerLink(n.ExplorerTxURL, "{tx}"):
		return apperr.Invalid(fmt.Sprintf("network %s: explorer_tx_url must be an https URL with {tx}", name))
	case !explorerLink(n.ExplorerAddressURL, "{address}"):
		return apperr.Invalid(fmt.Sprintf("network %s: explorer_address_url must be an https URL with {address}", name))
	}
	for field, v := range map[string]decimal.Decimal{"min_deposit": n.MinDeposit, "min_withdraw": n.MinWithdraw, "withdraw_fee": n.WithdrawFee} {
		if v.IsNegative() {
			return apperr.Invalid(fmt.Sprintf("network %s: %s must not be negative", name, field))
		}
		if !FitsScale(v, asset.Decimals) {
			return apperr.Invalid(fmt.Sprintf("network %s: %s has more than %d decimals", name, field, asset.Decimals))
		}
	}
	return nil
}

// Same reports whether the configuration equals other, ignoring versions.
func (n Network) Same(other Network) bool {
	return n.AssetCode == other.AssetCode && n.Network == other.Network && n.Chain == other.Chain &&
		n.ContractAddress == other.ContractAddress && n.Confirmations == other.Confirmations &&
		n.MinDeposit.Equal(other.MinDeposit) && n.MinWithdraw.Equal(other.MinWithdraw) && n.WithdrawFee.Equal(other.WithdrawFee) &&
		n.MemoRequired == other.MemoRequired && n.DepositEnabled == other.DepositEnabled && n.WithdrawEnabled == other.WithdrawEnabled &&
		n.DisplayName == other.DisplayName && n.AddressFormat == other.AddressFormat && n.ETAMinutes == other.ETAMinutes &&
		n.ExplorerTxURL == other.ExplorerTxURL && n.ExplorerAddressURL == other.ExplorerAddressURL
}

// explorerLink accepts an empty link or an https URL with placeholder.
func explorerLink(link, placeholder string) bool {
	return link == "" || (strings.HasPrefix(link, "https://") && strings.Contains(link, placeholder) && len(link) <= 256)
}

// FitsScale reports whether d has at most scale decimal places. Amounts
// with more digits are rejected, never rounded (ADR-0008).
func FitsScale(d decimal.Decimal, scale int32) bool {
	return d.Equal(d.Truncate(scale))
}

// IsMultipleOf reports whether d is a whole multiple of step.
func IsMultipleOf(d, step decimal.Decimal) bool {
	return !step.IsZero() && d.Mod(step).IsZero()
}
