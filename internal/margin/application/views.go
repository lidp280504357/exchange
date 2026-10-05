package application

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// AssetView is an asset of the margin list with its pool and this hour's
// rate.
type AssetView struct {
	Terms         domain.AssetTerms
	PoolAvailable decimal.Decimal
	// Utilization is what is lent over the pool's cap, 0 to 1.
	Utilization decimal.Decimal
	Rate        decimal.Decimal
	RateHour    time.Time
}

// Assets lists the margin list's assets (public).
func (s *Service) Assets(ctx context.Context) ([]AssetView, error) {
	r := s.Store.Read()
	cat, err := s.catalog(ctx, r)
	if err != nil {
		return nil, err
	}
	lent, err := r.Pools().Lent(ctx)
	if err != nil {
		return nil, err
	}
	hour := domain.Hour(s.Now())
	out := make([]AssetView, 0, len(cat.Assets))
	for _, t := range sortedAssets(cat) {
		rate, err := s.currentRate(ctx, r, t, hour, lent[t.Asset])
		if err != nil {
			return nil, err
		}
		out = append(out, AssetView{
			Terms: t, PoolAvailable: decimal.Max(t.PoolCap.Sub(lent[t.Asset]), decimal.Zero),
			Utilization: domain.Use(lent[t.Asset], t.PoolCap).Round(6), Rate: rate, RateHour: hour,
		})
	}
	return out, nil
}

// currentRate is the asset's rate of the hour: the one set for it, or
// what the pool's use gives now (reading never sets a rate).
func (s *Service) currentRate(ctx context.Context, r ports.Repos, t domain.AssetTerms, hour time.Time, lent decimal.Decimal) (decimal.Decimal, error) {
	if got, ok, err := r.Rates().Get(ctx, t.Asset, hour); err != nil || ok {
		return got.Rate, err
	}
	return t.HourlyRate(lent), nil
}

func sortedAssets(cat Catalog) []domain.AssetTerms {
	out := slices.Collect(maps.Values(cat.Assets))
	slices.SortFunc(out, func(a, b domain.AssetTerms) int { return strings.Compare(a.Asset, b.Asset) })
	return out
}

// Pairs returns the cross account's terms and the pairs' isolated terms
// (public).
func (s *Service) Pairs(ctx context.Context) (domain.Terms, []domain.Pair, error) {
	r := s.Store.Read()
	pairs, err := r.Terms().Pairs(ctx)
	if err != nil {
		return domain.Terms{}, nil, err
	}
	cross, ok, err := r.Terms().Cross(ctx)
	if err != nil {
		return domain.Terms{}, nil, err
	}
	if !ok {
		cross = domain.DefaultTerms(domain.AccountCross, 3)
	}
	return cross, pairs, nil
}

// LoanView is an open loan with the asset's model and this hour's rate.
type LoanView struct {
	Loan  ports.Loan
	Model domain.InterestModel
	Rate  decimal.Decimal
}

// Loans lists the user's open loans, of one account when a is set.
func (s *Service) Loans(ctx context.Context, userID string, a *domain.Account) ([]LoanView, error) {
	r := s.Store.Read()
	loans, err := r.Loans().OfUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	cat, err := s.catalog(ctx, r)
	if err != nil {
		return nil, err
	}
	lent, err := r.Pools().Lent(ctx)
	if err != nil {
		return nil, err
	}
	hour := domain.Hour(s.Now())
	out := []LoanView{}
	for _, l := range loans {
		if a != nil && l.Account != *a {
			continue
		}
		v := LoanView{Loan: l, Model: domain.InterestFixed, Rate: decimal.Zero}
		if t, ok := cat.Assets[l.Asset]; ok {
			if v.Rate, err = s.currentRate(ctx, r, t, hour, lent[l.Asset]); err != nil {
				return nil, err
			}
			v.Model = t.Model
		}
		out = append(out, v)
	}
	return out, nil
}

// Loan returns one loan as it stands with its model and rate.
func (s *Service) Loan(ctx context.Context, l ports.Loan) (LoanView, error) {
	r := s.Store.Read()
	v := LoanView{Loan: l, Model: domain.InterestFixed, Rate: decimal.Zero}
	t, ok, err := r.Terms().Asset(ctx, l.Asset)
	if err != nil || !ok {
		return v, err
	}
	lent, err := r.Pools().Lent(ctx)
	if err != nil {
		return v, err
	}
	v.Model = t.Model
	v.Rate, err = s.currentRate(ctx, r, t, domain.Hour(s.Now()), lent[l.Asset])
	return v, err
}

// Interest returns a page of the user's interest charges, newest first,
// and the cursor of the next page ("" on the last).
func (s *Service) Interest(ctx context.Context, userID string, a *domain.Account, asset, cursor string, limit int) ([]ports.Charge, string, error) {
	if cursor != "" {
		if _, err := uuid.Parse(cursor); err != nil {
			return nil, "", apperr.Invalid("bad cursor")
		}
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	list, err := s.Store.Read().Interest().OfUser(ctx, userID, a, asset, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	if len(list) <= limit {
		return list, "", nil
	}
	list = list[:limit]
	return list, list[limit-1].ID, nil
}
