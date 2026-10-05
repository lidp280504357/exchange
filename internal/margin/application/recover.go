package application

import (
	"context"
	"errors"
	"time"

	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/platform/apperr"
)

// recoverAfter is how old a PENDING write must be before recovery takes
// it over from the request that started it.
const recoverAfter = 10 * time.Second

// Recover finishes the transfers, borrows and repayments whose ledger
// outcome was not recorded (a crash or a ledger outage midway): each is
// posted again under its key, so the ledger books it once, and its
// outcome recorded. An hour's interest left PENDING is the next
// ChargeInterest pass's until the hour falls out of its catch-up window
// (MaxCatchUp); Recover then posts the charges stored for it as they are,
// so that none stays owed in margin-service's eyes and never booked. It
// returns how many it finished.
func (s *Service) Recover(ctx context.Context) (int, error) {
	cutoff := s.Now().Add(-recoverAfter)
	r := s.Store.Read()
	n := 0
	var errs []error
	finished := func(err error) {
		switch {
		case err == nil || refused(err):
			n++
		case !errors.Is(err, errInProgress):
			errs = append(errs, err)
		}
	}
	transfers, err := r.Transfers().Pending(ctx, cutoff, 100)
	if err != nil {
		return n, err
	}
	for _, t := range transfers {
		_, err := s.postTransfer(ctx, t)
		finished(err)
	}
	borrows, err := r.Borrows().Pending(ctx, cutoff, 100)
	if err != nil {
		return n, err
	}
	for _, b := range borrows {
		_, err := s.postBorrow(ctx, b)
		finished(err)
	}
	repays, err := r.Repays().Pending(ctx, cutoff, 100)
	if err != nil {
		return n, err
	}
	for _, p := range repays {
		_, err := s.postRepay(ctx, p)
		finished(err)
	}
	stale, err := r.Interest().Pending(ctx, s.Now().Add(-MaxCatchUp*time.Hour), 100)
	if err != nil {
		return n, err
	}
	type assetHour struct {
		asset string
		hour  time.Time
	}
	seen := map[assetHour]bool{}
	for _, c := range stale {
		if k := (assetHour{c.Asset, c.Hour}); !seen[k] {
			seen[k] = true
			booked, err := s.chargeAsset(ctx, domain.AssetTerms{Asset: c.Asset}, c.Hour, nil) // the stored charges only
			n += booked
			if err != nil && apperr.From(err).Kind != apperr.KindUnavailable {
				errs = append(errs, err)
			}
		}
	}
	return n, errors.Join(errs...)
}
