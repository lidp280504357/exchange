package application

import (
	"context"
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/derivatives/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// TierImpact is what a contract's new risk ladder would do to the open
// positions at the mark prices, measured as the margin monitor measures
// them (the admin console's preview of a ladder change, design 2026-10-02
// §2 item 6). HOUSE's positions are left out: it is never liquidated.
type TierImpact struct {
	Symbol string
	// Positions counts the open positions on the contract.
	Positions int
	// Liquidated counts the positions the monitor would take over that it
	// does not now, with their notional at the marks; a cross account goes
	// whole, every cross position of it on any contract. Accounts counts
	// their users.
	Liquidated int
	Notional   decimal.Decimal
	Accounts   int
	// Warned counts the positions newly within 1.2 times their maintenance
	// margin (cross accounts count their positions on the contract).
	Warned int
	// OverLimit counts the positions above the risk limit of their
	// leverage under the new ladder: they stay, but cannot grow.
	OverLimit int
	// Unmeasured counts the positions left out for want of a fresh mark
	// price (one of a cross account's contracts without one leaves the
	// account out).
	Unmeasured int
	// Examples are the largest of the liquidated, at most impactExamples.
	Examples []ImpactExample
}

// ImpactExample is a position the new ladder would liquidate.
type ImpactExample struct {
	UserID       string
	Symbol       string
	PositionSide domain.PositionSide
	Cross        bool
	Notional     decimal.Decimal
	// MarginBalance is the isolated position's margin with its result, or
	// the cross account's equity; the maintenance margins are the
	// position's, or the cross account's, before and after.
	MarginBalance     decimal.Decimal
	MaintenanceBefore decimal.Decimal
	MaintenanceAfter  decimal.Decimal
}

const impactExamples = 20

// impactAccounts and impactBudget bound the cross accounts an impact
// measures one by one (a ledger call each) and for how long: the console
// waits 10 seconds. The accounts left are counted unmeasured, so the
// console does not confirm a partial measure (C5.5 ⑩). Variables for the
// tests.
var (
	impactAccounts = 2000
	impactBudget   = 8 * time.Second
)

// eachCross measures the cross accounts with positions on a contract
// (cross: the user's positions on it) within impactAccounts and
// impactBudget, returning the positions it could not measure.
func eachCross(ctx context.Context, cross map[string]int, measure func(context.Context, string) error) (int, error) {
	budget, cancel := context.WithTimeout(ctx, impactBudget)
	defer cancel()
	left, measured := 0, 0
	for user, n := range cross {
		if measured >= impactAccounts || budget.Err() != nil {
			left += n
			continue
		}
		measured++
		if err := measure(budget, user); err != nil {
			if budget.Err() != nil && ctx.Err() == nil {
				left += n
				continue
			}
			return 0, err
		}
	}
	return left, nil
}

// TierImpact measures a risk ladder for symbol against its open positions
// without changing anything; the ladder is checked as instrument-service
// checks it (domain.ValidTiers).
func (s *Service) TierImpact(ctx context.Context, symbol string, tiers []domain.RiskTier) (TierImpact, error) {
	if err := domain.ValidTiers(tiers); err != nil {
		return TierImpact{}, err
	}
	c, err := s.Instruments.Contract(ctx, symbol)
	if err != nil {
		return TierImpact{}, err
	}
	next := c
	next.Tiers = tiers
	out := TierImpact{Symbol: c.Symbol, Notional: decimal.Zero, Examples: []ImpactExample{}}
	open, err := s.Store.Read().Positions().Open(ctx, c.Symbol)
	if err != nil {
		return TierImpact{}, err
	}
	m, fresh := s.Marks.Mark(c.Symbol)
	users := map[string]bool{}
	cross := map[string]int{}
	for _, p := range open {
		if s.HouseUser != "" && p.UserID == s.HouseUser {
			continue
		}
		out.Positions++
		if !fresh || !m.Price.IsPositive() {
			out.Unmeasured++
			continue
		}
		notional := p.Notional(m.Price)
		if notional.GreaterThan(next.MaxNotional(p.Leverage)) {
			out.OverLimit++
		}
		if p.MarginMode == domain.Cross {
			cross[p.UserID]++
			continue
		}
		if p.Liquidating {
			continue
		}
		balance := p.MarginBalance(m.Price)
		before, after := p.MaintenanceMargin(c, m.Price), p.MaintenanceMargin(next, m.Price)
		was, will := domain.State(balance, before), domain.State(balance, after)
		switch {
		case will == domain.MarginLiquidate && was != domain.MarginLiquidate:
			out.Liquidated++
			out.Notional = out.Notional.Add(notional)
			users[p.UserID] = true
			out.Examples = append(out.Examples, ImpactExample{
				UserID: p.UserID, Symbol: p.Symbol, PositionSide: p.Side, Notional: notional, MarginBalance: balance,
				MaintenanceBefore: before, MaintenanceAfter: after,
			})
		case will == domain.MarginWarning && was == domain.MarginHealthy:
			out.Warned++
		}
	}
	left, err := eachCross(ctx, cross, func(ctx context.Context, user string) error {
		return s.crossImpact(ctx, user, c, next, &out, users)
	})
	if err != nil {
		return TierImpact{}, err
	}
	out.Unmeasured += left
	out.Accounts = len(users)
	slices.SortFunc(out.Examples, func(a, b ImpactExample) int { return b.Notional.Cmp(a.Notional) })
	if len(out.Examples) > impactExamples {
		out.Examples = out.Examples[:impactExamples]
	}
	return out, nil
}

// crossImpact measures a user's cross account with c's ladder replaced by
// next's, the other contracts as they are.
func (s *Service) crossImpact(ctx context.Context, userID string, c, next domain.Contract, out *TierImpact, users map[string]bool) error {
	all, err := s.Store.Read().Positions().OfUser(ctx, userID, "")
	if err != nil {
		return err
	}
	var positions []domain.Position
	onContract := 0
	for _, p := range all {
		if p.Flat() || p.MarginMode != domain.Cross {
			continue
		}
		if p.Liquidating {
			return nil // already taken over
		}
		if p.Symbol == c.Symbol {
			onContract++
		}
		positions = append(positions, p)
	}
	if onContract == 0 {
		return nil
	}
	before, after := map[string]domain.Contract{}, map[string]domain.Contract{}
	marks := map[string]decimal.Decimal{}
	for _, p := range positions {
		if _, ok := before[p.Symbol]; ok {
			continue
		}
		pc := c
		if p.Symbol != c.Symbol {
			if pc, err = s.Instruments.Contract(ctx, p.Symbol); err != nil {
				return err
			}
		}
		m, fresh := s.Marks.Mark(p.Symbol)
		if !fresh || !m.Price.IsPositive() {
			out.Unmeasured += onContract
			return nil
		}
		before[p.Symbol], after[p.Symbol], marks[p.Symbol] = pc, pc, m.Price
		if p.Symbol == c.Symbol {
			after[p.Symbol] = next
		}
	}
	bal, err := s.Ledger.Balance(ctx, userID, c.Quote)
	if err != nil {
		return err
	}
	orders, err := s.Store.Read().Orders().Unreleased(ctx, userID)
	if err != nil {
		return err
	}
	equity, was := domain.CrossEquity(bal.Available, orders, positions, before, marks)
	_, will := domain.CrossEquity(bal.Available, orders, positions, after, marks)
	switch stateWas, stateWill := domain.State(equity, was), domain.State(equity, will); {
	case stateWill == domain.MarginLiquidate && stateWas != domain.MarginLiquidate:
		users[userID] = true
		for _, p := range positions {
			notional := p.Notional(marks[p.Symbol])
			out.Liquidated++
			out.Notional = out.Notional.Add(notional)
			out.Examples = append(out.Examples, ImpactExample{
				UserID: userID, Symbol: p.Symbol, PositionSide: p.Side, Cross: true, Notional: notional, MarginBalance: equity,
				MaintenanceBefore: was, MaintenanceAfter: will,
			})
		}
	case stateWill == domain.MarginWarning && stateWas == domain.MarginHealthy:
		out.Warned += onContract
	}
	return nil
}

// PriceImpact is what a contract's mark price at Target would do to the
// open positions (the admin console's confirmation of a simulated-market
// price event, ASTRA design §6.3), measured as the margin monitor measures
// them and as TierImpact does with the ladder: the positions it would then
// take over that it does not now, their notional at the target, their
// users, and what the insurance fund would bear if they were closed at
// the target (what an isolated position's margin, or a cross account's
// equity, falls short of zero). HOUSE's positions are left out.
type PriceImpact struct {
	Symbol        string
	Target        decimal.Decimal
	Positions     int
	Liquidated    int
	Notional      decimal.Decimal
	Accounts      int
	InsuranceCost decimal.Decimal
	// Unmeasured counts the positions left out for want of a fresh mark
	// price now (a cross account's contracts included).
	Unmeasured int
	// Examples are the largest of the liquidated, at most impactExamples:
	// the margin balance is at the target, the maintenance margins at the
	// mark now and at the target.
	Examples []ImpactExample
}

// PriceImpact measures a mark price of target for symbol against its open
// positions without changing anything.
func (s *Service) PriceImpact(ctx context.Context, symbol string, target decimal.Decimal) (PriceImpact, error) {
	if !target.IsPositive() {
		return PriceImpact{}, apperr.Invalid("the target price must be positive")
	}
	c, err := s.Instruments.Contract(ctx, symbol)
	if err != nil {
		return PriceImpact{}, err
	}
	out := PriceImpact{Symbol: c.Symbol, Target: target, Notional: decimal.Zero, InsuranceCost: decimal.Zero, Examples: []ImpactExample{}}
	open, err := s.Store.Read().Positions().Open(ctx, c.Symbol)
	if err != nil {
		return PriceImpact{}, err
	}
	m, fresh := s.Marks.Mark(c.Symbol)
	users := map[string]bool{}
	cross := map[string]int{}
	for _, p := range open {
		if s.HouseUser != "" && p.UserID == s.HouseUser {
			continue
		}
		out.Positions++
		if !fresh || !m.Price.IsPositive() {
			out.Unmeasured++
			continue
		}
		if p.MarginMode == domain.Cross {
			cross[p.UserID]++
			continue
		}
		if p.Liquidating {
			continue
		}
		before := p.MaintenanceMargin(c, m.Price)
		balance, after := p.MarginBalance(target), p.MaintenanceMargin(c, target)
		if domain.State(balance, after) != domain.MarginLiquidate || domain.State(p.MarginBalance(m.Price), before) == domain.MarginLiquidate {
			continue
		}
		notional := p.Notional(target)
		out.Liquidated++
		out.Notional = out.Notional.Add(notional)
		out.InsuranceCost = out.InsuranceCost.Add(decimal.Max(balance.Neg(), decimal.Zero))
		users[p.UserID] = true
		out.Examples = append(out.Examples, ImpactExample{
			UserID: p.UserID, Symbol: p.Symbol, PositionSide: p.Side, Notional: notional, MarginBalance: balance,
			MaintenanceBefore: before, MaintenanceAfter: after,
		})
	}
	left, err := eachCross(ctx, cross, func(ctx context.Context, user string) error {
		return s.crossPriceImpact(ctx, user, c, target, &out, users)
	})
	if err != nil {
		return PriceImpact{}, err
	}
	out.Unmeasured += left
	out.Accounts = len(users)
	slices.SortFunc(out.Examples, func(a, b ImpactExample) int { return b.Notional.Cmp(a.Notional) })
	if len(out.Examples) > impactExamples {
		out.Examples = out.Examples[:impactExamples]
	}
	return out, nil
}

// crossPriceImpact measures a user's cross account with c's mark at
// target, the other contracts at their marks: taken over, it goes whole.
func (s *Service) crossPriceImpact(ctx context.Context, userID string, c domain.Contract, target decimal.Decimal, out *PriceImpact,
	users map[string]bool,
) error {
	all, err := s.Store.Read().Positions().OfUser(ctx, userID, "")
	if err != nil {
		return err
	}
	var positions []domain.Position
	onContract := 0
	for _, p := range all {
		if p.Flat() || p.MarginMode != domain.Cross {
			continue
		}
		if p.Liquidating {
			return nil // already taken over
		}
		if p.Symbol == c.Symbol {
			onContract++
		}
		positions = append(positions, p)
	}
	if onContract == 0 {
		return nil
	}
	contracts := map[string]domain.Contract{}
	now, then := map[string]decimal.Decimal{}, map[string]decimal.Decimal{}
	for _, p := range positions {
		if _, ok := contracts[p.Symbol]; ok {
			continue
		}
		pc := c
		if p.Symbol != c.Symbol {
			if pc, err = s.Instruments.Contract(ctx, p.Symbol); err != nil {
				return err
			}
		}
		m, fresh := s.Marks.Mark(p.Symbol)
		if !fresh || !m.Price.IsPositive() {
			out.Unmeasured += onContract
			return nil
		}
		contracts[p.Symbol], now[p.Symbol], then[p.Symbol] = pc, m.Price, m.Price
	}
	then[c.Symbol] = target
	bal, err := s.Ledger.Balance(ctx, userID, c.Quote)
	if err != nil {
		return err
	}
	orders, err := s.Store.Read().Orders().Unreleased(ctx, userID)
	if err != nil {
		return err
	}
	equityNow, before := domain.CrossEquity(bal.Available, orders, positions, contracts, now)
	equity, after := domain.CrossEquity(bal.Available, orders, positions, contracts, then)
	if domain.State(equity, after) != domain.MarginLiquidate || domain.State(equityNow, before) == domain.MarginLiquidate {
		return nil
	}
	users[userID] = true
	out.InsuranceCost = out.InsuranceCost.Add(decimal.Max(equity.Neg(), decimal.Zero))
	for _, p := range positions {
		notional := p.Notional(then[p.Symbol])
		out.Liquidated++
		out.Notional = out.Notional.Add(notional)
		out.Examples = append(out.Examples, ImpactExample{
			UserID: userID, Symbol: p.Symbol, PositionSide: p.Side, Cross: true, Notional: notional, MarginBalance: equity,
			MaintenanceBefore: before, MaintenanceAfter: after,
		})
	}
	return nil
}
