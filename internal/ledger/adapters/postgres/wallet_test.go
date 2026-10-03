package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/lidp280504357/exchange/internal/ledger/domain"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

func TestWalletJournals(t *testing.T) {
	svc, store, _ := setup(t)
	ctx := context.Background()
	payer, payee := uuid.NewString(), uuid.NewString()
	if err := svc.OnUserRegistered(ctx, uuid.NewString(), payer, "SG"); err != nil {
		t.Fatal(err)
	}

	// A withdrawal of 100 USDT with a 1 USDT fee: freeze, then settle.
	if _, err := svc.Freeze(ctx, "w1", domain.EntryWithdrawFreeze, payer, domain.AccountSpot, "USDT", d("101"), "w1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SettleWithdrawal(ctx, "w1", payer, "USDT", d("100"), d("1"), "w1"); err != nil {
		t.Fatal(err)
	}
	again, err := svc.SettleWithdrawal(ctx, "w1", payer, "USDT", d("100"), d("1"), "w1")
	if err != nil || !again.Replayed {
		t.Fatalf("settling again replays: %+v %v", again, err)
	}
	if avail, frozen := usdt(t, svc, payer, domain.AccountSpot); !avail.Equal(d("9899")) || !frozen.IsZero() {
		t.Fatalf("payer after the withdrawal: %s / %s", avail, frozen)
	}

	// An internal withdrawal of 50 USDT to another user.
	if _, err := svc.Freeze(ctx, "w2", domain.EntryWithdrawFreeze, payer, domain.AccountSpot, "USDT", d("50"), "w2"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TransferInternal(ctx, "w2", payer, payee, "USDT", d("50"), "w2"); err != nil {
		t.Fatal(err)
	}
	if avail, _ := usdt(t, svc, payee, domain.AccountSpot); !avail.Equal(d("50")) {
		t.Fatalf("payee: %s", avail)
	}

	// Gas needs GAS_SUPPLY funds first.
	if _, err := svc.BookChainFee(ctx, "0xgas1", "USDT", d("0.5"), "0xgas1"); err == nil {
		t.Fatal("an unfunded GAS_SUPPLY cannot pay gas")
	}
	if _, err := svc.FundSystemAccount(ctx, "0xfund", domain.AccountGasSupply, "USDT", d("2"), "0xfund"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.BookChainFee(ctx, "0xgas1", "USDT", d("0.5"), "0xgas1"); err != nil {
		t.Fatal(err)
	}

	system, err := svc.SystemBalances(ctx, "USDT")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, a := range system {
		got[a.Key.Type] = a.Available.String()
	}
	// −(DEPOSIT_PENDING + WITHDRAWAL_PENDING) = 2 − 100.5: the welcome
	// funds came from ADJUSTMENT, not from the chain.
	want := map[string]string{"WITHDRAWAL_PENDING": "100.5", "FEE_REVENUE": "1", "GAS_SUPPLY": "1.5", "DEPOSIT_PENDING": "-2", "ADJUSTMENT": "-10000"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %s, want %s (all: %v)", k, got[k], v, got)
		}
	}
	results, err := store.Reconcile(ctx, svc.Now)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if len(r.Mismatches) > 0 {
			t.Errorf("reconcile %s: %v", r.Check, r.Mismatches)
		}
	}

	// A stand-in custodian replaced (2026-10-03): its simulated deposits
	// leave the expectation for ADJUSTMENT, never below nothing; a repeat
	// of the key replays, a reverse puts them back.
	pending := func() string {
		t.Helper()
		system, err := svc.SystemBalances(ctx, "USDT")
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range system {
			if a.Key.Type == domain.AccountDepositPending {
				return a.Available.String()
			}
		}
		return ""
	}
	if _, err := svc.ResetCustody(ctx, "too-much", "USDT", d("5"), false, "ops", "more than is expected"); err == nil {
		t.Fatal("reset more than the custodians are expected to hold")
	}
	reset, err := svc.ResetCustody(ctx, "switch", "USDT", d("1.5"), false, "ops", "the stand-in's deposits")
	if err != nil || pending() != "-0.5" {
		t.Fatalf("reset %+v %v, DEPOSIT_PENDING %s", reset, err, pending())
	}
	if again, err := svc.ResetCustody(ctx, "switch", "USDT", d("1.5"), false, "ops", "the stand-in's deposits"); err != nil ||
		!again.Replayed || again.JournalID != reset.JournalID || pending() != "-0.5" {
		t.Fatalf("a repeat replays: %+v %v", again, err)
	}
	// A reverse puts back at most what the resets took (review AH).
	if _, err := svc.ResetCustody(ctx, "too-far-back", "USDT", d("2"), true, "ops", "more than was reset"); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("reversed more than was reset: %v", err)
	}
	if _, err := svc.ResetCustody(ctx, "back", "USDT", d("1.5"), true, "ops", "back to the stand-in"); err != nil || pending() != "-2" {
		t.Fatalf("reversed: %v, DEPOSIT_PENDING %s", err, pending())
	}
	if again, err := svc.ResetCustody(ctx, "back", "USDT", d("1.5"), true, "ops", "back to the stand-in"); err != nil || !again.Replayed {
		t.Fatalf("a reverse replayed: %+v %v", again, err)
	}
}
