package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lidp280504357/exchange/internal/ledger/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

func TestReleaseUnclaimed(t *testing.T) {
	svc, _, db := setup(t)
	ctx := context.Background()
	user, dep := uuid.NewString(), uuid.NewString()
	unclaimed := func() string {
		list, err := svc.SystemBalances(ctx, "USDT")
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range list {
			if a.Key.Type == domain.AccountUnclaimedDeposit {
				return a.Available.String()
			}
		}
		return "0"
	}
	// Below the minimum: booked to UNCLAIMED_DEPOSIT.
	if _, err := svc.CreditDeposit(ctx, uuid.NewString(), domain.Deposit{
		ID: dep, UserID: user, Asset: "USDT", Amount: d("0.8"), Network: "TRON", TxHash: "t1", Unclaimed: true, Reason: "BELOW_MINIMUM",
	}); err != nil {
		t.Fatal(err)
	}
	if got := unclaimed(); got != "0.8" {
		t.Fatalf("unclaimed %s", got)
	}
	if j, err := svc.UnclaimedRelease(ctx, dep); err != nil || j != "" {
		t.Fatalf("released before the release: %q %v", j, err)
	}
	res, err := svc.ReleaseUnclaimed(ctx, dep, user, "USDT", d("0.8"), "ops@example.com", "the user asked")
	if err != nil || res.Replayed {
		t.Fatalf("release %+v %v", res, err)
	}
	if j, err := svc.UnclaimedRelease(ctx, dep); err != nil || j != res.JournalID {
		t.Fatalf("its release %q %v, want %s", j, err, res.JournalID)
	}
	if av, _ := usdt(t, svc, user, domain.AccountSpot); !av.Equal(d("0.8")) || unclaimed() != "0" {
		t.Fatalf("after the release: user %s, unclaimed %s", av, unclaimed())
	}
	again, err := svc.ReleaseUnclaimed(ctx, dep, user, "USDT", d("0.8"), "ops@example.com", "the user asked")
	if err != nil || !again.Replayed || again.JournalID != res.JournalID {
		t.Fatalf("repeated %+v %v", again, err)
	}
	// A retry in other words, by another administrator, is the same release (C5.5 ⑦).
	reworded, err := svc.ReleaseUnclaimed(ctx, dep, user, "USDT", d("0.8"), "fin@example.com", "retrying: the record failed")
	if err != nil || !reworded.Replayed || reworded.JournalID != res.JournalID {
		t.Fatalf("repeated in other words %+v %v", reworded, err)
	}
	other := uuid.NewString()
	if _, err := svc.ReleaseUnclaimed(ctx, other, user, "USDT", d("0.1"), "ops@example.com", "nothing is unclaimed"); !apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE") {
		t.Fatalf("more than UNCLAIMED_DEPOSIT holds: %v", err)
	}
	if _, err := svc.ReleaseUnclaimed(ctx, other, user, "USDT", d("0.1"), "", "no actor"); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("no actor: %v", err)
	}
	if n := count(t, db, `SELECT count(*) FROM outbox WHERE event_type = 'audit.AdminActionPerformed'`); n != 1 {
		t.Fatalf("release audits: %d", n)
	}
}

