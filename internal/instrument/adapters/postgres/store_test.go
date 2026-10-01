package postgres_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/instrument/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/instrument/application"
	"github.com/lidp280504357/exchange/internal/instrument/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
	"github.com/lidp280504357/exchange/migrations"
)

func setup(t *testing.T) (*application.Service, *pg.DB) {
	t.Helper()
	db := testenv.Postgres(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Instrument(), log); err != nil {
		t.Fatal(err)
	}
	return &application.Service{Store: postgres.NewStore(db, event.NewFactory("instrument-service", "test"))}, db
}

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func config() application.Config {
	return application.Config{
		FeeSchedules: []domain.FeeSchedule{{Tier: "default", MakerFeeRate: d("0.001"), TakerFeeRate: d("0.001")}},
		Assets: []application.AssetConfig{
			{Asset: domain.Asset{Code: "USDT", Name: "Tether USD", Decimals: 6, TradingEnabled: true}, Networks: []domain.Network{{
				Network: "ETH-SEPOLIA", Chain: "11155111", Confirmations: 12, MinDeposit: d("1"), MinWithdraw: d("10"), WithdrawFee: d("1"),
			}}},
			{Asset: domain.Asset{Code: "BTC", Name: "Bitcoin", Decimals: 8, TradingEnabled: true}},
		},
		Pairs: []domain.TradingPair{{
			Symbol: "BTC-USDT", BaseAsset: "BTC", QuoteAsset: "USDT", TickSize: d("0.01"), LotSize: d("0.0001"),
			MinQuantity: d("0.0001"), MaxQuantity: d("100"), MinNotional: d("5"), PriceBand: d("0.1"), FeeTier: "default",
		}},
	}
}

func count(t *testing.T, db *pg.DB, sql string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestApplyIsIdempotent(t *testing.T) {
	svc, db := setup(t)
	ctx := context.Background()
	cfg := config()

	res, err := svc.Apply(ctx, cfg, "cli:test", "seed")
	if err != nil || len(res.Changed) != 5 || res.Unchanged != 0 {
		t.Fatalf("first apply: %+v %v", res, err)
	}
	if res, err = svc.Apply(ctx, cfg, "cli:test", "seed again"); err != nil || len(res.Changed) != 0 || res.Unchanged != 5 {
		t.Fatalf("second apply: %+v %v", res, err)
	}
	if n := count(t, db, `SELECT count(*) FROM outbox WHERE topic = 'instrument.events'`); n != 5 {
		t.Fatalf("events: %d", n)
	}

	// A changed fee bumps the version; the pair keeps its status.
	if _, err := svc.SetPairStatus(ctx, "BTC-USDT", domain.StatusTrading, "cli:test", "open"); err != nil {
		t.Fatal(err)
	}
	cfg.FeeSchedules[0].TakerFeeRate = d("0.0015")
	cfg.Pairs[0].MinNotional = d("10")
	if res, err = svc.Apply(ctx, cfg, "cli:test", "cheaper makers"); err != nil || len(res.Changed) != 2 {
		t.Fatalf("change: %+v %v", res, err)
	}
	p, err := svc.Pair(ctx, "BTC-USDT")
	if err != nil || p.Status != domain.StatusTrading || !p.MinNotional.Equal(d("10")) || !p.TakerFeeRate.Equal(d("0.0015")) || p.Version != 3 {
		t.Fatalf("pair: %+v %v", p, err)
	}
	if n := count(t, db, `SELECT count(*) FROM config_history WHERE entity = 'TRADING_PAIR' AND key = 'BTC-USDT'`); n != 3 {
		t.Fatalf("pair history: %d", n)
	}

	// Decimals are fixed once set; bad input rolls the whole apply back.
	bad := config()
	bad.Assets[1].Decimals = 6
	if _, err := svc.Apply(ctx, bad, "cli:test", "shrink BTC"); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("decimals change: %v", err)
	}
	bad = config()
	bad.FeeSchedules[0].MakerFeeRate = d("0.002")
	bad.Pairs[0].TickSize = d("0.0000001")
	if _, err := svc.Apply(ctx, bad, "cli:test", "bad tick"); err == nil {
		t.Fatal("tick finer than USDT accepted")
	}
	if f, _ := svc.Pairs(ctx); !f[0].MakerFeeRate.Equal(d("0.001")) {
		t.Fatalf("a failed apply must change nothing: %+v", f[0])
	}

	assets, err := svc.Assets(ctx)
	if err != nil || len(assets) != 2 || assets[1].Code != "USDT" || len(assets[1].Networks) != 1 || !assets[1].Networks[0].WithdrawFee.Equal(d("1")) {
		t.Fatalf("assets: %+v %v", assets, err)
	}
}

