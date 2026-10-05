package application

import (
	"context"
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
)

// AccountView is a margin account as the user sees it: what it holds and
// owes, valued, with its terms and state.
type AccountView struct {
	Account   domain.Account
	Status    domain.Status
	Terms     domain.Terms
	Holdings  []domain.Holding
	Valuation domain.Valuation
	// LiquidationPrice estimates an isolated account's base price at its
	// liquidation level; nil for the cross account and without debts.
	LiquidationPrice *decimal.Decimal
	UpdatedAt        time.Time
}

// Accounts returns the user's cross account (empty until the first
// transfer) and every isolated account that ever held something.
func (s *Service) Accounts(ctx context.Context, userID string) (AccountView, []AccountView, error) {
	r := s.Store.Read()
	cat, err := s.catalog(ctx, r)
	if err != nil {
		return AccountView{}, nil, err
	}
	states, err := r.Accounts().OfUser(ctx, userID)
	if err != nil {
		return AccountView{}, nil, err
	}
	all, err := s.Ledger.Holdings(ctx, userID)
	if err != nil {
		return AccountView{}, nil, err
	}
	prices := s.Prices.Prices()
	cross := s.view(ctx, cat, prices, ports.Account{UserID: userID, Account: domain.Cross(), Status: domain.StatusNormal}, all[domain.Cross()])
	var isolated []AccountView
	for _, st := range states {
		if st.Account.IsCross() {
			cross = s.view(ctx, cat, prices, st, all[st.Account])
			continue
		}
		isolated = append(isolated, s.view(ctx, cat, prices, st, all[st.Account]))
	}
	return cross, isolated, nil
}

// view values an account; an isolated pair dropped from the list keeps
// the default terms of its leverage for display.
func (s *Service) view(ctx context.Context, cat Catalog, prices domain.Prices, st ports.Account, holdings []domain.Holding) AccountView {
	terms, err := cat.Terms(st.Account)
	if err != nil {
		terms = domain.DefaultTerms(st.Account.Type, 3)
	}
	v := AccountView{
		Account: st.Account, Status: st.Status, Terms: terms, Holdings: sorted(holdings),
		Valuation: domain.Value(holdings, cat.Assets, prices), UpdatedAt: st.UpdatedAt,
	}
	if st.Account.IsCross() {
		return v
	}
	pair, ok := cat.Pairs[st.Account.Symbol]
	if !ok {
		return v
	}
	base, quote := holding(holdings, pair.Base), holding(holdings, pair.Quote)
	haircut := func(asset string) decimal.Decimal {
		if t, ok := cat.Assets[asset]; ok && t.Collateral {
			return t.Haircut
		}
		return decimal.Zero
	}
	tick := int32(8)
	if info, err := s.Instruments.Pair(ctx, pair.Symbol); err == nil {
		tick = info.TickDecimals
	}
	if p, ok := domain.LiquidationPrice(base, quote, haircut(pair.Base), haircut(pair.Quote), terms.LiquidationLevel, tick); ok {
		v.LiquidationPrice = &p
	}
	return v
}

func sorted(list []domain.Holding) []domain.Holding {
	out := slices.Clone(list)
	slices.SortFunc(out, func(a, b domain.Holding) int {
		switch {
		case a.Asset < b.Asset:
			return -1
		case a.Asset > b.Asset:
			return 1
		}
		return 0
	})
	return out
}

// Borrowable is how much of an asset an account may borrow now.
type Borrowable struct {
	Asset     string
	Amount    decimal.Decimal
	LimitedBy domain.Limit
}

// MaxBorrowable works out the room of an account in an asset (design §2).
func (s *Service) MaxBorrowable(ctx context.Context, userID string, a domain.Account, asset string) (Borrowable, error) {
	r := s.Store.Read()
	cat, err := s.catalog(ctx, r)
	if err != nil {
		return Borrowable{}, err
	}
	room, err := s.room(ctx, r, cat, userID, a, asset, nil)
	if err != nil {
		return Borrowable{}, err
	}
	amount, limit := domain.MaxBorrow(room)
	return Borrowable{Asset: asset, Amount: amount, LimitedBy: limit}, nil
}

// room gathers what MaxBorrow needs: the account valued (every asset of
// it priced, MARGIN_PRICE_UNAVAILABLE otherwise), its terms, the pool's
// lent amount (lent, or read when nil) and the user's principal in the
// asset.
func (s *Service) room(ctx context.Context, r ports.Repos, cat Catalog, userID string, a domain.Account, asset string,
	lent *decimal.Decimal,
) (domain.BorrowRoom, error) {
	terms, err := cat.Terms(a)
	if err != nil {
		return domain.BorrowRoom{}, err
	}
	t, err := cat.Asset(a, asset)
	if err != nil {
		return domain.BorrowRoom{}, err
	}
	all, err := s.Ledger.Holdings(ctx, userID)
	if err != nil {
		return domain.BorrowRoom{}, err
	}
	prices := s.Prices.Prices()
	v := domain.Value(all[a], cat.Assets, prices)
	if !v.Complete() {
		return domain.BorrowRoom{}, domain.ErrPriceUnavailable.WithDetail("asset", v.Unpriced[0])
	}
	price, ok := prices.Of(asset)
	if !ok {
		return domain.BorrowRoom{}, domain.ErrPriceUnavailable.WithDetail("asset", asset)
	}
	if lent == nil {
		pools, err := r.Pools().Lent(ctx)
		if err != nil {
			return domain.BorrowRoom{}, err
		}
		l := pools[asset]
		lent = &l
	}
	owed, err := r.Loans().UserOwed(ctx, userID, asset)
	if err != nil {
		return domain.BorrowRoom{}, err
	}
	return domain.BorrowRoom{
		Valuation: v, Terms: terms, Asset: t, Price: price.Value, PoolLeft: t.PoolCap.Sub(*lent), UserOwed: owed,
	}, nil
}
