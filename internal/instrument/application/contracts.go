package application

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	instrumentv1 "github.com/skill/exchange/api/gen/go/exchange/instrument/v1"
	"github.com/skill/exchange/internal/instrument/domain"
	"github.com/skill/exchange/internal/instrument/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// ContractView is a contract with the rates of its fee tier.
type ContractView struct {
	domain.Contract
	MakerFeeRate decimal.Decimal `json:"maker_fee_rate"`
	TakerFeeRate decimal.Decimal `json:"taker_fee_rate"`
}

func (s *Service) applyContract(ctx context.Context, r ports.Repos, c domain.Contract, o ApplyOptions, res *ApplyResult) error {
	if c.Status == "" {
		c.Status = domain.StatusPrepare
	}
	if c.Type == "" {
		c.Type = domain.ContractPerpetual
	}
	base, err := r.Assets().Get(ctx, c.BaseAsset)
	if err != nil {
		return err
	}
	quote, err := r.Assets().Get(ctx, c.QuoteAsset)
	if err != nil {
		return err
	}
	if base == nil || quote == nil {
		return apperr.Invalid(fmt.Sprintf("contract %s: unknown asset", c.Symbol))
	}
	if tier, err := r.FeeSchedules().Get(ctx, c.FeeTier); err != nil {
		return err
	} else if tier == nil {
		return apperr.Invalid(fmt.Sprintf("contract %s: unknown fee tier %q", c.Symbol, c.FeeTier))
	}
	cur, err := r.Contracts().GetForUpdate(ctx, c.Symbol)
	if err != nil {
		return err
	}
	if cur != nil {
		if cur.SameConfig(c) {
			res.Unchanged++
			return nil
		}
		c.Status = cur.Status // statuses change only through SetContractStatus
	}
	if err := c.Validate(*base, *quote); err != nil {
		return err
	}
	if cur != nil {
		if keep, err := kept(ctx, r, o, "CONTRACT", c.Symbol, cur.Version, *cur, res); err != nil || keep {
			return err
		}
	}
	saved, err := r.Contracts().Save(ctx, c)
	if err != nil {
		return err
	}
	res.changed("CONTRACT", saved.Symbol, deref(cur), saved, saved.Version, cur == nil)
	if err := r.Record(ctx, "CONTRACT", saved.Symbol, saved.Version, saved, o.Actor, o.Reason, o.Source); err != nil {
		return err
	}
	view, err := s.contractView(ctx, r, saved)
	if err != nil {
		return err
	}
	return r.Emit(ctx, &instrumentv1.ContractUpserted{Contract: ToProtoContract(view), Actor: o.Actor, Reason: o.Reason}, "contract", saved.Symbol)
}

// SetContractStatus moves a contract along the pair status machine.
func (s *Service) SetContractStatus(ctx context.Context, symbol, to, actor, reason string) (string, error) {
	if err := domain.ValidReason(reason); err != nil {
		return "", err
	}
	var from string
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		c, err := r.Contracts().GetForUpdate(ctx, symbol)
		if err != nil {
			return err
		}
		if c == nil {
			return domain.ErrNotFound
		}
		if err := domain.CheckPairTransition(c.Status, to); err != nil {
			return err
		}
		from = c.Status
		c.Status = to
		saved, err := r.Contracts().Save(ctx, *c)
		if err != nil {
			return err
		}
		if err := r.Record(ctx, "CONTRACT", saved.Symbol, saved.Version, saved, actor, reason, SourceStatus); err != nil {
			return err
		}
		return r.Emit(ctx, &instrumentv1.ContractStatusChanged{
			Symbol: symbol, FromStatus: from, ToStatus: to, Reason: reason, Actor: actor, Version: saved.Version,
		}, "contract", symbol)
	})
	return from, err
}

func (s *Service) contractView(ctx context.Context, r ports.Repos, c domain.Contract) (ContractView, error) {
	f, err := r.FeeSchedules().Get(ctx, c.FeeTier)
	if err != nil {
		return ContractView{}, err
	}
	v := ContractView{Contract: c}
	if f != nil {
		v.MakerFeeRate, v.TakerFeeRate = f.MakerFeeRate, f.TakerFeeRate
	}
	return v, nil
}

// Contracts lists every contract with its fee rates.
func (s *Service) Contracts(ctx context.Context) ([]ContractView, error) {
	r := s.Store.Read()
	list, err := r.Contracts().List(ctx)
	if err != nil {
		return nil, err
	}
	fees, err := r.FeeSchedules().List(ctx)
	if err != nil {
		return nil, err
	}
	rates := map[string]domain.FeeSchedule{}
	for _, f := range fees {
		rates[f.Tier] = f
	}
	out := make([]ContractView, 0, len(list))
	for _, c := range list {
		out = append(out, ContractView{Contract: c, MakerFeeRate: rates[c.FeeTier].MakerFeeRate, TakerFeeRate: rates[c.FeeTier].TakerFeeRate})
	}
	return out, nil
}

// Contract returns one contract with its fee rates.
func (s *Service) Contract(ctx context.Context, symbol string) (ContractView, error) {
	r := s.Store.Read()
	c, err := r.Contracts().Get(ctx, symbol)
	if err != nil {
		return ContractView{}, err
	}
	if c == nil {
		return ContractView{}, domain.ErrNotFound
	}
	return s.contractView(ctx, r, *c)
}

// ToProtoContract converts a contract.
func ToProtoContract(c ContractView) *instrumentv1.Contract {
	tiers := make([]*instrumentv1.RiskTier, 0, len(c.RiskTiers))
	for _, t := range c.RiskTiers {
		tiers = append(tiers, &instrumentv1.RiskTier{MaxNotional: t.MaxNotional.String(), MaxLeverage: t.MaxLeverage, Mmr: t.MMR.String()})
	}
	return &instrumentv1.Contract{
		Symbol: c.Symbol, Type: c.Type, BaseAsset: c.BaseAsset, QuoteAsset: c.QuoteAsset, IndexSymbol: c.IndexSymbol,
		TickSize: c.TickSize.String(), LotSize: c.LotSize.String(), MinQuantity: c.MinQuantity.String(),
		MaxQuantity: c.MaxQuantity.String(), MinNotional: c.MinNotional.String(), PriceBand: c.PriceBand.String(), RiskTiers: tiers,
		FundingIntervalHours: c.FundingIntervalHours, InterestRate: c.InterestRate.String(), FundingCap: c.FundingCap.String(),
		ImpactNotional: c.ImpactNotional.String(), FeeTier: c.FeeTier, MakerFeeRate: c.MakerFeeRate.String(),
		TakerFeeRate: c.TakerFeeRate.String(), Status: c.Status, Version: c.Version,
	}
}