func TestPairStatusMachine(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	if _, err := svc.Apply(ctx, config(), "cli:test", "seed"); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		to string
		ok bool
	}{
		{domain.StatusHalt, false},
		{domain.StatusTrading, true},
		{domain.StatusHalt, true},
		{domain.StatusTrading, true},
		{domain.StatusCancelOnly, true},
		{domain.StatusTrading, false},
		{domain.StatusDelisted, true},
	} {
		_, err := svc.SetPairStatus(ctx, "BTC-USDT", step.to, "cli:test", "test")
		if (err == nil) != step.ok {
			t.Fatalf("-> %s: %v", step.to, err)
		}
	}
	if _, err := svc.SetPairStatus(ctx, "NOPE-USDT", domain.StatusTrading, "cli:test", "test"); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("unknown pair: %v", err)
	}
	if _, err := svc.SetPairStatus(ctx, "BTC-USDT", domain.StatusTrading, "cli:test", ""); err == nil {
		t.Fatal("a reason is required")
	}
}

func TestListingMetadata(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	cfg := config()
	cfg.Assets[1].Rank, cfg.Assets[1].Categories = 1, []string{"layer-1", "pow"}
	cfg.Assets[0].Networks[0].DisplayName, cfg.Assets[0].Networks[0].ETAMinutes = "Sepolia", 3
	cfg.Assets[0].Networks[0].ExplorerTxURL = "https://sepolia.etherscan.io/tx/{tx}"
	cfg.Pairs[0].ReferenceSymbol = "BTCUSDT"
	if _, err := svc.Apply(ctx, cfg, "cli:test", "seed"); err != nil {
		t.Fatal(err)
	}
	p, err := svc.Pair(ctx, "BTC-USDT")
	if err != nil || p.ReferenceSymbol != "BTCUSDT" || !p.ReferenceMultiplier.Equal(d("1")) || p.ListedAt.IsZero() ||
		p.BaseName != "Bitcoin" || p.Rank != 1 || len(p.Categories) != 2 {
		t.Fatalf("pair: %+v %v", p, err)
	}
	// Omitted multiplier and listing time keep what is stored: nothing changes.
	if res, err := svc.Apply(ctx, cfg, "cli:test", "again"); err != nil || len(res.Changed) != 0 {
		t.Fatalf("second apply: %+v %v", res, err)
	}
	listed := p.ListedAt
	cfg.Pairs[0].ReferenceSymbol, cfg.Pairs[0].ReferenceMultiplier = "PEPEUSDT", d("1000")
	if res, err := svc.Apply(ctx, cfg, "cli:test", "remap"); err != nil || len(res.Changed) != 1 {
		t.Fatalf("remap: %+v %v", res, err)
	}
	pairs, err := svc.Pairs(ctx)
	if err != nil || len(pairs) != 1 || pairs[0].ReferenceSymbol != "PEPEUSDT" || !pairs[0].ReferenceMultiplier.Equal(d("1000")) ||
		!pairs[0].ListedAt.Equal(listed) || pairs[0].BaseName != "Bitcoin" {
		t.Fatalf("pairs: %+v %v", pairs, err)
	}
	assets, err := svc.Assets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n := assets[1].Networks[0]
	if n.DisplayName != "Sepolia" || n.AddressFormat != domain.FormatEVM || n.ETAMinutes != 3 || n.ExplorerTxURL == "" {
		t.Fatalf("network: %+v", n)
	}
	if assets[0].Rank != 1 || assets[0].Categories[1] != "pow" {
		t.Fatalf("asset: %+v", assets[0])
	}
}

