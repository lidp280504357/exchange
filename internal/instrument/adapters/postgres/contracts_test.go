package postgres_test

import (
	"context"
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
