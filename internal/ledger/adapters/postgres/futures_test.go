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

func systemBalance(t *testing.T, svcBalances func() ([]domain.Account, error), accountType string) decimal.Decimal {
	t.Helper()
	list, err := svcBalances()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range list {
		if a.Key.Type == accountType {
			return a.Available
		}
	}
	return decimal.Zero
}

func TestFuturesSettlement(t *testing.T) {
	svc, store, _ := setup(t)
	ctx := context.Background()
	long, short := uuid.NewString(), uuid.NewString()
	system := func() ([]domain.Account, error) { return svc.SystemBalances(ctx, "USDT") }
	for _, u := range []string{long, short} {
		if err := svc.OnUserRegistered(ctx, uuid.NewString(), u, "SG"); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Transfer(ctx, transferInput(u, "futures-in", "1000", domain.AccountSpot, domain.AccountFutures)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.FundInsurance(ctx, "seed", "USDT", d("50"), "test", "seed the fund"); err != nil {
		t.Fatal(err)
	}

	// Both place an opening order: 100 of margin and a fee reserve of 1
	// frozen.
	for _, u := range []string{long, short} {
		if _, err := svc.Freeze(ctx, "order-"+u, domain.EntryOrderFreeze, u, domain.AccountFutures, "USDT", d("101"), "order"); err != nil {
			t.Fatal(err)
		}
	}
	// The fills: each pays a 0.4 fee out of its 1 reserve and keeps the
	// 100 as position margin.
	open := func(u, side string) {
		t.Helper()
		res, err := svc.SettleFutures(ctx, domain.FuturesRequest{
			IdemKey: "fill:t1:" + side, UserID: u, Asset: "USDT", Reference: "BTC-USDT-PERP trade t1",
			Moves: []domain.FuturesMove{
				{Type: domain.MoveFee, Amount: d("0.4"), Kind: domain.Frozen},
				{Type: domain.MoveUnfreeze, Amount: d("0.6")},
			},
		})
		if err != nil || res.Replayed || res.Outcomes[0].JournalID == "" || !res.Outcomes[0].User.Equal(d("-0.4")) {
			t.Fatalf("open %s: %+v %v", side, res, err)
		}
	}
	open(long, "B")
	open(short, "S")
	if av, fr := usdt(t, svc, long, domain.AccountFutures); !av.Equal(d("899.6")) || !fr.Equal(d("100")) {
		t.Fatalf("after opening: %s/%s", av, fr)
	}

	// The ledger takes derivatives-service's amounts as given: the long,
	// cross, closes 30 up; the short, isolated, closes 130 down with 100 of
	// margin, so the insurance fund pays 30 and the fee is waived.
	closeLong := domain.FuturesRequest{
		IdemKey: "fill:t2:S", UserID: long, Asset: "USDT", Reference: "BTC-USDT-PERP trade t2",
		Moves: []domain.FuturesMove{
			{Type: domain.MoveUnfreeze, Amount: d("100")},
			{Type: domain.MoveProfit, Amount: d("30")},
			{Type: domain.MoveFee, Amount: d("0.5")},
		},
	}
	if _, err := svc.SettleFutures(ctx, closeLong); err != nil {
		t.Fatal(err)
	}
	zero := decimal.Zero
	closeShort := domain.FuturesRequest{
		IdemKey: "fill:t2:B", UserID: short, Asset: "USDT", Reference: "BTC-USDT-PERP trade t2",
		Moves: []domain.FuturesMove{
			{Type: domain.MoveLoss, Amount: d("130"), Kind: domain.Frozen, Limit: ptr(d("100"))},
			{Type: domain.MoveFee, Amount: d("0.5"), Kind: domain.Frozen, Limit: &zero},
		},
	}
	res, err := svc.SettleFutures(ctx, closeShort)
	if err != nil || !res.Outcomes[0].Insurance.Equal(d("30")) || !res.Outcomes[1].Waived.Equal(d("0.5")) || res.Outcomes[1].JournalID != "" {
		t.Fatalf("close short: %+v %v", res, err)
	}
	// A repeat returns the first outcome; other content under the key is
	// refused.
	again, err := svc.SettleFutures(ctx, closeShort)
	if err != nil || !again.Replayed || again.Outcomes[0].JournalID != res.Outcomes[0].JournalID {
		t.Fatalf("replay: %+v %v", again, err)
	}
	closeShort.Moves[0].Amount = d("131")
	if _, err := svc.SettleFutures(ctx, closeShort); !apperr.Is(err, apperr.CodeIdempotencyConflict) {
		t.Fatalf("another request under the key: %v", err)
	}
	if av, fr := usdt(t, svc, long, domain.AccountFutures); !av.Equal(d("1029.1")) || !fr.IsZero() {
		t.Fatalf("the long after closing: %s/%s", av, fr)
	}
	if av, fr := usdt(t, svc, short, domain.AccountFutures); !av.Equal(d("899.6")) || !fr.IsZero() {
		t.Fatalf("the short after closing: %s/%s", av, fr)
	}
	// PNL_CLEARING paid 30 and got 130 (100 from the short, 30 from the
	// insurance fund).
	if got := systemBalance(t, system, domain.AccountPnLClearing); !got.Equal(d("100")) {
		t.Fatalf("PNL_CLEARING %s", got)
	}
	if got := systemBalance(t, system, domain.AccountInsuranceFund); !got.Equal(d("20")) {
		t.Fatalf("INSURANCE_FUND %s", got)
	}

	// The insurance fund cannot cover another 25: nothing is booked.
	_, err = svc.SettleFutures(ctx, domain.FuturesRequest{
		IdemKey: "fill:t3:B", UserID: short, Asset: "USDT", Reference: "trade t3",
		Moves: []domain.FuturesMove{{Type: domain.MoveLoss, Amount: d("25"), Kind: domain.Frozen}},
	})
	if apperr.From(err).Code != domain.ErrInsufficientBalance.Code {
		t.Fatalf("an empty insurance fund: %v", err)
	}

	// Funding: the long pays 2 from available, the short receives 1.9;
	// FUNDING_CLEARING keeps 0.1.
	for _, f := range []struct {
		user, key string
		move      domain.FuturesMove
	}{
		{long, "funding:BTC-USDT-PERP:1759219200:p1", domain.FuturesMove{Type: domain.MoveFundingPay, Amount: d("2")}},
		{short, "funding:BTC-USDT-PERP:1759219200:p2", domain.FuturesMove{Type: domain.MoveFundingReceive, Amount: d("1.9")}},
	} {
		if _, err := svc.SettleFutures(ctx, domain.FuturesRequest{IdemKey: f.key, UserID: f.user, Asset: "USDT", Moves: []domain.FuturesMove{f.move}}); err != nil {
			t.Fatal(err)
		}
	}
	if got := systemBalance(t, system, domain.AccountFundingClearing); !got.Equal(d("0.1")) {
		t.Fatalf("FUNDING_CLEARING %s", got)
	}
	if m := mismatches(t, store); len(m) != 0 {
		t.Fatalf("reconciliation: %v", m)
	}
}

func ptr(v decimal.Decimal) *decimal.Decimal { return &v }

func transferInput(user, key, amount, from, to string) application.TransferInput {
	return application.TransferInput{UserID: user, IdemKey: key, Asset: "USDT", Amount: d(amount), From: from, To: to}
}

// GAS_SUPPLY is funded out of fee revenue for the custodian's fees
// (ADR-0011): no more than the revenue, once per key, audited.
func TestFundGasSupplyFromFeeRevenue(t *testing.T) {
	svc, _, _ := setup(t)
	ctx := context.Background()
	system := func() ([]domain.Account, error) { return svc.SystemBalances(ctx, "USDT") }
	u := uuid.NewString()
	if err := svc.OnUserRegistered(ctx, uuid.NewString(), u, "SG"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Transfer(ctx, transferInput(u, "futures-in", "10", domain.AccountSpot, domain.AccountFutures)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Freeze(ctx, "order-fee", domain.EntryOrderFreeze, u, domain.AccountFutures, "USDT", d("1"), "order"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SettleFutures(ctx, domain.FuturesRequest{
		IdemKey: "fill:fee", UserID: u, Asset: "USDT", Reference: "a fee",
		Moves: []domain.FuturesMove{{Type: domain.MoveFee, Amount: d("0.4"), Kind: domain.Frozen}, {Type: domain.MoveUnfreeze, Amount: d("0.6")}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.FundGasSupply(ctx, "too-much", "USDT", d("0.5"), "ops", "the custodian's fees"); err == nil {
		t.Fatal("more than the fee revenue")
	}
	if _, err := svc.FundGasSupply(ctx, "k1", "USDT", d("0.3"), "ops", ""); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("no reason: %v", err)
	}
	res, err := svc.FundGasSupply(ctx, "k1", "USDT", d("0.3"), "ops", "the custodian's fees")
	if err != nil || res.Replayed {
		t.Fatalf("%+v %v", res, err)
	}
	if again, err := svc.FundGasSupply(ctx, "k1", "USDT", d("0.3"), "ops", "the custodian's fees"); err != nil || !again.Replayed {
		t.Fatalf("a repeat: %+v %v", again, err)
	}
	if gas, rev := systemBalance(t, system, domain.AccountGasSupply), systemBalance(t, system, domain.AccountFeeRevenue); !gas.Equal(d("0.3")) ||
		!rev.Equal(d("0.1")) {
		t.Fatalf("GAS_SUPPLY %s, FEE_REVENUE %s", gas, rev)
	}
}
