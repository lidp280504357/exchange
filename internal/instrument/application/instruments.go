// Package application holds instrument-service's use cases.
package application

import (
	"context"
	"errors"
	"fmt"
	"time"

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

// Export returns the stored reference data as a Config, in the shape of
// deploy/instruments/<env>.json (statuses and versions included): what
// the admin console edits and applies back.
func (s *Service) Export(ctx context.Context) (Config, error) {
	r := s.Store.Read()
	fees, err := r.FeeSchedules().List(ctx)
	if err != nil {
		return Config{}, err
	}
	assets, err := r.Assets().List(ctx)
	if err != nil {
		return Config{}, err
	}
	nets, err := r.Networks().List(ctx)
	if err != nil {
		return Config{}, err
	}
	pairs, err := r.Pairs().List(ctx)
	if err != nil {
		return Config{}, err
	}
	contracts, err := r.Contracts().List(ctx)
	if err != nil {
		return Config{}, err
	}
	out := Config{FeeSchedules: fees, Assets: make([]AssetConfig, 0, len(assets)), Pairs: pairs, Contracts: contracts}
	for _, a := range assets {
		ac := AssetConfig{Asset: a, Networks: []domain.Network{}}
		for _, n := range nets {
			if n.AssetCode == a.Code {
				ac.Networks = append(ac.Networks, n)
			}
		}
		out.Assets = append(out.Assets, ac)
	}
	return out, nil
}

// Where a change of the reference data came from (config_history.source).
const (
	// SourceFile is exchangectl instruments apply: the deploy's sync of
	// deploy/instruments/<env>.json.
	SourceFile = "FILE"
	// SourceConsole is the admin console's edits.
	SourceConsole = "CONSOLE"
	// SourceStatus is a status change (SetPairStatus, SetContractStatus).
	SourceStatus = "STATUS"
	// SourceProfile is an asset profile change.
	SourceProfile = "PROFILE"
)

// What an apply does to an item.
const (
	ActionCreate = "CREATE"
	ActionUpdate = "UPDATE"
	// ActionKeep is an item a file apply leaves as the console changed it.
	ActionKeep = "KEEP"
)

// ApplyOptions say who applies the reference data, from where and how.
type ApplyOptions struct {
	Actor  string
	Reason string
	// Source is SourceFile or SourceConsole.
	Source string
	// DryRun works the changes out without making them.
	DryRun bool
	// Force lets a file apply change the items the console changed last.
	Force bool
}

// Change is an item an apply creates, updates or keeps: the item before
// (nil when created) and after, at its version after the apply.
type Change struct {
	Entity  string `json:"entity"`
	Key     string `json:"key"`
	Action  string `json:"action"`
	Version int64  `json:"version"`
	Before  any    `json:"before"`
	After   any    `json:"after"`
	// KeptBy and KeptAt say who changed a kept item in the console, and
	// when.
	KeptBy string    `json:"kept_by,omitempty"`
	KeptAt time.Time `json:"kept_at,omitzero"`
}

// ApplyResult lists what an apply did: Changed as "ENTITY key vN", the
// same in Changes with the items before and after, and Kept, the items a
// file apply left as the console changed them.
type ApplyResult struct {
	Changed   []string
	Changes   []Change
	Kept      []Change
	Unchanged int
}

func (res *ApplyResult) changed(entity, key string, before, after any, version int64, created bool) {
	action := ActionUpdate
	if created {
		action = ActionCreate
		before = nil
	}
	res.Changed = append(res.Changed, fmt.Sprintf("%s %s v%d", entity, key, version))
	res.Changes = append(res.Changes, Change{Entity: entity, Key: key, Action: action, Version: version, Before: before, After: after})
}

// errDryRun rolls a dry run's transaction back.
var errDryRun = errors.New("dry run")

// Apply makes the stored reference data match cfg in one transaction, as
// exchangectl instruments apply does (source FILE).
func (s *Service) Apply(ctx context.Context, cfg Config, actor, reason string) (ApplyResult, error) {
	return s.ApplyWith(ctx, cfg, ApplyOptions{Actor: actor, Reason: reason, Source: SourceFile})
}

// ApplyWith makes the stored reference data match cfg in one transaction:
// missing items are created, changed ones get a new version, a history
// row and an event, and identical ones are left alone, so applying the
// same file twice changes nothing. A pair's status is set only when the
// pair is created; later changes go through SetPairStatus. Items missing
// from cfg are kept. A file apply leaves an item the admin console changed
// last as it is (KEEP) unless forced: console edits survive deploys. A
// dry run reports the same without changing anything.
func (s *Service) ApplyWith(ctx context.Context, cfg Config, o ApplyOptions) (ApplyResult, error) {
	if err := domain.ValidReason(o.Reason); err != nil {
		return ApplyResult{}, err
	}
	if o.Source != SourceFile && o.Source != SourceConsole {
		return ApplyResult{}, fmt.Errorf("apply from unknown source %q", o.Source)
	}
	var res ApplyResult
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		res = ApplyResult{}
		for _, f := range cfg.FeeSchedules {
			if err := s.applyFee(ctx, r, f, o, &res); err != nil {
				return err
			}
		}
		for _, a := range cfg.Assets {
			if err := s.applyAsset(ctx, r, a, o, &res); err != nil {
				return err
			}
		}
		for _, p := range cfg.Pairs {
			if err := s.applyPair(ctx, r, p, o, &res); err != nil {
				return err
			}
		}
		for _, c := range cfg.Contracts {
			if err := s.applyContract(ctx, r, c, o, &res); err != nil {
				return err
			}
		}
		if o.DryRun {
			return errDryRun
		}
		return nil
	})
	if errors.Is(err, errDryRun) {
		err = nil
	}
	return res, err
}

