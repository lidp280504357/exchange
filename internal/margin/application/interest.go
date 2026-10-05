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
// hour (FLOATING from the pool's use on it), in ledger journals of one
// asset and hour of at most interestChunk accounts each (ledger
// AccrueMarginInterest). An asset with a borrow or a repayment from
// before the hour still in flight waits for the next pass, and the hour
// stays RUNNING until every asset of it is charged; the later hours wait
// for it. It returns the number of loans charged.
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
// charged. The loans and what changed them since the hour are read in one
// snapshot (review CK ②): read apart, a borrow or a repayment finished in
// between would count in one and not the other, and the hour charged on
// a principal it never had.
func (s *Service) chargeHour(ctx context.Context, hour time.Time) (int, bool, error) {
	read := s.Store.Read()
	if err := read.Interest().StartRun(ctx, hour); err != nil {
		return 0, false, err
	}
	var since map[ports.LoanKey]ports.LoanSince
	var open []ports.Loan
	var cat Catalog
	err := s.Store.Snapshot(ctx, func(r ports.Repos) error {
		var err error
		if since, err = r.Interest().Since(ctx, hour); err != nil {
			return err
		}
		if open, err = r.Loans().Open(ctx, ""); err != nil {
			return err
		}
		cat, err = s.catalog(ctx, r)
		return err
	})
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

// interestChunk is how many accounts one interest journal books (the
// ledger takes at most 2000).
const interestChunk = 1000

// chargeAsset books one asset's hour, once (design §4.3, review CK ②):
// the first pass stores a charge per loan owed on the hour, PENDING, all
// of them in one transaction, and from then on the stored charges are the
// hour's — a retry posts them as they are. They go to the ledger in
// journals of at most interestChunk accounts, in the order of the
// accounts, each under a key of the asset, the hour and its place
// (<asset>:<hour>, then <asset>:<hour>:<n>), which the ledger replays,
// and are recorded DONE journal by journal.
func (s *Service) chargeAsset(ctx context.Context, t domain.AssetTerms, hour time.Time, loans []owedOnHour) (int, error) {
	charges, err := s.hourCharges(ctx, t, hour, loans)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := 0; i*interestChunk < len(charges); i++ {
		chunk := charges[i*interestChunk : min((i+1)*interestChunk, len(charges))]
		if !slices.ContainsFunc(chunk, func(c ports.Charge) bool { return c.Status == ports.OpPending }) {
			continue
		}
		key := fmt.Sprintf("%s:%d", t.Asset, hour.Unix())
		if i > 0 {
			key = fmt.Sprintf("%s:%d", key, i)
		}
		booked, err := s.bookCharges(ctx, t.Asset, hour, key, chunk)
		n += booked
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// hourCharges returns the asset's hourly charges of an hour in the order
// of their accounts, storing them first unless a pass did before.
func (s *Service) hourCharges(ctx context.Context, t domain.AssetTerms, hour time.Time, loans []owedOnHour) ([]ports.Charge, error) {
	var charges []ports.Charge
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.Interest().LockHour(ctx, t.Asset, hour); err != nil {
			return err
		}
		stored, err := r.Interest().OfHour(ctx, t.Asset, hour)
		if err != nil || len(stored) > 0 {
			charges = stored
			return err
		}
		lent := decimal.Zero
		for _, l := range loans {
			lent = lent.Add(l.principal)
		}
		rate, err := s.hourRate(ctx, r, t, hour, lent)
		if err != nil {
			return err
		}
		now := s.Now()
		for _, l := range loans {
			interest := domain.Interest(l.principal, rate.Rate, t.Decimals)
			if !interest.IsPositive() {
				continue
			}
			c := ports.Charge{
				ID: uuid.Must(uuid.NewV7()).String(), UserID: l.key.UserID, Account: l.key.Account, Asset: l.key.Asset, Hour: hour,
				Principal: l.principal, Model: rate.Model, Rate: rate.Rate, Interest: interest, Status: ports.OpPending, CreatedAt: now,
			}
			if _, err := r.Interest().Insert(ctx, c); err != nil {
				return err
			}
		}
		charges, err = r.Interest().OfHour(ctx, t.Asset, hour)
		return err
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(charges, func(a, b ports.Charge) int {
		return cmp.Or(cmp.Compare(a.UserID, b.UserID), cmp.Compare(a.Account.Key(), b.Account.Key()))
	})
	return charges, nil
}

// bookCharges posts one journal of an hour's charges and records them
// DONE; it returns how many it recorded.
func (s *Service) bookCharges(ctx context.Context, asset string, hour time.Time, key string, chunk []ports.Charge) (int, error) {
	lines := make([]ports.Accrual, len(chunk))
	for i, c := range chunk {
		lines[i] = ports.Accrual{UserID: c.UserID, Account: c.Account, Amount: c.Interest}
	}
	journal, err := s.Ledger.Accrue(ctx, key, asset, "interest "+hour.Format(time.RFC3339), lines)
	if err != nil {
		if refused(err) {
			s.Log.ErrorContext(ctx, "the ledger refused an hour's interest", "asset", asset, "hour", hour, "key", key,
				"error", apperr.From(err).Message)
		}
		s.count("interest", "pending")
		return 0, err
	}
	n := 0
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		n = 0
		for _, c := range chunk {
			cur, err := r.Interest().GetForUpdate(ctx, c.ID)
			if err != nil {
				return err
			}
			if cur.Status != ports.OpPending {
				continue
			}
			cur.Status, cur.DoneAt, cur.JournalKey = ports.OpDone, s.Now(), "margin-interest:"+key
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
			n++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	s.count("interest", "done")
	return n, nil
}
