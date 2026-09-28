package domain

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var (
	btc  = Asset{Code: "BTC", Name: "Bitcoin", Decimals: 8, TradingEnabled: true}
	usdt = Asset{Code: "USDT", Name: "Tether USD", Decimals: 6, TradingEnabled: true}
)

func pair() TradingPair {
	return TradingPair{
		Symbol: "BTC-USDT", BaseAsset: "BTC", QuoteAsset: "USDT", TickSize: d("0.01"), LotSize: d("0.00001"),
		MinQuantity: d("0.00001"), MaxQuantity: d("100"), MinNotional: d("5"), PriceBand: d("0.1"), FeeTier: "default",
		Status: StatusPrepare,
	}
}

func TestScaleHelpers(t *testing.T) {
	if !FitsScale(d("1.123456"), 6) || FitsScale(d("1.1234567"), 6) || !FitsScale(d("100"), 0) {
		t.Fatal("FitsScale")
	}
	if !IsMultipleOf(d("0.00003"), d("0.00001")) || IsMultipleOf(d("0.000015"), d("0.00001")) || IsMultipleOf(d("1"), decimal.Zero) {
		t.Fatal("IsMultipleOf")
	}
}

func TestPairValidation(t *testing.T) {
	if err := pair().Validate(btc, usdt); err != nil {
		t.Fatalf("valid pair: %v", err)
	}
	cases := map[string]func(*TradingPair){
		"symbol format":         func(p *TradingPair) { p.Symbol = "BTCUSDT" },
		"symbol names assets":   func(p *TradingPair) { p.Symbol = "ETH-USDT" },
		"tick finer than quote": func(p *TradingPair) { p.TickSize = d("0.0000001") },
		"lot finer than base":   func(p *TradingPair) { p.LotSize = d("0.000000001") },
		"zero tick":             func(p *TradingPair) { p.TickSize = decimal.Zero },
		"min qty off the lot":   func(p *TradingPair) { p.MinQuantity = d("0.000015") },
		"max below min":         func(p *TradingPair) { p.MaxQuantity = d("0.00001") },
		"negative min notional": func(p *TradingPair) { p.MinNotional = d("-1") },
		"band above 100%":       func(p *TradingPair) { p.PriceBand = d("1.5") },
		"unknown status":        func(p *TradingPair) { p.Status = "LIVE" },
		"fee tier format":       func(p *TradingPair) { p.FeeTier = "VIP 1" },
	}
	for name, mutate := range cases {
		p := pair()
		mutate(&p)
		if err := p.Validate(btc, usdt); !apperr.Is(err, apperr.CodeInvalidArgument) {
			t.Errorf("%s: %v", name, err)
		}
	}
	noTrade := btc
	noTrade.TradingEnabled = false
	if err := pair().Validate(noTrade, usdt); err == nil {
		t.Error("assets must be tradable")
	}
	other := pair()
	other.Status = StatusHalt
	if !pair().SameConfig(other) {
		t.Error("status is not configuration")
	}
	other.TickSize = d("0.1")
	if pair().SameConfig(other) {
		t.Error("tick size is configuration")
	}
}

func TestPairTransitions(t *testing.T) {
	allowed := map[[2]string]bool{
		{StatusPrepare, StatusTrading}: true, {StatusTrading, StatusHalt}: true, {StatusHalt, StatusTrading}: true,
		{StatusTrading, StatusCancelOnly}: true, {StatusHalt, StatusCancelOnly}: true, {StatusCancelOnly, StatusDelisted}: true,
	}
	all := []string{StatusPrepare, StatusTrading, StatusHalt, StatusCancelOnly, StatusDelisted}
	for _, from := range all {
		for _, to := range all {
			if err := CheckPairTransition(from, to); (err == nil) != allowed[[2]string{from, to}] {
				t.Errorf("%s -> %s: %v", from, to, err)
			}
		}
	}
}

func TestAssetNetworkFee(t *testing.T) {
	if err := btc.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, a := range []Asset{{Code: "btc", Name: "x", Decimals: 8}, {Code: "BTC", Decimals: 8}, {Code: "BTC", Name: "x", Decimals: 19}} {
		if a.Validate() == nil {
			t.Errorf("accepted %+v", a)
		}
	}
	n := Network{AssetCode: "USDT", Network: "ETH-SEPOLIA", Chain: "11155111", Confirmations: 12, MinDeposit: d("1"), MinWithdraw: d("10"), WithdrawFee: d("1.5")}
	if err := n.Validate(usdt); err != nil {
		t.Fatal(err)
	}
	n.WithdrawFee = d("0.0000001")
	if n.Validate(usdt) == nil {
		t.Error("fee finer than the asset")
	}
	n.WithdrawFee, n.Network = d("1"), "eth sepolia"
	if n.Validate(usdt) == nil {
		t.Error("network code format")
	}
	f := FeeSchedule{Tier: "default", MakerFeeRate: d("0.001"), TakerFeeRate: d("0.001")}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	f.TakerFeeRate = d("0.1")
	if f.Validate() == nil {
		t.Error("fee rates stay below 10%")
	}
	if (FeeSchedule{Tier: "default", MakerFeeRate: d("0.0010"), TakerFeeRate: d("0.001")}).Same(FeeSchedule{Tier: "default", MakerFeeRate: d("0.001"), TakerFeeRate: d("0.001")}) != true {
		t.Error("equal decimals with different scales are the same")
	}
}