// kept reports whether a file apply leaves an existing item (cur, at its
// version) as the console changed it last, unless forced, noting it.
func kept(ctx context.Context, r ports.Repos, o ApplyOptions, entity, key string, version int64, cur any, res *ApplyResult) (bool, error) {
	if o.Source != SourceFile || o.Force {
		return false, nil
	}
	last, err := r.LastEdit(ctx, entity, key)
	if err != nil || last == nil || last.Source != SourceConsole {
		return false, err
	}
	res.Kept = append(res.Kept, Change{
		Entity: entity, Key: key, Action: ActionKeep, Version: version, Before: cur, After: cur, KeptBy: last.Actor, KeptAt: last.At,
	})
	return true, nil
}

func (s *Service) applyFee(ctx context.Context, r ports.Repos, f domain.FeeSchedule, o ApplyOptions, res *ApplyResult) error {
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
	if cur != nil {
		if keep, err := kept(ctx, r, o, "FEE_SCHEDULE", f.Tier, cur.Version, *cur, res); err != nil || keep {
			return err
		}
	}
	saved, err := r.FeeSchedules().Save(ctx, f)
	if err != nil {
		return err
	}
	res.changed("FEE_SCHEDULE", saved.Tier, deref(cur), saved, saved.Version, cur == nil)
	if err := r.Record(ctx, "FEE_SCHEDULE", saved.Tier, saved.Version, saved, o.Actor, o.Reason, o.Source); err != nil {
		return err
	}
	return r.Emit(ctx, &instrumentv1.FeeScheduleChanged{Schedule: ToProtoFee(saved), Actor: o.Actor, Reason: o.Reason}, "fee_tier", saved.Tier)
}

// deref is the item an apply found, nil when there was none.
func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

