package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/skill/exchange/internal/instrument/domain"
	"github.com/skill/exchange/internal/platform/apperr"
)

func contract() domain.Contract {
	return domain.Contract{
		Symbol: "BTC-USDT-PERP", Type: domain.ContractPerpetual, BaseAsset: "BTC", QuoteAsset: "USDT", IndexSymbol: "BTC-USDT",
		TickSize: d("0.1"), LotSize: d("0.001"), MinQuantity: d("0.001"), MaxQuantity: d("100"), MinNotional: d("5"),
		PriceBand: d("0.05"), FundingIntervalHours: 8, InterestRate: d("0.0001"), FundingCap: d("0.0075"),
		ImpactNotional: d("10000"), FeeTier: "default",
		RiskTiers: []domain.RiskTier{
			{MaxNotional: d("50000"), MaxLeverage: 50, MMR: d("0.004")},
			{MaxNotional: d("250000"), MaxLeverage: 20, MMR: d("0.01")},
		},
	}
}

func TestContracts(t *testing.T) {
	svc, db := setup(t)
	ctx := context.Background()
	cfg := config()
	cfg.Contracts = []domain.Contract{contract()}
	res, err := svc.Apply(ctx, cfg, "cli:test", "first contract")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changed) != 6 { // fee, 2 assets, network, pair, contract
		t.Fatalf("changed %v", res.Changed)
	}
	c, err := svc.Contract(ctx, "BTC-USDT-PERP")
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != domain.StatusPrepare || c.Version != 1 || len(c.RiskTiers) != 2 || !c.RiskTiers[1].MMR.Equal(d("0.01")) ||
		!c.TakerFeeRate.Equal(d("0.001")) || c.MaxLeverage() != 50 {
		t.Fatalf("contract %+v", c)
	}
	// Applying the same file again changes nothing; a new tier is a new version.
	if res, err := svc.Apply(ctx, cfg, "cli:test", "again"); err != nil || len(res.Changed) != 0 {
		t.Fatalf("again: %v %v", res.Changed, err)
	}
	if _, err := svc.SetContractStatus(ctx, "BTC-USDT-PERP", domain.StatusTrading, "cli:test", "open it"); err != nil {
		t.Fatal(err)
	}
	cfg.Contracts[0].RiskTiers = append(cfg.Contracts[0].RiskTiers, domain.RiskTier{MaxNotional: d("1000000"), MaxLeverage: 10, MMR: d("0.025")})
	if res, err := svc.Apply(ctx, cfg, "cli:test", "a third tier"); err != nil || len(res.Changed) != 1 {
		t.Fatalf("third tier: %v %v", res.Changed, err)
	}
	c, err = svc.Contract(ctx, "BTC-USDT-PERP")
	if err != nil || c.Status != domain.StatusTrading || c.Version != 3 || len(c.RiskTiers) != 3 {
		t.Fatalf("after the tier: %+v %v", c, err)
	}
	if _, err := svc.SetContractStatus(ctx, "BTC-USDT-PERP", domain.StatusPrepare, "cli:test", "back"); !apperr.Is(err, "INSTRUMENT_STATUS_TRANSITION_INVALID") {
		t.Fatalf("TRADING -> PREPARE: %v", err)
	}
	bad := cfg
	bad.Contracts = []domain.Contract{contract()}
	bad.Contracts[0].RiskTiers[0].MMR = d("0.5")
	if _, err := svc.Apply(ctx, bad, "cli:test", "bad"); err == nil {
		t.Fatal("an mmr above 1/leverage was accepted")
	}
	if n := count(t, db, `SELECT count(*) FROM config_history WHERE entity = 'CONTRACT'`); n != 3 {
		t.Fatalf("%d contract versions in the history, want 3", n)
	}
	if n := count(t, db, `SELECT count(*) FROM outbox WHERE event_type IN ('instrument.ContractUpserted', 'instrument.ContractStatusChanged')`); n != 3 {
		t.Fatalf("%d contract events, want 3", n)
	}
}

func TestInverseContracts(t *testing.T) {
	svc, db := setup(t)
	ctx := context.Background()
	cfg := config()
	inverse := contract()
	inverse.Symbol, inverse.QuoteAsset, inverse.MarginType, inverse.ContractSize = "BTC-USD-PERP", domain.CoinQuote, domain.MarginCoin, d("100")
	inverse.LotSize, inverse.MinQuantity, inverse.MaxQuantity, inverse.MinNotional, inverse.ImpactNotional = d("1"), d("1"), d("60000"), d("100"), d("100")
	inverse.ReferenceSymbol = "BTCUSD_PERP"
	cfg.Contracts = []domain.Contract{contract(), inverse}
	if _, err := svc.Apply(ctx, cfg, "cli:test", "a linear and an inverse contract"); err != nil {
		t.Fatal(err)
	}
	c, err := svc.Contract(ctx, "BTC-USD-PERP")
	if err != nil {
		t.Fatal(err)
	}
	if c.MarginType != domain.MarginCoin || c.SettleAsset != "BTC" || !c.ContractSize.Equal(d("100")) || c.ReferenceSymbol != "BTCUSD_PERP" ||
		c.QuoteAsset != domain.CoinQuote || c.IndexSymbol != "BTC-USDT" {
		t.Fatalf("inverse contract %+v", c)
	}
	linear, err := svc.Contract(ctx, "BTC-USDT-PERP")
	if err != nil || linear.MarginType != domain.MarginUSDT || linear.SettleAsset != "USDT" || !linear.ContractSize.IsZero() {
		t.Fatalf("linear contract %+v %v", linear, err)
	}
	// The default list keeps the clients from before on the linear contracts.
	for filter, want := range map[string][]string{
		"": {"BTC-USDT-PERP"}, "USDT": {"BTC-USDT-PERP"}, "COIN": {"BTC-USD-PERP"}, "ALL": {"BTC-USD-PERP", "BTC-USDT-PERP"},
	} {
		list, err := svc.ContractsOf(ctx, filter)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, c := range list {
			got = append(got, c.Symbol)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("margin_type %q: %v, want %v", filter, got, want)
		}
	}
	if _, err := svc.ContractsOf(ctx, "USDC"); !apperr.Is(err, "COMMON_INVALID_ARGUMENT") {
		t.Fatalf("an unknown margin type: %v", err)
	}
	// The kind never changes; the reference symbol does.
	changed := cfg
	changed.Contracts = []domain.Contract{contract(), inverse}
	changed.Contracts[1].ContractSize = d("10")
	if _, err := svc.Apply(ctx, changed, "cli:test", "another face value"); err == nil {
		t.Fatal("a new face value was accepted")
	}
	changed.Contracts[1].ContractSize, changed.Contracts[1].ReferenceSymbol = d("100"), ""
	if res, err := svc.Apply(ctx, changed, "cli:test", "no reference"); err != nil || len(res.Changed) != 1 {
		t.Fatalf("reference symbol: %v %v", res.Changed, err)
	}
	if n := count(t, db, `SELECT count(*) FROM contracts WHERE margin_type = 'COIN' AND reference_symbol = ''`); n != 1 {
		t.Fatalf("%d inverse contracts without a reference, want 1", n)
	}
}
