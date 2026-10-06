package application_test

import (
	"context"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/skill/exchange/internal/margin/adapters/postgres"
	"github.com/skill/exchange/internal/margin/application"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/testenv"
)

const seedFile = `{
  "cross": {"leverage": 3, "warn_level": "1.30", "liquidation_level": "1.10"},
  "liquidation_fee_rate": "0.02",
  "floating": {"base_rate": "0.000005", "kink": "0.8", "kink_rate": "0.00003", "max_rate": "0.0001"},
  "assets": [
    {"asset": "usdt", "haircut": "1", "pool_cap": "2000000", "user_cap": "200000", "interest_model": "FIXED", "hourly_rate": "0.00001"},
    {"asset": "BTC", "borrowable": false, "haircut": "0.95", "pool_cap": "20", "user_cap": "2", "interest_model": "FLOATING", "hourly_rate": "0"}
  ],
  "pairs": [
    {"symbol": "btc-usdt", "isolated_leverage": 10}
  ]
}`

func readSeed(t *testing.T, text string) (application.Seed, error) {
	t.Helper()
	f, err := application.ReadTermsFile(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return f.Terms()
}

// TestTermsFile checks the seed of the margin terms: strict keys; assets
// borrowable and collateral and pairs isolated unless set false, a pair's
// levels those of its leverage unless set, the fee on every pair; the
// refusals; and the repository's own seed (deploy/instruments/margin.json).
func TestTermsFile(t *testing.T) {
	if _, err := application.ReadTermsFile(strings.NewReader(`{"cross": {"leverage": 3, "margin": "1"}}`)); err == nil {
		t.Fatal("an unknown key read")
	}
	seed, err := readSeed(t, seedFile)
	if err != nil {
		t.Fatal(err)
	}
	if seed.Cross.Leverage != 3 || !seed.Cross.WarnLevel.Equal(d("1.3")) || !seed.Cross.LiquidationFee.Equal(d("0.02")) {
		t.Fatalf("cross %+v", seed.Cross)
	}
	if len(seed.Assets) != 2 || seed.Assets[0].Asset != "USDT" || !seed.Assets[0].Borrowable || !seed.Assets[0].Collateral ||
		seed.Assets[1].Borrowable || seed.Assets[1].Model != domain.InterestFloating || !seed.Assets[1].Floating.KinkRate.Equal(d("0.00003")) {
		t.Fatalf("assets %+v", seed.Assets)
	}
	if len(seed.Pairs) != 1 {
		t.Fatalf("pairs %+v", seed.Pairs)
	}
	if p := seed.Pairs[0]; p.Symbol != "BTC-USDT" || p.Base != "BTC" || p.Quote != "USDT" || !p.Isolated || p.Terms.Leverage != 10 ||
		!p.Terms.WarnLevel.Equal(d("1.1")) || !p.Terms.LiquidationLevel.Equal(d("1.05")) || !p.Terms.LiquidationFee.Equal(d("0.02")) {
		t.Fatalf("the pair %+v", p)
	}
	for _, c := range []struct{ name, from, to string }{
		{"a symbol not BASE-QUOTE", `"btc-usdt"`, `"BTCUSDT"`},
		{"an asset off the list", `"btc-usdt"`, `"eth-usdt"`},
		{"a pair twice", `{"symbol": "btc-usdt", "isolated_leverage": 10}`, `{"symbol": "btc-usdt", "isolated_leverage": 10},
    {"symbol": "BTC-USDT", "isolated_leverage": 5}`},
		{"an asset twice", `"asset": "BTC"`, `"asset": "USDT"`},
		{"a haircut above 1", `"haircut": "0.95"`, `"haircut": "1.5"`},
		{"a user cap above the pool", `"user_cap": "2"`, `"user_cap": "21"`},
		{"a leverage above the most", `"isolated_leverage": 10`, `"isolated_leverage": 11`},
		{"cross levels the wrong way round", `"warn_level": "1.30"`, `"warn_level": "1.05"`},
		{"an unknown model", `"FLOATING"`, `"CURVED"`},
	} {
		if !strings.Contains(seedFile, c.from) {
			t.Fatalf("%s: %s is not in the seed", c.name, c.from)
		}
		if _, err := readSeed(t, strings.Replace(seedFile, c.from, c.to, 1)); err == nil {
			t.Errorf("%s: read", c.name)
		}
	}

	f, err := os.Open("../../../deploy/instruments/margin.json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	file, err := application.ReadTermsFile(f)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := file.Terms()
	if err != nil || len(repo.Assets) == 0 || len(repo.Pairs) == 0 {
		t.Fatalf("deploy/instruments/margin.json: %d assets, %d pairs: %v", len(repo.Assets), len(repo.Pairs), err)
	}
}

// emptyStore is a margin schema with no terms in it.
func emptyStore(t *testing.T) *postgres.Store {
	t.Helper()
	ctx := context.Background()
	db := testenv.Postgres(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, os.DirFS("../../../migrations/margin"), log); err != nil {
		t.Fatal(err)
	}
	return postgres.NewStore(db, event.NewFactory("margin-service", "test"))
}

// TestApplyTerms checks a seed's run (exchangectl margin apply): all of it
// written the first time, nothing the second; an item the console changed
// kept unless forced, one the seed wrote updated; a dry run writes
// nothing.
func TestApplyTerms(t *testing.T) {
	ctx := context.Background()
	store := emptyStore(t)
	seed, err := readSeed(t, seedFile)
	if err != nil {
		t.Fatal(err)
	}
	all := []string{"cross", "asset USDT", "asset BTC", "pair BTC-USDT"}
	if res, err := application.ApplyTerms(ctx, store, seed, application.ApplyOptions{DryRun: true}); err != nil ||
		!slices.Equal(res.Changed, all) {
		t.Fatalf("a dry run %+v %v", res, err)
	}
	if _, _, ok, err := store.Read().Terms().AssetWithSource(ctx, "USDT"); err != nil || ok {
		t.Fatalf("a dry run wrote %v %v", ok, err)
	}
	if res, err := application.ApplyTerms(ctx, store, seed, application.ApplyOptions{}); err != nil || !slices.Equal(res.Changed, all) {
		t.Fatalf("the first run %+v %v", res, err)
	}
	if res, err := application.ApplyTerms(ctx, store, seed, application.ApplyOptions{}); err != nil || len(res.Changed) != 0 ||
		res.Unchanged != len(all) {
		t.Fatalf("the second run %+v %v", res, err)
	}

	// An administrator lowers BTC's pool; the seed changes USDT's and BTC's.
	err = store.Tx(ctx, func(r ports.Repos) error {
		btc := seed.Assets[1]
		btc.PoolCap = d("10")
		return r.Terms().SaveAsset(ctx, btc, "ops@example.com")
	})
	if err != nil {
		t.Fatal(err)
	}
	seed.Assets[0].UserCap, seed.Assets[1].UserCap = d("100000"), d("1")
	res, err := application.ApplyTerms(ctx, store, seed, application.ApplyOptions{})
	if err != nil || !slices.Equal(res.Changed, []string{"asset USDT"}) || !slices.Equal(res.Kept, []string{"asset BTC (changed by ops@example.com)"}) {
		t.Fatalf("a run after the console %+v %v", res, err)
	}
	btc, by, _, err := store.Read().Terms().AssetWithSource(ctx, "BTC")
	if err != nil || by != "ops@example.com" || !btc.PoolCap.Equal(d("10")) || !btc.UserCap.Equal(d("2")) {
		t.Fatalf("BTC kept %+v %s %v", btc, by, err)
	}
	if res, err := application.ApplyTerms(ctx, store, seed, application.ApplyOptions{Force: true}); err != nil ||
		!slices.Equal(res.Changed, []string{"asset BTC"}) {
		t.Fatalf("forced %+v %v", res, err)
	}
	if btc, by, _, err := store.Read().Terms().AssetWithSource(ctx, "BTC"); err != nil || by != application.SourceFile ||
		!btc.PoolCap.Equal(d("20")) || !btc.UserCap.Equal(d("1")) {
		t.Fatalf("BTC forced %+v %s %v", btc, by, err)
	}
}
