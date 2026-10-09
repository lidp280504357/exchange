package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/user/domain"
)

// purgeWorld is a few test accounts and what they hold; its actions change
// it as the services would.
type purgeWorld struct {
	users  []purgeCandidate
	states map[string]*purgeState
	// pending is how many more reads a user's canceled orders take to end
	// (-1: never).
	pending map[string]int
	failOn  string // an action that fails: "sweep", "close", ...
	log     []string
	purged  map[string]bool
}

func (w *purgeWorld) counts(context.Context) (string, error) {
	return fmt.Sprintf("%d purged", len(w.purged)), nil
}

func (w *purgeWorld) candidates(context.Context, purgeOptions) ([]purgeCandidate, error) {
	return w.users, nil
}

func (w *purgeWorld) state(_ context.Context, user string) (purgeState, error) {
	s := w.states[user]
	if n, ok := w.pending[user]; ok && s.spotOrders > 0 {
		switch {
		case n > 0:
			w.pending[user] = n - 1
		case n == 0:
			// The engine confirmed the cancels; the ledger released the freezes.
			s.spotOrders = 0
			for i := range s.rows {
				s.rows[i].available = s.rows[i].available.Add(s.rows[i].frozen)
				s.rows[i].frozen = decimal.Zero
			}
		}
	}
	out := *s
	out.rows = slices.Clone(s.rows)
	return out, nil
}

func (w *purgeWorld) fail(what string) error {
	if w.failOn == what {
		return errors.New(what + " refused")
	}
	return nil
}

func (w *purgeWorld) cancelOrders(_ context.Context, user string) error {
	w.log = append(w.log, "cancel "+user)
	if _, ok := w.pending[user]; !ok {
		w.pending[user] = 0
	}
	return w.fail("cancel")
}

func (w *purgeWorld) marginOut(_ context.Context, user string, r purgeRow) error {
	w.log = append(w.log, fmt.Sprintf("out %s %s %s %s", user, r.account, r.asset, r.available))
	add(w.states[user], r.account, r.asset, r.available.Neg())
	add(w.states[user], "SPOT", r.asset, r.available)
	return w.fail("out")
}

func (w *purgeWorld) sweep(_ context.Context, user string, r purgeRow, _, _ string) error {
	if err := w.fail("sweep"); err != nil {
		return err
	}
	w.log = append(w.log, fmt.Sprintf("sweep %s %s %s %s", user, r.account, r.asset, r.available))
	add(w.states[user], r.account, r.asset, r.available.Neg())
	return nil
}

func (w *purgeWorld) setStatus(_ context.Context, user, to, _, _ string) error {
	w.log = append(w.log, "status "+user+" "+to)
	if to == domain.StatusClosed {
		return w.fail("close")
	}
	return nil
}

func (w *purgeWorld) markPurged(_ context.Context, user, _, _ string) error {
	w.log = append(w.log, "purged "+user)
	w.purged[user] = true
	return nil
}

// add moves a row's available balance by amount; a row left with nothing
// is gone, as the state's query leaves it out.
func add(s *purgeState, account, asset string, amount decimal.Decimal) {
	i := slices.IndexFunc(s.rows, func(r purgeRow) bool { return r.account == account && r.asset == asset })
	if i < 0 {
		s.rows = append(s.rows, purgeRow{account: account, asset: asset})
		i = len(s.rows) - 1
	}
	s.rows[i].available = s.rows[i].available.Add(amount)
	if s.rows[i].available.IsZero() && s.rows[i].frozen.IsZero() {
		s.rows = slices.Delete(s.rows, i, i+1)
	}
}

func row(account, asset, available string) purgeRow {
	return purgeRow{account: account, asset: asset, available: decimal.RequireFromString(available)}
}

