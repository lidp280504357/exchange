package application

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// TermsFile is the seed of the margin terms (deploy/instruments/margin.json,
// E0 contract §9): the cross account, the fee, the floating curve every
// FLOATING asset starts from, the assets and the pairs.
type TermsFile struct {
	Cross struct {
		Leverage         int             `json:"leverage"`
		WarnLevel        decimal.Decimal `json:"warn_level"`
		LiquidationLevel decimal.Decimal `json:"liquidation_level"`
	} `json:"cross"`
	LiquidationFeeRate decimal.Decimal `json:"liquidation_fee_rate"`
	Floating           struct {
		BaseRate decimal.Decimal `json:"base_rate"`
		Kink     decimal.Decimal `json:"kink"`
		KinkRate decimal.Decimal `json:"kink_rate"`
		MaxRate  decimal.Decimal `json:"max_rate"`
	} `json:"floating"`
	Assets []struct {
		Asset         string          `json:"asset"`
		Borrowable    *bool           `json:"borrowable"`
		Collateral    *bool           `json:"collateral"`
		Haircut       decimal.Decimal `json:"haircut"`
		PoolCap       decimal.Decimal `json:"pool_cap"`
		UserCap       decimal.Decimal `json:"user_cap"`
		InterestModel string          `json:"interest_model"`
		HourlyRate    decimal.Decimal `json:"hourly_rate"`
	} `json:"assets"`
	Pairs []struct {
		Symbol           string           `json:"symbol"`
		Isolated         *bool            `json:"isolated"`
		IsolatedLeverage int              `json:"isolated_leverage"`
		WarnLevel        *decimal.Decimal `json:"warn_level"`
		LiquidationLevel *decimal.Decimal `json:"liquidation_level"`
	} `json:"pairs"`
}

// ReadTermsFile decodes a seed strictly: an unknown key is an error.
func ReadTermsFile(r io.Reader) (TermsFile, error) {
	var f TermsFile
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return TermsFile{}, fmt.Errorf("margin terms: %w", err)
	}
	return f, nil
}

// Seed is a TermsFile turned into terms, every one of them checked.
type Seed struct {
	Cross  domain.Terms
	Assets []domain.AssetTerms
	Pairs  []domain.Pair
}

// Terms checks the file and returns its terms: assets borrowable and
// collateral unless set false, pairs isolated unless set false with the
// warning and liquidation levels of their leverage unless set.
func (f TermsFile) Terms() (Seed, error) {
	fee := f.LiquidationFeeRate
	cross := domain.Terms{Leverage: f.Cross.Leverage, WarnLevel: f.Cross.WarnLevel, LiquidationLevel: f.Cross.LiquidationLevel, LiquidationFee: fee}
	if err := cross.Validate(); err != nil {
		return Seed{}, apperr.Invalid("cross: " + apperr.From(err).Message)
	}
	floating := domain.FloatingRate{Base: f.Floating.BaseRate, Kink: f.Floating.Kink, KinkRate: f.Floating.KinkRate, MaxRate: f.Floating.MaxRate}
	if err := floating.Validate(); err != nil {
		return Seed{}, err
	}
	out := Seed{Cross: cross}
	seen := map[string]bool{}
	for _, a := range f.Assets {
		t := domain.AssetTerms{
			Asset: strings.ToUpper(a.Asset), Borrowable: a.Borrowable == nil || *a.Borrowable, Collateral: a.Collateral == nil || *a.Collateral,
			Haircut: a.Haircut, PoolCap: a.PoolCap, UserCap: a.UserCap, Model: domain.InterestModel(a.InterestModel),
			FixedRate: a.HourlyRate, Floating: floating,
		}
		if err := t.Validate(); err != nil {
			return Seed{}, err
		}
		if seen[t.Asset] {
			return Seed{}, apperr.Invalid(t.Asset + " twice")
		}
		seen[t.Asset] = true
		out.Assets = append(out.Assets, t)
	}
	pairs := map[string]bool{}
	for _, p := range f.Pairs {
		symbol := strings.ToUpper(p.Symbol)
		base, quote, ok := strings.Cut(symbol, "-")
		switch {
		case !ok || base == "" || quote == "":
			return Seed{}, apperr.Invalid(fmt.Sprintf("pair %q: a symbol is BASE-QUOTE", p.Symbol))
		case !seen[base] || !seen[quote]:
			return Seed{}, apperr.Invalid(fmt.Sprintf("pair %s: both assets must be on the margin list", symbol))
		case pairs[symbol]:
			return Seed{}, apperr.Invalid(symbol + " twice")
		}
		pairs[symbol] = true
		terms := domain.DefaultTerms(domain.AccountIsolated, p.IsolatedLeverage)
		terms.LiquidationFee = fee
		if p.WarnLevel != nil {
			terms.WarnLevel = *p.WarnLevel
		}
		if p.LiquidationLevel != nil {
			terms.LiquidationLevel = *p.LiquidationLevel
		}
		if err := terms.Validate(); err != nil {
			return Seed{}, apperr.Invalid(symbol + ": " + apperr.From(err).Message)
		}
		out.Pairs = append(out.Pairs, domain.Pair{Symbol: symbol, Base: base, Quote: quote, Isolated: p.Isolated == nil || *p.Isolated, Terms: terms})
	}
	return out, nil
}

