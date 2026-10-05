package application

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
)

// MaxCatchUp bounds how many past hours a run charges after an outage.
const MaxCatchUp = 48

// ChargeInterest charges the hours that are due (design §4.3): from the
// hour after the last finished run to the current one, oldest first, at
// most MaxCatchUp of them. Each loan is charged once per hour on the
// principal it had on the hour (PrincipalAt), at the asset's rate of the
// hour (FLOATING from the pool's use on it), one ledger journal per asset
// and hour (ledger AccrueMarginInterest). An asset with a borrow or a
// repayment from before the hour still in flight waits for the next pass,
// and the hour stays RUNNING until every asset of it is charged; the
// later hours wait for it. It returns the number of loans charged.
func (s *Service) ChargeInterest(ctx context.Context) (int, error) {
	now := domain.Hour(s.Now())
	hours, err := s.dueHours(ctx, now)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, h := range hours {
		n, done, err := s.chargeHour(ctx, h)
		total += n
		if err != nil {
			return total, fmt.Errorf("interest of %s: %w", h.Format(time.RFC3339), err)
		}
		if !done {
			break
		}
	}
	return total, nil
}

// dueHours lists the hours to charge, oldest first: from the hour after
// the latest finished run (or the earliest unfinished one, or now on the
// very first run) up to now, at most MaxCatchUp of them.
func (s *Service) dueHours(ctx context.Context, now time.Time) ([]time.Time, error) {
	r := s.Store.Read().Interest()
	start := now
	if last, ok, err := r.LastDone(ctx); err != nil {
		return nil, err
	} else if ok {
		start = last.Add(time.Hour)
	} else if first, ok, err := r.FirstRunning(ctx); err != nil {
		return nil, err
	} else if ok {
		start = first
	}
	if oldest := now.Add(-(MaxCatchUp - 1) * time.Hour); start.Before(oldest) {
		start = oldest
	}
	var hours []time.Time
	for h := start; !h.After(now); h = h.Add(time.Hour) {
		hours = append(hours, h)
	}
	return hours, nil
}

// owedOnHour is a loan's principal on the hour.
type owedOnHour struct {
	key       ports.LoanKey
	principal decimal.Decimal
}

// chargeHour charges one hour; done reports whether every asset of it is
// charged.
func (s *Service) chargeHour(ctx context.Context, hour time.Time) (int, bool, error) {
	read := s.Store.Read()
	if err := read.Interest().StartRun(ctx, hour); err != nil {
		return 0, false, err
	}
	since, err := read.Interest().Since(ctx, hour)
	if err != nil {
		return 0, false, err
	}
	open, err := read.Loans().Open(ctx, "")
	if err != nil {
		return 0, false, err
	}
	// The loans owed on the hour: those owing now and those whose
	// principal changed since (a loan repaid after the hour was owed on it).
	now := map[ports.LoanKey]decimal.Decimal{}
	for _, l := range open {
		now[ports.LoanKey{UserID: l.UserID, Account: l.Account, Asset: l.Asset}] = l.Principal
	}
	keys := map[ports.LoanKey]bool{}
	for k := range now {
		keys[k] = true
	}
	for k := range since {
		keys[k] = true
	}
	byAsset := map[string][]owedOnHour{}
	waiting := map[string]bool{}
	for k := range keys {
		if since[k].Unsettled {
			waiting[k.Asset] = true
			continue
		}
		if p := domain.PrincipalAt(now[k], since[k].Borrowed, since[k].Repaid); p.IsPositive() {
			byAsset[k.Asset] = append(byAsset[k.Asset], owedOnHour{key: k, principal: p})
		}
	}
	cat, err := s.catalog(ctx, read)
	if err != nil {
		return 0, false, err
	}
	n, done := 0, len(waiting) == 0
	for _, asset := range slices.Sorted(maps.Keys(byAsset)) {
		if waiting[asset] {
			continue
		}
		t, ok := cat.Assets[asset]
		if !ok {
			continue // dropped from the margin list: its loans are charged no more
		}
		charged, err := s.chargeAsset(ctx, t, hour, byAsset[asset])
		n += charged
		if err != nil {
			s.Log.WarnContext(ctx, "interest charge failed; the next pass retries it", "asset", asset, "hour", hour, "error", err)
			done = false
		}
	}
	if !done {
		return n, false, nil
	}
	if err := read.Interest().FinishRun(ctx, hour, n); err != nil {
		return n, false, err
	}
	if s.Metrics != nil {
		s.Metrics.InterestRun.SetToCurrentTime()
	}
	return n, true, nil
}