func (s *Service) applyAsset(ctx context.Context, r ports.Repos, a AssetConfig, o ApplyOptions, res *ApplyResult) error {
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
	keep := false
	if cur != nil && !cur.Same(a.Asset) {
		if keep, err = kept(ctx, r, o, "ASSET", a.Code, cur.Version, *cur, res); err != nil {
			return err
		}
	}
	switch {
	case cur != nil && cur.Same(a.Asset):
		res.Unchanged++
	case keep:
	default:
		saved, err := r.Assets().Save(ctx, a.Asset)
		if err != nil {
			return err
		}
		res.changed("ASSET", saved.Code, deref(cur), saved, saved.Version, cur == nil)
		if err := r.Record(ctx, "ASSET", saved.Code, saved.Version, saved, o.Actor, o.Reason, o.Source); err != nil {
			return err
		}
		if err := r.Emit(ctx, &instrumentv1.AssetUpserted{Asset: ToProtoAsset(saved, nil), Actor: o.Actor, Reason: o.Reason}, "asset", saved.Code); err != nil {
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
		key := n.AssetCode + "/" + n.Network
		if cur != nil {
			if keep, err := kept(ctx, r, o, "NETWORK", key, cur.Version, *cur, res); err != nil {
				return err
			} else if keep {
				continue
			}
		}
		saved, err := r.Networks().Save(ctx, n)
		if err != nil {
			return err
		}
		res.changed("NETWORK", key, deref(cur), saved, saved.Version, cur == nil)
		if err := r.Record(ctx, "NETWORK", key, saved.Version, saved, o.Actor, o.Reason, o.Source); err != nil {
			return err
		}
		if err := r.Emit(ctx, &instrumentv1.NetworkUpserted{Network: ToProtoNetwork(saved), Actor: o.Actor, Reason: o.Reason}, "asset", saved.AssetCode); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) applyPair(ctx context.Context, r ports.Repos, p domain.TradingPair, o ApplyOptions, res *ApplyResult) error {
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
	if cur != nil {
		if keep, err := kept(ctx, r, o, "TRADING_PAIR", p.Symbol, cur.Version, *cur, res); err != nil || keep {
			return err
		}
	}
	saved, err := r.Pairs().Save(ctx, p)
	if err != nil {
		return err
	}
	res.changed("TRADING_PAIR", saved.Symbol, deref(cur), saved, saved.Version, cur == nil)
	if err := r.Record(ctx, "TRADING_PAIR", saved.Symbol, saved.Version, saved, o.Actor, o.Reason, o.Source); err != nil {
		return err
	}
	view, err := s.pairView(ctx, r, saved)
	if err != nil {
		return err
	}
	return r.Emit(ctx, &instrumentv1.TradingPairUpserted{Pair: ToProtoPair(view), Actor: o.Actor, Reason: o.Reason}, "pair", saved.Symbol)
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
		if err := r.Record(ctx, "TRADING_PAIR", saved.Symbol, saved.Version, saved, actor, reason, SourceStatus); err != nil {
			return err
		}
		return r.Emit(ctx, &instrumentv1.TradingPairStatusChanged{
			Symbol: symbol, FromStatus: from, ToStatus: to, Reason: reason, Actor: actor, Version: saved.Version,
		}, "pair", symbol)
	})
	return from, err
}

// AssetView is an asset with its networks and its profile.
type AssetView struct {
	domain.Asset
	Networks []domain.Network    `json:"networks"`
	Profile  domain.AssetProfile `json:"profile"`
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
	profiles, err := s.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AssetView, 0, len(list))
	for _, a := range list {
		v := AssetView{Asset: a, Networks: []domain.Network{}, Profile: profiles[a.Code]}
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
// lists show of its base asset (its profile too).
type PairView struct {
	domain.TradingPair
	MakerFeeRate decimal.Decimal     `json:"maker_fee_rate"`
	TakerFeeRate decimal.Decimal     `json:"taker_fee_rate"`
	BaseName     string              `json:"base_name"`
	Rank         int32               `json:"rank"`
	Categories   []string            `json:"categories"`
	BaseProfile  domain.AssetProfile `json:"base_profile"`
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
	profiles, err := s.Profiles(ctx)
	if err != nil {
		return PairView{}, err
	}
	v.BaseProfile = profiles[p.BaseAsset]
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
	profiles, err := s.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PairView, 0, len(list))
	for _, p := range list {
		base := bases[p.BaseAsset]
		out = append(out, PairView{
			TradingPair: p, MakerFeeRate: rates[p.FeeTier].MakerFeeRate, TakerFeeRate: rates[p.FeeTier].TakerFeeRate,
			BaseName: base.Name, Rank: base.Rank, Categories: base.Categories, BaseProfile: profiles[p.BaseAsset],
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
