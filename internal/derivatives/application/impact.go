package application

import (
	"context"
	"slices"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/derivatives/domain"
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
	cross := map[string]bool{}
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
			cross[p.UserID] = true
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
	for user := range cross {
		if err := s.crossImpact(ctx, user, c, next, &out, users); err != nil {
			return TierImpact{}, err
		}
	}
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