// An operator's profile change (ASTRA design §5.3): the text and a cleaned
// logo are stored, the version goes up, the history keeps both sides and a
// later apply of the reference data leaves the profile alone.
func TestAssetProfiles(t *testing.T) {
	svc, db := setup(t)
	ctx := context.Background()
	if _, err := svc.Apply(ctx, config(), "test", "seed"); err != nil {
		t.Fatal(err)
	}
	svg := []byte(`<svg viewBox="0 0 10 10"><script>x</script><circle cx="5" cy="5" r="5"/></svg>`)
	p, err := svc.UpdateProfile(ctx, "BTC", application.ProfileChange{
		DisplayName: "Bitcoin 比特币", Description: map[string]string{"zh-CN": "数字黄金", "en": "Digital gold"},
		Links: map[string]string{"website": "https://bitcoin.org"}, Logo: &domain.Logo{Data: svg, MIME: domain.LogoSVG},
	}, "ops", "a new profile")
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != 1 || p.DisplayName != "Bitcoin 比特币" || p.LogoMIME != domain.LogoSVG || p.LogoSize == 0 || application.LogoURL(p) != "/v1/market/assets/BTC/logo?v=1" {
		t.Fatalf("profile %+v", p)
	}
	logo, version, err := svc.Logo(ctx, "BTC")
	if err != nil || version != 1 || len(logo.Data) != p.LogoSize || string(logo.Data) == string(svg) {
		t.Fatalf("logo %s v%d %v", logo.Data, version, err)
	}
	if n := count(t, db, `SELECT count(*) FROM config_history WHERE entity = 'ASSET_PROFILE' AND key = 'BTC'
		AND value->'old'->>'display_name' = '' AND value->'new'->>'logo_sha256' <> '' AND actor = 'ops'`); n != 1 {
		t.Fatalf("%d history rows", n)
	}
	// Text only keeps the logo; clearing it removes it.
	p, err = svc.UpdateProfile(ctx, "BTC", application.ProfileChange{DisplayName: "Bitcoin"}, "ops", "the name only")
	if err != nil || p.Version != 2 || p.LogoSize == 0 || len(p.Description) != 0 {
		t.Fatalf("text only %+v %v", p, err)
	}
	if _, err := svc.Apply(ctx, config(), "test", "again"); err != nil {
		t.Fatal(err)
	}
	views, err := svc.Assets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if v.Code == "BTC" && (v.Profile.DisplayName != "Bitcoin" || v.Profile.Version != 2) {
			t.Fatalf("after an apply %+v", v.Profile)
		}
	}
	if p, err = svc.UpdateProfile(ctx, "BTC", application.ProfileChange{ClearLogo: true}, "ops", "no logo"); err != nil || p.LogoSize != 0 {
		t.Fatalf("cleared %+v %v", p, err)
	}
	if _, _, err := svc.Logo(ctx, "BTC"); apperr.From(err).Code != "COMMON_NOT_FOUND" {
		t.Fatalf("no logo: %v", err)
	}
	// What the server refuses.
	for name, ch := range map[string]application.ProfileChange{
		"an oblong logo": {Logo: &domain.Logo{Data: []byte(`<svg viewBox="0 0 10 20"/>`), MIME: domain.LogoSVG}},
		"a GIF":          {Logo: &domain.Logo{Data: []byte("GIF89a"), MIME: "image/gif"}},
		"a bad link":     {Links: map[string]string{"website": "ftp://x"}},
	} {
		if _, err := svc.UpdateProfile(ctx, "BTC", ch, "ops", "bad"); apperr.From(err).Code != "COMMON_INVALID_ARGUMENT" {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := svc.UpdateProfile(ctx, "NOPE", application.ProfileChange{}, "ops", "missing"); apperr.From(err).Code != "COMMON_NOT_FOUND" {
		t.Fatalf("unknown asset: %v", err)
	}
	if _, err := svc.UpdateProfile(ctx, "BTC", application.ProfileChange{}, "ops", ""); apperr.From(err).Code != "COMMON_INVALID_ARGUMENT" {
		t.Fatalf("no reason: %v", err)
	}
}
