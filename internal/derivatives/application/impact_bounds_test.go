package application

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestEachCrossStopsAtItsBounds: an impact measures so many cross
// accounts, for so long; the positions of those left are unmeasured
// (C5.5 ⑩).
func TestEachCrossStopsAtItsBounds(t *testing.T) {
	defer func(n int, d time.Duration) { impactAccounts, impactBudget = n, d }(impactAccounts, impactBudget)
	cross := map[string]int{"alice": 1, "bob": 2, "carol": 4}
	total := 7

	impactAccounts, impactBudget = 2, time.Second
	done := 0
	left, err := eachCross(context.Background(), cross, func(_ context.Context, user string) error {
		done += cross[user]
		return nil
	})
	if err != nil || left+done != total || left == 0 {
		t.Fatalf("two accounts at most: measured %d, left %d, %v", done, left, err)
	}

	// Out of time: the account being measured and the rest are left.
	impactAccounts, impactBudget = 10, 50*time.Millisecond
	calls := 0
	left, err = eachCross(context.Background(), cross, func(ctx context.Context, _ string) error {
		calls++
		<-ctx.Done()
		return ctx.Err()
	})
	if err != nil || calls != 1 || left != total {
		t.Fatalf("out of time: %d calls, left %d, %v", calls, left, err)
	}

	// Another failure is the impact's.
	boom := errors.New("ledger down")
	if _, err := eachCross(context.Background(), cross, func(context.Context, string) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("a failure: %v", err)
	}
}
