package application

import (
	"context"
	"errors"
	"time"
)

// recoverAfter is how old a PENDING write must be before recovery takes
// it over from the request that started it.
const recoverAfter = 10 * time.Second

// Recover finishes the transfers, borrows and repayments whose ledger
// outcome was not recorded (a crash or a ledger outage midway): each is
// posted again under its key, so the ledger books it once, and its
// outcome recorded. An hour's interest left PENDING is the next
// ChargeInterest pass's. It returns how many it finished.
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
	return n, errors.Join(errs...)
}