func TestHoldsAndAccountAdjustments(t *testing.T) {
	svc, store, db := setup(t)
	ctx := context.Background()
	user := uuid.NewString()
	if err := svc.OnUserRegistered(ctx, uuid.NewString(), user, "SG"); err != nil {
		t.Fatal(err)
	}

	id := uuid.NewString()
	h, err := svc.PlaceHold(ctx, id, user, "USDT", d("250"), "risk@example.com", "chargeback under review")
	if err != nil || h.JournalID == "" || !h.Active() || h.AccountType != domain.AccountSpot {
		t.Fatalf("hold: %+v %v", h, err)
	}
	if av, fr := usdt(t, svc, user, domain.AccountSpot); !av.Equal(d("9750")) || !fr.Equal(d("250")) {
		t.Fatalf("after the hold: %s/%s", av, fr)
	}
	again, err := svc.PlaceHold(ctx, id, user, "USDT", d("250"), "risk@example.com", "chargeback under review")
	if err != nil || again.JournalID != h.JournalID {
		t.Fatalf("repeated: %+v %v", again, err)
	}
	if _, err := svc.PlaceHold(ctx, id, user, "USDT", d("300"), "risk@example.com", "chargeback under review"); !apperr.Is(err, apperr.CodeIdempotencyConflict) {
		t.Fatalf("same ID, other amount: %v", err)
	}
	if _, err := svc.PlaceHold(ctx, uuid.NewString(), user, "USDT", d("9750.000001"), "risk@example.com", "too much"); !apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE") {
		t.Fatalf("more than available: %v", err)
	}
	if _, err := svc.PlaceHold(ctx, uuid.NewString(), user, "USDT", d("1"), "risk@example.com", " "); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("no reason: %v", err)
	}
	list, err := svc.Holds(ctx, user, true)
	if err != nil || len(list) != 1 || list[0].ID != id {
		t.Fatalf("active holds: %+v %v", list, err)
	}

	released, err := svc.ReleaseHold(ctx, id, "ops@example.com", "cleared by the bank")
	if err != nil || released.Active() || released.ReleasedBy != "ops@example.com" || released.ReleaseJournalID == "" {
		t.Fatalf("release: %+v %v", released, err)
	}
	if _, err := svc.ReleaseHold(ctx, id, "ops@example.com", "cleared by the bank"); !apperr.Is(err, "LEDGER_HOLD_RELEASED") {
		t.Fatalf("released twice: %v", err)
	}
	if _, err := svc.ReleaseHold(ctx, uuid.NewString(), "ops@example.com", "nothing"); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("an unknown hold: %v", err)
	}
	if av, fr := usdt(t, svc, user, domain.AccountSpot); !av.Equal(d("10000")) || !fr.IsZero() {
		t.Fatalf("after the release: %s/%s", av, fr)
	}
	if list, _ := svc.Holds(ctx, user, true); len(list) != 0 {
		t.Fatalf("no active hold left: %+v", list)
	}
	if list, _ := svc.Holds(ctx, user, false); len(list) != 1 || list[0].ReleaseReason != "cleared by the bank" {
		t.Fatalf("every hold: %+v", list)
	}
	if n := count(t, db, `SELECT count(*) FROM journals WHERE entry_type IN ('ADMIN_FREEZE', 'ADMIN_UNFREEZE')`); n != 2 {
		t.Fatalf("hold journals: %d", n)
	}
	if n := count(t, db, `SELECT count(*) FROM outbox WHERE event_type = 'audit.AdminActionPerformed'`); n != 2 {
		t.Fatalf("hold audits: %d", n)
	}

	// A hold whose frozen funds were partly released elsewhere cannot be
	// released in full; the operators' forced release returns the amount
	// they worked out is still the hold's, never what an order holds
	// (C5.5 ⑧, ⑯).
	stuck := uuid.NewString()
	if _, err := svc.PlaceHold(ctx, stuck, user, "USDT", d("100"), "risk@example.com", "chargeback under review"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Freeze(ctx, "order-live", domain.EntryOrderFreeze, user, domain.AccountSpot, "USDT", d("30"), "a live order"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Unfreeze(ctx, "stray", domain.EntryOrderUnfreeze, user, domain.AccountSpot, "USDT", d("40"), "a stray unfreeze"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReleaseHold(ctx, stuck, "ops@example.com", "cleared by the bank"); !apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE") {
		t.Fatalf("a stuck hold released in full: %v", err)
	}
	if h, acc, err := svc.Hold(ctx, stuck); err != nil || h == nil || !acc.Frozen.Equal(d("90")) {
		t.Fatalf("the hold and its account: %+v %+v %v", h, acc, err)
	}
	if _, err := svc.ForceReleaseHold(ctx, stuck, "ops@example.com", "more than the hold", d("101")); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("more than the hold: %v", err)
	}
	// 90 frozen, 30 of it the live order's: the hold's 60.
	forced, err := svc.ForceReleaseHold(ctx, stuck, "ops@example.com", "60 left frozen, released by hand", d("60"))
	if err != nil || forced.Active() || forced.ReleaseJournalID == "" {
		t.Fatalf("forced release: %+v %v", forced, err)
	}
	if av, fr := usdt(t, svc, user, domain.AccountSpot); !av.Equal(d("9970")) || !fr.Equal(d("30")) {
		t.Fatalf("after the forced release, the order's 30 still frozen: %s/%s", av, fr)
	}
	if _, err := svc.ForceReleaseHold(ctx, stuck, "ops@example.com", "again", d("1")); !apperr.Is(err, "LEDGER_HOLD_RELEASED") {
		t.Fatalf("forced twice: %v", err)
	}
	if _, err := svc.Unfreeze(ctx, "order-live-canceled", domain.EntryOrderUnfreeze, user, domain.AccountSpot, "USDT", d("30"), "the order canceled"); err != nil {
		t.Fatalf("the order's own release: %v", err)
	}

	// The admin console adjusts the FUTURES account too.
	res, err := svc.AdjustApproved(ctx, "approval:f1", user, domain.AccountFutures, "USDT", d("15"), "fin@example.com", "goodwill on fees")
	if err != nil || res.Replayed {
		t.Fatalf("futures adjustment: %+v %v", res, err)
	}
	if av, _ := usdt(t, svc, user, domain.AccountFutures); !av.Equal(d("15")) {
		t.Fatalf("futures balance: %s", av)
	}
	if _, err := svc.AdjustApproved(ctx, "approval:f1", user, domain.AccountSpot, "USDT", d("15"), "fin@example.com", "goodwill on fees"); !apperr.Is(err, apperr.CodeIdempotencyConflict) {
		t.Fatalf("same key, other account: %v", err)
	}
	if _, err := svc.AdjustApproved(ctx, "approval:f2", user, "MARGIN", "USDT", d("1"), "fin@example.com", "no such account"); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("an unknown account type: %v", err)
	}
	if _, err := svc.AdjustApproved(ctx, "approval:f3", user, domain.AccountFutures, "USDT", d("-15.000001"), "fin@example.com", "too much"); !apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE") {
		t.Fatalf("debit beyond the balance: %v", err)
	}

	results, err := store.Reconcile(ctx, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if len(r.Mismatches) != 0 {
			t.Fatalf("%s: %+v", r.Check, r.Mismatches)
		}
	}
}
