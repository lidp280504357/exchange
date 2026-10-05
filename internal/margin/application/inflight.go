package application

import (
	"context"
	"slices"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
)

// inFlight is a user's writes on their way to the ledger: recorded
// PENDING, booked there or not yet.
type inFlight struct {
	borrows   []ports.Borrow
	transfers []ports.Transfer
	charges   []ports.Charge
}

// borrowing is what the user's PENDING borrows of asset add up to, in all
// accounts.
func (f inFlight) borrowing(asset string) decimal.Decimal {
	sum := decimal.Zero
	for _, b := range f.borrows {
		if b.Asset == asset {
			sum = sum.Add(b.Amount)
		}
	}
	return sum
}

// count returns the holdings with the writes counted the cautious way: a
// borrow as lent already, its first hour owed; a transfer out as gone; an
// hourly charge as owed. What would add to an account, a transfer in or a
// repayment, counts once the ledger has it.
func (f inFlight) count(all map[domain.Account][]domain.Holding) map[domain.Account][]domain.Holding {
	out := make(map[domain.Account][]domain.Holding, len(all))
	for a, list := range all {
		out[a] = slices.Clone(list)
	}
	change := func(a domain.Account, asset string, fn func(*domain.Holding)) {
		list := out[a]
		i := slices.IndexFunc(list, func(h domain.Holding) bool { return h.Asset == asset })
		if i < 0 {
			list, i = append(list, domain.Holding{Asset: asset}), len(list)
			out[a] = list
		}
		fn(&list[i])
	}
	for _, b := range f.borrows {
		change(b.Account, b.Asset, func(h *domain.Holding) {
			h.Free, h.Borrowed, h.Interest = h.Free.Add(b.Amount), h.Borrowed.Add(b.Amount), h.Interest.Add(b.FirstInterest)
		})
	}
	for _, t := range f.transfers {
		if t.Direction == domain.DirectionOut {
			change(t.Account, t.Asset, func(h *domain.Holding) { h.Free = h.Free.Sub(t.Amount) })
		}
	}
	for _, c := range f.charges {
		change(c.Account, c.Asset, func(h *domain.Holding) { h.Interest = h.Interest.Add(c.Interest) })
	}
	return out
}

// standing is a user's margin accounts as far as the service can tell.
type standing struct {
	userID   string
	accounts map[domain.Account][]domain.Holding
	pending  inFlight
}

// of returns an account's holdings.
func (st standing) of(a domain.Account) []domain.Holding { return st.accounts[a] }

// standingOf reads what the user's margin accounts hold and owe as far as
// the service can tell (review CK ①): the ledger's balances with the
// user's writes still on their way counted (inFlight.count), so that a
// borrow or a transfer out checked while another is on its way sees it.
// The PENDING writes are read before the ledger's balances: a write that
// lands in between counts twice rather than not at all, and twice errs on
// the cautious side too.
func (s *Service) standingOf(ctx context.Context, r ports.Repos, userID string) (standing, error) {
	st := standing{userID: userID}
	var err error
	if st.pending.borrows, err = r.Borrows().PendingOf(ctx, userID); err != nil {
		return standing{}, err
	}
	if st.pending.transfers, err = r.Transfers().PendingOf(ctx, userID); err != nil {
		return standing{}, err
	}
	if st.pending.charges, err = r.Interest().PendingOf(ctx, userID); err != nil {
		return standing{}, err
	}
	all, err := s.Ledger.Holdings(ctx, userID)
	if err != nil {
		return standing{}, err
	}
	st.accounts = st.pending.count(all)
	return st, nil
}
