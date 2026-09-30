// Package application holds instrument-service's use cases.
package application

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	instrumentv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/instrument/v1"
	"github.com/lidp280504357/exchange/internal/instrument/domain"
	"github.com/lidp280504357/exchange/internal/instrument/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Service reads and changes the reference data.
type Service struct {
	Store ports.Store
}

// AssetConfig is an asset with the networks it should have.
type AssetConfig struct {
	domain.Asset
	Networks []domain.Network `json:"networks"`
}

// Config is a declarative set of reference data, as applied by
// "exchangectl instruments apply".
type Config struct {
	FeeSchedules []domain.FeeSchedule `json:"fee_schedules"`
	Assets       []AssetConfig        `json:"assets"`
	Pairs        []domain.TradingPair `json:"pairs"`
	Contracts    []domain.Contract    `json:"contracts"`
}

// ApplyResult lists what an apply did, as "ENTITY key vN".
type ApplyResult struct {
	Changed   []string
	Unchanged int
}

// Apply makes the stored reference data match cfg in one transaction:
// missing items are created, changed ones get a new version, a history
// row and an event, and identical ones are left alone, so applying the
// same file twice changes nothing. A pair's status is set only when the
// pair is created; later changes go through SetPairStatus. Items missing
// from cfg are kept.
func (s *Service) Apply(ctx context.Context, cfg Config, actor, reason string) (ApplyResult, error) {
	if err := domain.ValidReason(reason); err != nil {
		return ApplyResult{}, err
	}
	var res ApplyResult
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		res = ApplyResult{}
		for _, f := range cfg.FeeSchedules {
			if err := s.applyFee(ctx, r, f, actor, reason, &res); err != nil {
				return err
			}
		}
		for _, a := range cfg.Assets {
			if err := s.applyAsset(ctx, r, a, actor, reason, &res); err != nil {
				return err
			}
		}
		for _, p := range cfg.Pairs {
			if err := s.applyPair(ctx, r, p, actor, reason, &res); err != nil {
				return err
			}
		}
		for _, c := range cfg.Contracts {
			if err := s.applyContract(ctx, r, c, actor, reason, &res); err != nil {
				return err
			}
		}
		return nil
	})
	return res, err
}

func (s *Service) applyFee(ctx context.Context, r ports.Repos, f domain.FeeSchedule, actor, reason string, res *ApplyResult) error {
	if err := f.Validate(); err != nil {
		return err
	}
	cur, err := r.FeeSchedules().Get(ctx, f.Tier)
	if err != nil {
		return err
	}
	if cur != nil && cur.Same(f) {
		res.Unchanged++
		return nil
	}
	saved, err := r.FeeSchedules().Save(ctx, f)
	if err != nil {
		return err
	}
	res.Changed = append(res.Changed, fmt.Sprintf("FEE_SCHEDULE %s v%d", saved.Tier, saved.Version))
	if err := r.Record(ctx, "FEE_SCHEDULE", saved.Tier, saved.Version, saved, actor, reason); err != nil {
		return err
	}
	return r.Emit(ctx, &instrumentv1.FeeScheduleChanged{Schedule: ToProtoFee(saved), Actor: actor, Reason: reason}, "fee_tier", saved.Tier)
}