// chargeAsset books one asset's hour, once: a charge per loan is stored
// PENDING, the PENDING charges of the asset and hour are posted in one
// journal under a key of the asset and the hour, and recorded DONE. A
// retry finds the charges stored before and posts the same journal, which
// the ledger replays.
func (s *Service) chargeAsset(ctx context.Context, t domain.AssetTerms, hour time.Time, loans []owedOnHour) (int, error) {
	read := s.Store.Read()
	lent := decimal.Zero
	for _, l := range loans {
		lent = lent.Add(l.principal)
	}
	rate, err := s.hourRate(ctx, read, t, hour, lent)
	if err != nil {
		return 0, err
	}
	var charges []ports.Charge
	for _, l := range loans {
		c, ok, err := read.Interest().Hourly(ctx, l.key.UserID, l.key.Account, l.key.Asset, hour)
		if err != nil {
			return 0, err
		}
		if !ok {
			interest := domain.Interest(l.principal, rate.Rate, t.Decimals)
			if !interest.IsPositive() {
				continue
			}
			c = ports.Charge{
				ID: uuid.Must(uuid.NewV7()).String(), UserID: l.key.UserID, Account: l.key.Account, Asset: l.key.Asset, Hour: hour,
				Principal: l.principal, Model: rate.Model, Rate: rate.Rate, Interest: interest, Status: ports.OpPending, CreatedAt: s.Now(),
			}
			if inserted, err := read.Interest().Insert(ctx, c); err != nil {
				return 0, err
			} else if !inserted { // stored meanwhile: book that one
				if c, _, err = read.Interest().Hourly(ctx, l.key.UserID, l.key.Account, l.key.Asset, hour); err != nil {
					return 0, err
				}
			}
		}
		if c.Status == ports.OpPending {
			charges = append(charges, c)
		}
	}
	if len(charges) == 0 {
		return 0, nil
	}
	slices.SortFunc(charges, func(a, b ports.Charge) int {
		return cmp.Or(cmp.Compare(a.UserID, b.UserID), cmp.Compare(a.Account.Key(), b.Account.Key()))
	})
	lines := make([]ports.Accrual, len(charges))
	for i, c := range charges {
		lines[i] = ports.Accrual{UserID: c.UserID, Account: c.Account, Amount: c.Interest}
	}
	journal, err := s.Ledger.Accrue(ctx, fmt.Sprintf("%s:%d", t.Asset, hour.Unix()), t.Asset, "interest "+hour.Format(time.RFC3339), lines)
	if err != nil {
		if refused(err) {
			s.Log.ErrorContext(ctx, "the ledger refused an hour's interest", "asset", t.Asset, "hour", hour, "error", apperr.From(err).Message)
		}
		s.count("interest", "pending")
		return 0, err
	}
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		for _, c := range charges {
			cur, err := r.Interest().GetForUpdate(ctx, c.ID)
			if err != nil {
				return err
			}
			if cur.Status != ports.OpPending {
				continue
			}
			cur.Status, cur.DoneAt = ports.OpDone, s.Now()
			if err := r.Interest().Finish(ctx, cur); err != nil {
				return err
			}
			if err := r.Loans().Add(ctx, cur.UserID, cur.Account, cur.Asset, decimal.Zero, cur.Interest, cur.DoneAt); err != nil {
				return err
			}
			loan, err := r.Loans().Get(ctx, cur.UserID, cur.Account, cur.Asset)
			if err != nil {
				return err
			}
			if err := r.Emit(ctx, event.TopicMargin, interestEvent(cur, loan, journal), "user", cur.UserID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	s.count("interest", "done")
	return len(charges), nil
}
