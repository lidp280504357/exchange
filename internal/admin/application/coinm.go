package application

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/flags"
)

// Coin-margined perpetuals in the console (design 2026-10-06 §2.7, batch
// G5): contracts of both margin types, the insurance fund of every
// settlement asset, and the launch checklist's two items.

// listedContract is what the console needs of a contract from the
// instruments listing (both margin types).
type listedContract struct {
	Symbol      string `json:"symbol"`
	Status      string `json:"status"`
	BaseAsset   string `json:"base_asset"`
	MarginType  string `json:"margin_type"`
	SettleAsset string `json:"settle_asset"`
}

// marginType is a contract's margin type: its own, else (a listing from
// before G0) a linear contract's, USDT.
func (c listedContract) marginType() string {
	if c.MarginType != "" {
		return c.MarginType
	}
	return "USDT"
}

// settlement is a contract's settlement asset: its own, else (a listing
// from before G0) the quote asset of a linear contract, USDT.
func (c listedContract) settlement() string {
	if c.SettleAsset != "" {
		return c.SettleAsset
	}
	return "USDT"
}

// listedContracts reads the contracts of the instruments listing.
func (s *Service) listedContracts(ctx context.Context) ([]listedContract, error) {
	if s.Catalog == nil {
		return nil, errors.New("no instrument catalog")
	}
	raw, err := s.Catalog.List(ctx)
	if err != nil {
		return nil, err
	}
	var cat struct {
		Contracts []listedContract `json:"contracts"`
	}
	if err := json.Unmarshal(raw, &cat); err != nil {
		return nil, err
	}
	return cat.Contracts, nil
}

// settlements maps every listed contract to its settlement asset, for the
// read models' rows that do not carry it (liquidation steps, the reports;
// review ER ①); nil while the listing cannot be read, the rows then left
// without one (the console reads them as USDT).
func (s *Service) settlements(ctx context.Context) map[string]string {
	contracts, err := s.listedContracts(ctx)
	if err != nil {
		s.Log.WarnContext(ctx, "the contracts' settlement assets are unknown", "error", err)
		return nil
	}
	out := make(map[string]string, len(contracts))
	for _, c := range contracts {
		out[c.Symbol] = c.settlement()
	}
	return out
}

// InsuranceFunds returns the insurance fund of every asset a contract
// settles in, and of any other asset the fund holds: USDT first, then by
// asset.
func (s *Service) InsuranceFunds(ctx context.Context, p Principal) ([]InsuranceFund, error) {
	if err := p.require(domain.PermDerivativesRead); err != nil {
		return nil, err
	}
	contracts, err := s.listedContracts(ctx)
	if err != nil {
		return nil, err
	}
	funds := map[string]*InsuranceFund{"USDT": {Asset: "USDT", Balance: "0", PnLClearing: "0"}}
	fund := func(asset string) *InsuranceFund {
		if funds[asset] == nil {
			funds[asset] = &InsuranceFund{Asset: asset, Balance: "0", PnLClearing: "0"}
		}
		return funds[asset]
	}
	for _, c := range contracts {
		if c.Status != "DELISTED" {
			fund(c.settlement())
		}
	}
	list, err := s.Ledger.SystemBalances(ctx, "")
	if err != nil {
		return nil, err
	}
	// The funds first, then their clearing rows: the ledger lists accounts
	// in no promised order (review ER).
	for _, b := range list {
		if b.AccountType == accountInsuranceFund {
			fund(b.Asset).Balance = b.Available
		}
	}
	for _, b := range list {
		if f := funds[b.Asset]; f != nil && b.AccountType == accountPnLClearing {
			f.PnLClearing = b.Available
		}
	}
	out := make([]InsuranceFund, 0, len(funds))
	for _, f := range funds {
		out = append(out, *f)
	}
	slices.SortFunc(out, func(a, b InsuranceFund) int {
		switch {
		case a.Asset == b.Asset:
			return 0
		case a.Asset == "USDT":
			return -1
		case b.Asset == "USDT":
			return 1
		}
		return strings.Compare(a.Asset, b.Asset)
	})
	return out, nil
}

// launchInsurance: the insurance fund of the settlement asset of every
// contract in trading holds something (design 2026-10-06 §2.7, coordinator
// 20:45): a liquidation that loses more than its margin is paid from it.
// Its value also counts the contracts open by margin type (§3.5).
func (s *Service) launchInsurance(ctx context.Context) (string, map[string]any) {
	if s.Catalog == nil || s.Ledger == nil {
		return LaunchUnknown, map[string]any{}
	}
	contracts, err := s.listedContracts(ctx)
	if err != nil {
		s.Log.WarnContext(ctx, "launch checklist: the contracts are unknown", "error", err)
		return LaunchUnknown, map[string]any{}
	}
	value := map[string]any{}
	open := map[string][]string{}
	byType := map[string]int{"USDT": 0, "COIN": 0}
	for _, c := range contracts {
		if c.Status == "TRADING" {
			open[c.settlement()] = append(open[c.settlement()], c.Symbol)
			if c.MarginType == "COIN" {
				byType["COIN"]++
			} else {
				byType["USDT"]++
			}
		}
	}
	value["open"] = byType
	list, err := s.Ledger.SystemBalances(ctx, "")
	if err != nil {
		s.Log.WarnContext(ctx, "launch checklist: the insurance fund is unknown", "error", err)
		return LaunchUnknown, value
	}
	balances := map[string]string{}
	for asset := range open {
		balances[asset] = "0"
	}
	for _, b := range list {
		if _, ok := open[b.Asset]; ok && b.AccountType == accountInsuranceFund {
			balances[b.Asset] = b.Available
		}
	}
	short := []string{}
	for asset, v := range balances {
		if b, err := decimal.NewFromString(v); err != nil || !b.IsPositive() {
			short = append(short, asset)
		}
	}
	slices.Sort(short)
	for _, symbols := range open {
		slices.Sort(symbols)
	}
	value["balances"], value["contracts"], value["short"] = balances, open, short
	if len(short) > 0 {
		return LaunchFail, value
	}
	return LaunchOK, value
}

// launchCoinM: the coin-margined contracts are off, or open by rules (some
// users, regions or statuses), never to everyone at once (coordinator
// 20:45, as margin trading's switches).
func launchCoinM(flagged map[string]ports.Flag) (string, map[string]any) {
	f := flagged[flags.KeyCoinM]
	value := map[string]any{"flag": flags.KeyCoinM, "enabled": f.Enabled}
	if hasRules(f) {
		value["rules"] = f.Rules
	}
	if f.Enabled && !hasRules(f) {
		return LaunchFail, value
	}
	return LaunchOK, value
}