func (s *Service) applyAsset(ctx context.Context, r ports.Repos, a AssetConfig, actor, reason string, res *ApplyResult) error {
	if err := a.Validate(); err != nil {
		return err
	}
	cur, err := r.Assets().Get(ctx, a.Code)
	if err != nil {
		return err
	}
	if cur != nil && cur.Decimals != a.Decimals {
		// Stored amounts and pair steps depend on it.
		return apperr.Invalid(fmt.Sprintf("asset %s: decimals cannot change once set", a.Code))
	}
	if cur != nil && cur.Same(a.Asset) {
		res.Unchanged++
	} else {
		saved, err := r.Assets().Save(ctx, a.Asset)
		if err != nil {
			return err
		}
		res.Changed = append(res.Changed, fmt.Sprintf("ASSET %s v%d", saved.Code, saved.Version))
		if err := r.Record(ctx, "ASSET", saved.Code, saved.Version, saved, actor, reason); err != nil {
			return err
		}
		if err := r.Emit(ctx, &instrumentv1.AssetUpserted{Asset: ToProtoAsset(saved, nil), Actor: actor, Reason: reason}, "asset", saved.Code); err != nil {
			return err
		}
	}
	for _, n := range a.Networks {
		n.AssetCode = a.Code
		if n.AddressFormat == "" {
			n.AddressFormat = domain.FormatEVM
		}
		if err := n.Validate(a.Asset); err != nil {
			return err
		}
		cur, err := r.Networks().Get(ctx, n.AssetCode, n.Network)
		if err != nil {
			return err
		}
		if cur != nil && cur.Same(n) {
			res.Unchanged++
			continue
		}
		saved, err := r.Networks().Save(ctx, n)
		if err != nil {
			return err
		}
		key := saved.AssetCode + "/" + saved.Network
		res.Changed = append(res.Changed, fmt.Sprintf("NETWORK %s v%d", key, saved.Version))
		if err := r.Record(ctx, "NETWORK", key, saved.Version, saved, actor, reason); err != nil {
			return err
		}
		if err := r.Emit(ctx, &instrumentv1.NetworkUpserted{Network: ToProtoNetwork(saved), Actor: actor, Reason: reason}, "asset", saved.AssetCode); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) applyPair(ctx context.Context, r ports.Repos, p domain.TradingPair, actor, reason string, res *ApplyResult) error {
	if p.Status == "" {
		p.Status = domain.StatusPrepare
	}
	if p.ReferenceMultiplier.IsZero() {
		p.ReferenceMultiplier = decimal.NewFromInt(1)
	}
	base, err := r.Assets().Get(ctx, p.BaseAsset)
	if err != nil {
		return err
	}
	quote, err := r.Assets().Get(ctx, p.QuoteAsset)
	if err != nil {
		return err
	}
	if base == nil || quote == nil {
		return apperr.Invalid(fmt.Sprintf("pair %s: unknown asset", p.Symbol))
	}
	if tier, err := r.FeeSchedules().Get(ctx, p.FeeTier); err != nil {
		return err
	} else if tier == nil {
		return apperr.Invalid(fmt.Sprintf("pair %s: unknown fee tier %q", p.Symbol, p.FeeTier))
	}
	cur, err := r.Pairs().GetForUpdate(ctx, p.Symbol)
	if err != nil {
		return err
	}
	if cur != nil {
		if p.ListedAt.IsZero() {
			p.ListedAt = cur.ListedAt
		}
		if cur.SameConfig(p) {
			res.Unchanged++
			return nil
		}
		p.Status = cur.Status // statuses change only through SetPairStatus
	}
	if err := p.Validate(*base, *quote); err != nil {
		return err
	}
	saved, err := r.Pairs().Save(ctx, p)
	if err != nil {
		return err
	}
	res.Changed = append(res.Changed, fmt.Sprintf("TRADING_PAIR %s v%d", saved.Symbol, saved.Version))
	if err := r.Record(ctx, "TRADING_PAIR", saved.Symbol, saved.Version, saved, actor, reason); err != nil {
		return err
	}
	view, err := s.pairView(ctx, r, saved)
	if err != nil {
		return err
	}
	return r.Emit(ctx, &instrumentv1.TradingPairUpserted{Pair: ToProtoPair(view), Actor: actor, Reason: reason}, "pair", saved.Symbol)
}

// SetPairStatus moves a pair along its status machine (appendix B).
func (s *Service) SetPairStatus(ctx context.Context, symbol, to, actor, reason string) (string, error) {
	if err := domain.ValidReason(reason); err != nil {
		return "", err
	}
	var from string
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		p, err := r.Pairs().GetForUpdate(ctx, symbol)
		if err != nil {
			return err
		}
		if p == nil {
			return domain.ErrNotFound
		}
		if err := domain.CheckPairTransition(p.Status, to); err != nil {
			return err
		}
		from = p.Status
		p.Status = to
		saved, err := r.Pairs().Save(ctx, *p)
		if err != nil {
			return err
		}
		if err := r.Record(ctx, "TRADING_PAIR", saved.Symbol, saved.Version, saved, actor, reason); err != nil {
			return err
		}
		return r.Emit(ctx, &instrumentv1.TradingPairStatusChanged{
			Symbol: symbol, FromStatus: from, ToStatus: to, Reason: reason, Actor: actor, Version: saved.Version,
		}, "pair", symbol)
	})
	return from, err
}