func newPurgeWorld() *purgeWorld {
	w := &purgeWorld{states: map[string]*purgeState{}, pending: map[string]int{}, purged: map[string]bool{}}
	add1 := func(id, status string, exempt bool, s purgeState) {
		w.users = append(w.users, purgeCandidate{id: id, status: status, exempt: exempt})
		w.states[id] = &s
	}
	add1("plain", domain.StatusActive, false, purgeState{rows: []purgeRow{row("FUTURES", "BTC", "0.01"), row("SPOT", "USDT", "100")}})
	add1("review", domain.StatusRiskReview, false, purgeState{rows: []purgeRow{row("SPOT", "USDT", "5")}})
	add1("kept", domain.StatusActive, true, purgeState{rows: []purgeRow{row("SPOT", "USDT", "7")}})
	add1("hedged", domain.StatusActive, false, purgeState{positions: 1, rows: []purgeRow{row("FUTURES", "USDT", "50")}})
	add1("owing", domain.StatusActive, false, purgeState{rows: []purgeRow{row("MARGIN_CROSS_DEBT", "USDT", "-3"), row("MARGIN_CROSS", "USDT", "10")}})
	add1("leaving", domain.StatusActive, false, purgeState{withdrawals: 1, rows: []purgeRow{row("SPOT", "USDT", "1")}})
	ordering := purgeState{spotOrders: 1, rows: []purgeRow{row("SPOT", "USDT", "8")}}
	ordering.rows[0].frozen = decimal.NewFromInt(2)
	add1("ordering", domain.StatusActive, false, ordering)
	add1("margin", domain.StatusFrozen, false, purgeState{rows: []purgeRow{row("MARGIN_CROSS", "USDT", "20"), row("SPOT", "USDT", "1")}})
	w.pending["ordering"] = 2
	return w
}

func runPurge(t *testing.T, w *purgeWorld, dry bool) (string, error) {
	t.Helper()
	p := &purger{
		data: w, act: w, actor: "cli:ops", sleep: func(context.Context, time.Duration) error { return nil },
		opts: purgeOptions{dryRun: dry, reason: "test accounts", wait: time.Second, pace: time.Millisecond},
	}
	var out bytes.Buffer
	err := p.run(context.Background(), &out)
	return out.String(), err
}

// The purge (L4) settles what it can and skips the rest with the reason:
// balances go to ADJUSTMENT (a margin account's through spot), spot orders
// are canceled and waited for, the account is closed whatever its status
// and marked purged; exempt accounts, contract positions, a margin debt
// and a withdrawal in flight are left.
func TestPurgeSettlesClosesAndMarks(t *testing.T) {
	w := newPurgeWorld()
	out, err := runPurge(t, w, false)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"sweep plain FUTURES BTC 0.01", "sweep plain SPOT USDT 100", "status plain CLOSED", "purged plain",
		"status review CLOSED", "purged review",
		"cancel ordering", "sweep ordering SPOT USDT 10", "purged ordering",
		"out margin MARGIN_CROSS USDT 20", "sweep margin SPOT USDT 21", "status margin CLOSED", "purged margin",
	} {
		if !slices.Contains(w.log, want) {
			t.Errorf("missing %q in\n%s", want, strings.Join(w.log, "\n"))
		}
	}
	if slices.ContainsFunc(w.log, func(l string) bool { return strings.HasSuffix(l, " ACTIVE") }) {
		t.Errorf("an account under review or frozen is closed as it is: %v", w.log)
	}
	for _, id := range []string{"kept", "hedged", "owing", "leaving"} {
		if w.purged[id] || slices.ContainsFunc(w.log, func(l string) bool { return strings.Contains(l, " "+id) }) {
			t.Errorf("%s was touched: %v", id, w.log)
		}
	}
	for _, want := range []string{
		"skip kept: " + skipExempt, "skip hedged: " + skipContracts, "skip owing: " + skipDebt, "skip leaving: " + skipWithdrawal,
		"purged 4, skipped 4",
		"recovered to ADJUSTMENT: FUTURES BTC 0.01, SPOT USDT 136",
		"after: 4 purged",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

// A dry run changes nothing and says what would be recovered, a margin
// account's balance under spot.
func TestPurgeDryRun(t *testing.T) {
	w := newPurgeWorld()
	out, err := runPurge(t, w, true)
	if err != nil || len(w.log) != 0 || len(w.purged) != 0 {
		t.Fatalf("%v %v\n%s", err, w.log, out)
	}
	for _, want := range []string{"dry run over 8 test accounts", "would purge 4, skipped 4", "would recover to ADJUSTMENT: FUTURES BTC 0.01, SPOT USDT 134"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

// Orders that do not end in time leave the account as it is; a failed step
// is reported and fails the run, after the others were done.
func TestPurgeStopsShort(t *testing.T) {
	w := newPurgeWorld()
	w.pending["ordering"] = -1
	w.failOn = "close"
	out, err := runPurge(t, w, false)
	if err == nil || !strings.Contains(err.Error(), "accounts failed") {
		t.Fatalf("a failed close fails the run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "skip ordering: "+skipOrders) || slices.ContainsFunc(w.log, func(l string) bool { return strings.HasPrefix(l, "sweep ordering") }) {
		t.Fatalf("orders still open: nothing swept\n%s\n%v", out, w.log)
	}
	if !strings.Contains(out, "skip plain: failed: close it: close refused") || w.purged["plain"] {
		t.Fatalf("a refused close is not purged\n%s", out)
	}
}