// SourceFile marks terms a seed wrote (updated_by); the console writes
// its administrator's name.
const SourceFile = "file"

// ApplyOptions are a seed's run: DryRun changes nothing, Force overwrites
// what the console changed.
type ApplyOptions struct {
	DryRun bool
	Force  bool
}

// ApplyResult lists what a seed changed and what it kept because the
// console had changed it, as "asset BTC", "pair BTC-USDT", "cross".
type ApplyResult struct {
	Changed   []string
	Kept      []string
	Unchanged int
}

// ApplyTerms writes a seed's terms (the file is the source of a fresh
// environment, the console of a running one): a missing item is created,
// an item the seed wrote last is updated when it differs, an item the
// console changed is kept unless Force.
func ApplyTerms(ctx context.Context, store ports.Store, seed Seed, o ApplyOptions) (ApplyResult, error) {
	var res ApplyResult
	err := store.Tx(ctx, func(r ports.Repos) error {
		res = ApplyResult{}
		write := func(name, by string, exists, same bool, save func() error) error {
			switch {
			case exists && same:
				res.Unchanged++
				return nil
			case exists && by != SourceFile && !o.Force:
				res.Kept = append(res.Kept, name+" (changed by "+by+")")
				return nil
			}
			res.Changed = append(res.Changed, name)
			if o.DryRun {
				return nil
			}
			return save()
		}
		cur, by, ok, err := r.Terms().CrossWithSource(ctx)
		if err != nil {
			return err
		}
		if err := write("cross", by, ok, sameTerms(cur, seed.Cross), func() error {
			return r.Terms().SaveCross(ctx, seed.Cross, SourceFile)
		}); err != nil {
			return err
		}
		for _, a := range seed.Assets {
			cur, by, ok, err := r.Terms().AssetWithSource(ctx, a.Asset)
			if err != nil {
				return err
			}
			if err := write("asset "+a.Asset, by, ok, sameAsset(cur, a), func() error {
				return r.Terms().SaveAsset(ctx, a, SourceFile)
			}); err != nil {
				return err
			}
		}
		for _, p := range seed.Pairs {
			cur, by, ok, err := r.Terms().PairWithSource(ctx, p.Symbol)
			if err != nil {
				return err
			}
			if err := write("pair "+p.Symbol, by, ok, samePair(cur, p), func() error {
				return r.Terms().SavePair(ctx, p, SourceFile)
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return res, err
}

func sameTerms(a, b domain.Terms) bool {
	return a.Leverage == b.Leverage && a.WarnLevel.Equal(b.WarnLevel) && a.LiquidationLevel.Equal(b.LiquidationLevel) &&
		a.LiquidationFee.Equal(b.LiquidationFee)
}

func sameAsset(a, b domain.AssetTerms) bool {
	return a.Borrowable == b.Borrowable && a.Collateral == b.Collateral && a.Haircut.Equal(b.Haircut) && a.PoolCap.Equal(b.PoolCap) &&
		a.UserCap.Equal(b.UserCap) && a.Model == b.Model && a.FixedRate.Equal(b.FixedRate) && a.Floating.Base.Equal(b.Floating.Base) &&
		a.Floating.Kink.Equal(b.Floating.Kink) && a.Floating.KinkRate.Equal(b.Floating.KinkRate) && a.Floating.MaxRate.Equal(b.Floating.MaxRate)
}

func samePair(a, b domain.Pair) bool {
	return a.Symbol == b.Symbol && a.Base == b.Base && a.Quote == b.Quote && a.Isolated == b.Isolated && sameTerms(a.Terms, b.Terms)
}