// AssetView is an asset with its networks.
type AssetView struct {
	domain.Asset
	Networks []domain.Network `json:"networks"`
}

// Assets lists every asset with its networks.
func (s *Service) Assets(ctx context.Context) ([]AssetView, error) {
	r := s.Store.Read()
	list, err := r.Assets().List(ctx)
	if err != nil {
		return nil, err
	}
	nets, err := r.Networks().List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AssetView, 0, len(list))
	for _, a := range list {
		v := AssetView{Asset: a, Networks: []domain.Network{}}
		for _, n := range nets {
			if n.AssetCode == a.Code {
				v.Networks = append(v.Networks, n)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// Asset returns one asset with its networks.
func (s *Service) Asset(ctx context.Context, code string) (AssetView, error) {
	list, err := s.Assets(ctx)
	if err != nil {
		return AssetView{}, err
	}
	for _, a := range list {
		if a.Code == code {
			return a, nil
		}
	}
	return AssetView{}, domain.ErrNotFound
}

// PairView is a pair with the rates of its fee tier and what the market
// lists show of its base asset.
type PairView struct {
	domain.TradingPair
	MakerFeeRate decimal.Decimal `json:"maker_fee_rate"`
	TakerFeeRate decimal.Decimal `json:"taker_fee_rate"`
	BaseName     string          `json:"base_name"`
	Rank         int32           `json:"rank"`
	Categories   []string        `json:"categories"`
}

func (s *Service) pairView(ctx context.Context, r ports.Repos, p domain.TradingPair) (PairView, error) {
	f, err := r.FeeSchedules().Get(ctx, p.FeeTier)
	if err != nil {
		return PairView{}, err
	}
	base, err := r.Assets().Get(ctx, p.BaseAsset)
	if err != nil {
		return PairView{}, err
	}
	v := PairView{TradingPair: p}
	if f != nil {
		v.MakerFeeRate, v.TakerFeeRate = f.MakerFeeRate, f.TakerFeeRate
	}
	if base != nil {
		v.BaseName, v.Rank, v.Categories = base.Name, base.Rank, base.Categories
	}
	return v, nil
}

// Pairs lists every pair with its fee rates.
func (s *Service) Pairs(ctx context.Context) ([]PairView, error) {
	r := s.Store.Read()
	list, err := r.Pairs().List(ctx)
	if err != nil {
		return nil, err
	}
	fees, err := r.FeeSchedules().List(ctx)
	if err != nil {
		return nil, err
	}
	assets, err := r.Assets().List(ctx)
	if err != nil {
		return nil, err
	}
	rates := map[string]domain.FeeSchedule{}
	for _, f := range fees {
		rates[f.Tier] = f
	}
	bases := map[string]domain.Asset{}
	for _, a := range assets {
		bases[a.Code] = a
	}
	out := make([]PairView, 0, len(list))
	for _, p := range list {
		base := bases[p.BaseAsset]
		out = append(out, PairView{
			TradingPair: p, MakerFeeRate: rates[p.FeeTier].MakerFeeRate, TakerFeeRate: rates[p.FeeTier].TakerFeeRate,
			BaseName: base.Name, Rank: base.Rank, Categories: base.Categories,
		})
	}
	return out, nil
}

// Pair returns one pair with its fee rates.
func (s *Service) Pair(ctx context.Context, symbol string) (PairView, error) {
	r := s.Store.Read()
	p, err := r.Pairs().Get(ctx, symbol)
	if err != nil {
		return PairView{}, err
	}
	if p == nil {
		return PairView{}, domain.ErrNotFound
	}
	return s.pairView(ctx, r, *p)
}
