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
	failOn  string // an action that fails: "sweep", "close", "mark"
	log     []string
	keys    []string
	purged  map[string]bool
	closed  map[string]bool
}

func (w *purgeWorld) counts(context.Context) (string, error) {
	return fmt.Sprintf("%d purged", len(w.purged)), nil
}

// candidates are the users not purged yet, with the status the run left.
func (w *purgeWorld) candidates(context.Context, purgeOptions) ([]purgeCandidate, error) {
	var out []purgeCandidate
	for _, c := range w.users {
		if w.purged[c.id] {
			continue
		}
		if w.closed[c.id] {
			c.status = domain.StatusClosed
		}
		out = append(out, c)
	}
	return out, nil
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
				if s.rows[i].frozen.IsPositive() {
					s.rows[i].available = s.rows[i].available.Add(s.rows[i].frozen)
					s.rows[i].frozen = decimal.Zero
					s.rows[i].version++
				}
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

// flatten closes the user's positions, unless the user is "stuck" (one
// left open) or "liquidating" (409); the margin comes back to the futures
// account.
func (w *purgeWorld) flatten(_ context.Context, user, _, _ string) (purgeEnd, error) {
	w.log = append(w.log, "flatten "+user)
	switch user {
	case "stuck":
		return purgeEnd{left: []string{"BTC-USDT-PERP LONG 0.001 NOT_FILLED"}}, nil
	case "liquidating":
		return purgeEnd{}, errLiquidating
	}
	s := w.states[user]
	s.positions, s.contractOrders, s.conditionals = 0, 0, 0
	for i := range s.rows {
		if s.rows[i].account == "FUTURES" && s.rows[i].frozen.IsPositive() {
			s.rows[i].available = s.rows[i].available.Add(s.rows[i].frozen)
			s.rows[i].frozen = decimal.Zero
			s.rows[i].version++
		}
	}
	return purgeEnd{complete: true}, nil
}

// settleMargin repays each debt from its margin account (the debt row's
// account without _DEBT, the same pair), as far as it holds the asset;
// what it cannot is left.
func (w *purgeWorld) settleMargin(_ context.Context, user, _, _ string) (purgeEnd, error) {
	w.log = append(w.log, "settle "+user)
	s := w.states[user]
	end := purgeEnd{complete: true}
	for _, debt := range slices.Clone(s.rows) {
		if !debt.debt() {
			continue
		}
		owed, from := debt.available.Neg(), strings.TrimSuffix(debt.account, "_DEBT")
		i := slices.IndexFunc(s.rows, func(r purgeRow) bool { return r.account == from && r.scope == debt.scope && r.asset == debt.asset })
		paid := decimal.Zero
		if i >= 0 {
			paid = decimal.Min(owed, s.rows[i].available)
			addScoped(s, from, debt.scope, debt.asset, paid.Neg())
		}
		addScoped(s, debt.account, debt.scope, debt.asset, paid)
		if left := owed.Sub(paid); left.IsPositive() {
			end.complete = false
			end.debts = append(end.debts, purgeDebt{account: from, symbol: debt.scope, asset: debt.asset, amount: left})
		}
	}
	return end, nil
}

func (w *purgeWorld) marginIn(_ context.Context, user string, d purgeDebt, key string) error {
	w.log = append(w.log, fmt.Sprintf("in %s %s %s %s", user, d.account, d.asset, d.amount))
	w.keys = append(w.keys, key)
	add(w.states[user], "SPOT", d.asset, d.amount.Neg())
	addScoped(w.states[user], d.account, d.symbol, d.asset, d.amount)
	return nil
}

func (w *purgeWorld) cancelOrders(_ context.Context, user string) error {
	w.log = append(w.log, "cancel "+user)
	if _, ok := w.pending[user]; !ok {
		w.pending[user] = 0
	}
	return w.fail("cancel")
}

func (w *purgeWorld) marginOut(_ context.Context, user string, r purgeRow, key string) error {
	w.log = append(w.log, fmt.Sprintf("out %s %s %s %s", user, r.account, r.asset, r.available))
	w.keys = append(w.keys, key)
	add(w.states[user], r.account, r.asset, r.available.Neg())
	add(w.states[user], "SPOT", r.asset, r.available)
	return w.fail("out")
}

func (w *purgeWorld) sweep(_ context.Context, user string, r purgeRow, key, _, _ string) error {
	if err := w.fail("sweep"); err != nil {
		return err
	}
	w.log = append(w.log, fmt.Sprintf("sweep %s %s %s %s", user, r.account, r.asset, r.available))
	w.keys = append(w.keys, key)
	add(w.states[user], r.account, r.asset, r.available.Neg())
	return nil
}

func (w *purgeWorld) setStatus(_ context.Context, user, to, _, _ string) error {
	w.log = append(w.log, "status "+user+" "+to)
	if to == domain.StatusClosed {
		if err := w.fail("close"); err != nil {
			return err
		}
		w.closed[user] = true
	}
	return nil
}

func (w *purgeWorld) markPurged(_ context.Context, user, _, _ string) error {
	if err := w.fail("mark"); err != nil {
		return err
	}
	w.log = append(w.log, "purged "+user)
	w.purged[user] = true
	return nil
}

// add moves a row's available balance by amount; a row left with nothing
// is gone, as the state's query leaves it out.
func add(s *purgeState, account, asset string, amount decimal.Decimal) {
	addScoped(s, account, "", asset, amount)
}

// addScoped is add on an isolated margin account's row of a pair.
func addScoped(s *purgeState, account, scope, asset string, amount decimal.Decimal) {
	i := slices.IndexFunc(s.rows, func(r purgeRow) bool { return r.account == account && r.scope == scope && r.asset == asset })
	if i < 0 {
		s.rows = append(s.rows, purgeRow{account: account, scope: scope, asset: asset})
		i = len(s.rows) - 1
	}
	s.rows[i].available = s.rows[i].available.Add(amount)
	s.rows[i].version++
	if s.rows[i].available.IsZero() && s.rows[i].frozen.IsZero() {
		s.rows = slices.Delete(s.rows, i, i+1)
	}
}

func row(account, asset, available string) purgeRow {
	return purgeRow{account: account, asset: asset, available: decimal.RequireFromString(available)}
}

func newPurgeWorld() *purgeWorld {
	w := &purgeWorld{states: map[string]*purgeState{}, pending: map[string]int{}, purged: map[string]bool{}, closed: map[string]bool{}}
	add1 := func(id, status string, exempt bool, s purgeState) {
		w.users = append(w.users, purgeCandidate{id: id, status: status, exempt: exempt})
		w.states[id] = &s
	}
	add1("plain", domain.StatusActive, false, purgeState{rows: []purgeRow{row("FUTURES", "BTC", "0.01"), row("SPOT", "USDT", "100")}})
	add1("review", domain.StatusRiskReview, false, purgeState{rows: []purgeRow{row("SPOT", "USDT", "5")}})
	add1("kept", domain.StatusActive, true, purgeState{rows: []purgeRow{row("SPOT", "USDT", "7")}})
	hedged := purgeState{positions: 1, rows: []purgeRow{row("FUTURES", "USDT", "50")}}
	hedged.rows[0].frozen = decimal.NewFromInt(4) // the position's margin
	add1("hedged", domain.StatusActive, false, hedged)
	add1("stuck", domain.StatusActive, false, purgeState{positions: 1, rows: []purgeRow{row("FUTURES", "USDT", "9")}})
	add1("liquidating", domain.StatusActive, false, purgeState{positions: 1, rows: []purgeRow{row("FUTURES", "USDT", "9")}})
	add1("owing", domain.StatusActive, false, purgeState{rows: []purgeRow{row("MARGIN_CROSS_DEBT", "USDT", "-3"), row("MARGIN_CROSS", "USDT", "10")}})
	// Owes more than the margin account holds: the rest comes in from spot.
	add1("short", domain.StatusActive, false, purgeState{rows: []purgeRow{row("MARGIN_CROSS_DEBT", "USDT", "-5"), row("MARGIN_CROSS", "USDT", "2"), row("SPOT", "USDT", "10")}})
	// Owes what neither account holds.
	add1("broke", domain.StatusActive, false, purgeState{rows: []purgeRow{row("MARGIN_CROSS_DEBT", "USDT", "-5"), row("SPOT", "USDT", "1")}})
	// Two debts in one asset met from spot one after the other (B188 ①).
	twoDebts := purgeState{rows: []purgeRow{row("MARGIN_CROSS_DEBT", "USDT", "-3"), row("MARGIN_ISOLATED_DEBT", "USDT", "-2"), row("SPOT", "USDT", "10")}}
	twoDebts.rows[1].scope = "ETH-USDT"
	add1("twodebts", domain.StatusActive, false, twoDebts)
	add1("leaving", domain.StatusActive, false, purgeState{withdrawals: 1, rows: []purgeRow{row("SPOT", "USDT", "1")}})
	ordering := purgeState{spotOrders: 1, rows: []purgeRow{row("SPOT", "USDT", "8")}}
	ordering.rows[0].frozen = decimal.NewFromInt(2)
	add1("ordering", domain.StatusActive, false, ordering)
	add1("margin", domain.StatusFrozen, false, purgeState{rows: []purgeRow{row("MARGIN_CROSS", "USDT", "20"), row("SPOT", "USDT", "1")}})
	// An order on the margin account holds 5 of it until canceled (B184 ①).
	marginOrder := purgeState{spotOrders: 1, rows: []purgeRow{row("MARGIN_CROSS", "BTC", "0.2")}}
	marginOrder.rows[0].frozen = decimal.RequireFromString("0.05")
	add1("marginorder", domain.StatusActive, false, marginOrder)
	// Closed by an earlier run that failed to mark it (B184 ③).
	add1("closed", domain.StatusClosed, false, purgeState{})
	w.pending["ordering"] = 2
	w.pending["marginorder"] = 1
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
		"cancel marginorder", "out marginorder MARGIN_CROSS BTC 0.25", "sweep marginorder SPOT BTC 0.25", "purged marginorder",
		"purged closed",
		// L4b: positions flattened, debts settled (from spot when short).
		"flatten hedged", "sweep hedged FUTURES USDT 54", "purged hedged",
		"settle owing", "out owing MARGIN_CROSS USDT 7", "sweep owing SPOT USDT 7", "purged owing",
		"settle short", "in short MARGIN_CROSS USDT 3", "sweep short SPOT USDT 7", "purged short",
		"in twodebts MARGIN_CROSS USDT 3", "in twodebts MARGIN_ISOLATED USDT 2", "sweep twodebts SPOT USDT 5", "purged twodebts",
	} {
		if !slices.Contains(w.log, want) {
			t.Errorf("missing %q in\n%s", want, strings.Join(w.log, "\n"))
		}
	}
	if slices.ContainsFunc(w.log, func(l string) bool { return strings.HasSuffix(l, " ACTIVE") }) {
		t.Errorf("an account under review or frozen is closed as it is: %v", w.log)
	}
	if slices.Contains(w.log, "status closed CLOSED") {
		t.Errorf("a closed account is only marked: %v", w.log)
	}
	// The keys name the balance as read: the margin account's version 1
	// after its order's release, spot's version 1 after the move in (a new
	// row).
	for _, want := range []string{
		"purge:plain:FUTURES:BTC:v0", "purge:marginorder:SPOT:BTC:v1", marginKey("marginorder", purgeRow{account: "MARGIN_CROSS", asset: "BTC", version: 1}),
		// The two moves in: each its own debt's account and spot's version then.
		marginInKey("twodebts", purgeDebt{account: "MARGIN_CROSS", asset: "USDT"}, 0),
		marginInKey("twodebts", purgeDebt{account: "MARGIN_ISOLATED", symbol: "ETH-USDT", asset: "USDT"}, 1),
	} {
		if !slices.Contains(w.keys, want) {
			t.Errorf("missing key %q in %v", want, w.keys)
		}
	}
	for _, id := range []string{"kept", "leaving"} {
		if w.purged[id] || slices.ContainsFunc(w.log, func(l string) bool { return strings.Contains(l, " "+id) }) {
			t.Errorf("%s was touched: %v", id, w.log)
		}
	}
	for _, id := range []string{"stuck", "liquidating", "broke"} {
		if w.purged[id] || slices.ContainsFunc(w.log, func(l string) bool { return strings.HasPrefix(l, "sweep "+id) }) {
			t.Errorf("%s was swept or purged: %v", id, w.log)
		}
	}
	for _, want := range []string{
		"skip kept: " + skipExempt, "skip leaving: " + skipWithdrawal,
		"skip stuck: " + skipNotFlat + ": BTC-USDT-PERP LONG 0.001 NOT_FILLED", "skip liquidating: " + skipLiquidating,
		"skip broke: " + skipDebt + ": MARGIN_CROSS USDT 5, not in spot",
		"purged 10, skipped 5",
		"recovered to ADJUSTMENT: FUTURES BTC 0.01, FUTURES USDT 54, SPOT BTC 0.25, SPOT USDT 155",
		"after: 10 purged",
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
	for _, want := range []string{
		"dry run over 15 test accounts", "would purge 13, skipped 2",
		"first: derivatives flatten 3, margin settle 4",
		"would recover to ADJUSTMENT: FUTURES BTC 0.01, FUTURES USDT 72, SPOT BTC 0.25, SPOT USDT 169",
	} {
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

// A run again (B186): the purged accounts are not candidates any more and
// nothing is moved twice; an account a failed run closed but did not mark
// is only marked; the skipped ones are skipped again.
func TestPurgeRunsAgain(t *testing.T) {
	w := newPurgeWorld()
	w.failOn = "mark"
	if out, err := runPurge(t, w, false); err == nil || len(w.purged) != 0 || len(w.closed) == 0 {
		t.Fatalf("the marks fail: %v %v\n%s", err, w.purged, out)
	}
	swept := len(w.keys)
	w.failOn, w.log = "", nil
	out, err := runPurge(t, w, false)
	if err != nil || len(w.keys) != swept {
		t.Fatalf("again: %v, %d keys then %d\n%s", err, swept, len(w.keys), out)
	}
	if slices.ContainsFunc(w.log, func(l string) bool { return strings.HasPrefix(l, "status ") || strings.HasPrefix(l, "sweep ") }) {
		t.Fatalf("closed already, swept already: only marked: %v", w.log)
	}
	if !strings.Contains(out, "purged 10, skipped 5") {
		t.Fatalf("the closed ones marked, the others skipped again:\n%s", out)
	}
	w.log = nil
	if out, err := runPurge(t, w, false); err != nil || !strings.Contains(out, "purging 5 test accounts") || !strings.Contains(out, "purged 0, skipped 5") {
		t.Fatalf("a third run: %v\n%s", err, out)
	}
}

// The L4b answers as the services write them (api/internal): flatten's
// remaining positions; settle's remaining debts and, since C80, the
// accounts whose cancel was refused, which leave the user not done even
// with no debt (B189).
func TestTheL4bAnswers(t *testing.T) {
	end, err := flattenedEnd([]byte(`{"canceled_orders":1,"canceled_conditionals":0,"closed":[],` +
		`"remaining":[{"symbol":"BTC-USDT-PERP","side":"LONG","quantity":"0.001","reason":"NOT_FILLED"}],"complete":false}`))
	if err != nil || end.complete || !slices.Equal(end.left, []string{"BTC-USDT-PERP LONG 0.001 NOT_FILLED"}) {
		t.Fatalf("flatten: %+v %v", end, err)
	}
	end, err = settledEnd([]byte(`{"canceled_orders":0,"cancel_refused":[{"account":"MARGIN_ISOLATED","symbol":"ETH-USDT","code":"PRODUCT_CLOSED"}],` +
		`"repaid":[],"remaining_debt":[{"account":"MARGIN_CROSS","symbol":null,"asset":"USDT","amount":"2.5"}],"complete":false}`))
	if err != nil || end.complete || len(end.debts) != 1 || !end.debts[0].amount.Equal(decimal.RequireFromString("2.5")) ||
		!slices.Equal(end.left, []string{"MARGIN_ISOLATED ETH-USDT: cancel refused, PRODUCT_CLOSED", "MARGIN_CROSS USDT 2.5"}) {
		t.Fatalf("settle: %+v %v", end, err)
	}
	end, err = settledEnd([]byte(`{"canceled_orders":0,"cancel_refused":[{"account":"MARGIN_CROSS","symbol":null,"code":"COMMON_INVALID_ARGUMENT"}],` +
		`"repaid":[],"remaining_debt":[],"complete":true}`))
	if err != nil || end.complete || end.refused != 1 {
		t.Fatalf("no debt, a refused cancel: not done: %+v %v", end, err)
	}
	// Skipped as such, not as a debt (B190).
	var o purgeOutcome
	if done, err := (&purger{}).ended(&o, end, nil, skipDebt); done || err != nil || o.skip != skipRefused ||
		o.detail != "MARGIN_CROSS: cancel refused, COMMON_INVALID_ARGUMENT" {
		t.Fatalf("the skip: %v %v %+v", done, err, o)
	}
	if end, err := settledEnd([]byte(`{"canceled_orders":0,"cancel_refused":[],"repaid":[],"remaining_debt":[],"complete":true}`)); err != nil || !end.complete {
		t.Fatalf("done: %+v %v", end, err)
	}
	if _, err := settledEnd([]byte(`{"complete":"yes"}`)); err == nil {
		t.Fatal("an answer that is not the contract's")
	}
}
