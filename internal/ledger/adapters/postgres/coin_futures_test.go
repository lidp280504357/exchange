package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/ledger/application"
	"github.com/skill/exchange/internal/ledger/domain"
	"github.com/skill/exchange/internal/platform/apperr"
)

// btcFutures is a user's FUTURES BTC: available and frozen.
func btcFutures(t *testing.T, svc *application.Service, user string) (decimal.Decimal, decimal.Decimal) {
	t.Helper()
	list, err := svc.Balances(context.Background(), user, domain.AccountFutures)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range list {
		if a.Key.Asset == "BTC" {
			return a.Available, a.Frozen
		}
	}
	return decimal.Zero, decimal.Zero
}

// TestCoinSettledFutures settles an inverse (coin-margined) contract in
// BTC, a backed asset (coin-M design 2026-10-06 §2.3): the FUTURES
// accounts, PNL_CLEARING and the insurance fund in BTC at its 8 decimals.
// A loser that cannot pay — HOUSE among them, a user account like any —
// never goes below zero: its BTC pays what it holds and the BTC insurance
// fund the rest; when the fund cannot, the whole settlement is refused
// and nothing is booked (derivatives-service parks it). An amount past
// BTC's decimals is refused.
func TestCoinSettledFutures(t *testing.T) {
	svc, store, _ := setup(t)
	ctx := context.Background()
	winner, house := uuid.NewString(), uuid.NewString()
	system := func() ([]domain.Account, error) { return svc.SystemBalances(ctx, "BTC") }
	for _, u := range []string{winner, house} {
		if err := svc.OnUserRegistered(ctx, uuid.NewString(), u, "SG"); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Transfer(ctx, application.TransferInput{
			UserID: u, IdemKey: "futures-in", Asset: "BTC", Amount: d("0.05"), From: domain.AccountSpot, To: domain.AccountFutures,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.FundInsurance(ctx, "seed-btc", "BTC", d("0.001"), "test", "seed the BTC fund"); err != nil {
		t.Fatal(err)
	}

	// The winner closes 0.01 BTC up (cross); HOUSE, cross, loses 0.0505
	// with 0.05 held: its 0.05 pays, the fund the 0.0005 left, the fee is
	// waived.
	if _, err := svc.SettleFutures(ctx, domain.FuturesRequest{
		IdemKey: "fill:c1:S", UserID: winner, Asset: "BTC", Reference: "BTC-USD-PERP trade c1",
		Moves: []domain.FuturesMove{{Type: domain.MoveProfit, Amount: d("0.01")}, {Type: domain.MoveFee, Amount: d("0.00001")}},
	}); err != nil {
		t.Fatal(err)
	}
	res, err := svc.SettleFutures(ctx, domain.FuturesRequest{
		IdemKey: "fill:c1:B", UserID: house, Asset: "BTC", Reference: "BTC-USD-PERP trade c1",
		Moves: []domain.FuturesMove{{Type: domain.MoveLoss, Amount: d("0.0505")}, {Type: domain.MoveFee, Amount: d("0.00001")}},
	})
	if err != nil || !res.Outcomes[0].User.Equal(d("-0.05")) || !res.Outcomes[0].Insurance.Equal(d("0.0005")) ||
		!res.Outcomes[1].Waived.Equal(d("0.00001")) {
		t.Fatalf("HOUSE's loss past its BTC: %+v %v", res, err)
	}
	if av, fr := btcFutures(t, svc, house); !av.IsZero() || !fr.IsZero() {
		t.Fatalf("HOUSE below zero: %s/%s", av, fr)
	}
	if av, _ := btcFutures(t, svc, winner); !av.Equal(d("0.05999")) {
		t.Fatalf("the winner %s", av)
	}
	if got := systemBalance(t, system, domain.AccountInsuranceFund); !got.Equal(d("0.0005")) {
		t.Fatalf("the BTC insurance fund %s", got)
	}
	// PNL_CLEARING paid 0.01 and got 0.0505.
	if got := systemBalance(t, system, domain.AccountPnLClearing); !got.Equal(d("0.0405")) {
		t.Fatalf("PNL_CLEARING in BTC %s", got)
	}

	// Another loss of 0.001 with nothing held and 0.0005 in the fund: the
	// settlement is refused whole, nothing booked.
	_, err = svc.SettleFutures(ctx, domain.FuturesRequest{
		IdemKey: "fill:c2:B", UserID: house, Asset: "BTC", Reference: "BTC-USD-PERP trade c2",
		Moves: []domain.FuturesMove{{Type: domain.MoveLoss, Amount: d("0.001")}},
	})
	if apperr.From(err).Code != domain.ErrInsufficientBalance.Code {
		t.Fatalf("a BTC fund too small: %v", err)
	}
	if got := systemBalance(t, system, domain.AccountInsuranceFund); !got.Equal(d("0.0005")) {
		t.Fatalf("the refused settlement moved the fund: %s", got)
	}

	// BTC has 8 decimals.
	_, err = svc.SettleFutures(ctx, domain.FuturesRequest{
		IdemKey: "fill:c3:S", UserID: winner, Asset: "BTC", Moves: []domain.FuturesMove{{Type: domain.MoveFee, Amount: d("0.000000001")}},
	})
	if apperr.From(err).Code != "LEDGER_AMOUNT_PRECISION" {
		t.Fatalf("an amount past BTC's decimals: %v", err)
	}
	if m := mismatches(t, store); len(m) != 0 {
		t.Fatalf("reconciliation: %v", m)
	}
}
