package application

import (
	"google.golang.org/protobuf/types/known/timestamppb"

	instrumentv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/instrument/v1"
	"github.com/lidp280504357/exchange/internal/instrument/domain"
)

// ToProtoAsset converts an asset and its networks.
func ToProtoAsset(a domain.Asset, networks []domain.Network) *instrumentv1.Asset {
	out := &instrumentv1.Asset{
		AssetCode: a.Code, Name: a.Name, Decimals: a.Decimals, DepositEnabled: a.DepositEnabled,
		WithdrawEnabled: a.WithdrawEnabled, TradingEnabled: a.TradingEnabled, RiskRestricted: a.RiskRestricted, Version: a.Version,
		Rank: a.Rank, Categories: a.Categories, Hidden: a.Hidden,
	}
	for _, n := range networks {
		out.Networks = append(out.Networks, ToProtoNetwork(n))
	}
	return out
}

// ToProtoNetwork converts a network.
func ToProtoNetwork(n domain.Network) *instrumentv1.Network {
	return &instrumentv1.Network{
		AssetCode: n.AssetCode, Network: n.Network, Chain: n.Chain, ContractAddress: n.ContractAddress,
		Confirmations: n.Confirmations, MinDeposit: n.MinDeposit.String(), MinWithdraw: n.MinWithdraw.String(),
		WithdrawFee: n.WithdrawFee.String(), MemoRequired: n.MemoRequired, DepositEnabled: n.DepositEnabled,
		WithdrawEnabled: n.WithdrawEnabled, Version: n.Version, DisplayName: n.DisplayName, AddressFormat: n.AddressFormat,
		EtaMinutes: n.ETAMinutes, ExplorerTxUrl: n.ExplorerTxURL, ExplorerAddressUrl: n.ExplorerAddressURL,
		Provider: n.Provider, ProviderCoin: n.ProviderCoin,
	}
}

// ToProtoPair converts a pair with its fee rates.
func ToProtoPair(p PairView) *instrumentv1.TradingPair {
	return &instrumentv1.TradingPair{
		Symbol: p.Symbol, BaseAsset: p.BaseAsset, QuoteAsset: p.QuoteAsset, TickSize: p.TickSize.String(),
		LotSize: p.LotSize.String(), MinQuantity: p.MinQuantity.String(), MaxQuantity: p.MaxQuantity.String(),
		MinNotional: p.MinNotional.String(), PriceBand: p.PriceBand.String(), FeeTier: p.FeeTier,
		MakerFeeRate: p.MakerFeeRate.String(), TakerFeeRate: p.TakerFeeRate.String(), Status: p.Status, Version: p.Version,
		ReferenceSymbol: p.ReferenceSymbol, ReferenceMultiplier: p.ReferenceMultiplier.String(), ListedAt: timestamppb.New(p.ListedAt),
	}
}

// ToProtoFee converts a fee schedule.
func ToProtoFee(f domain.FeeSchedule) *instrumentv1.FeeSchedule {
	return &instrumentv1.FeeSchedule{
		Tier: f.Tier, MakerFeeRate: f.MakerFeeRate.String(), TakerFeeRate: f.TakerFeeRate.String(), Version: f.Version,
	}
}

// ToProtoProfile converts an asset's profile.
func ToProtoProfile(p domain.AssetProfile) *instrumentv1.AssetProfile {
	return &instrumentv1.AssetProfile{
		DisplayName: p.DisplayName, Description: p.Description, Links: p.Links, LogoMime: p.LogoMIME,
		LogoSize: int32(p.LogoSize), LogoUrl: LogoURL(p), Version: p.Version, //nolint:gosec // at most 200 KB
	}
}
