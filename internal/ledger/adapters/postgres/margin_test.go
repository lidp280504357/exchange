package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/ledger/domain"
	"github.com/skill/exchange/internal/platform/apperr"
)

// margin returns a margin account's row of an asset (zero without one).
func marginRow(t *testing.T, list []domain.Account, key domain.AccountKey) domain.Account {
	t.Helper()
	for _, a := range list {
		if a.Key == key {
			return a
		}
	}
	return domain.Account{Key: key}
}

func TestMarginPostings(t *testing.T) {
	svc, store, _ := setup(t)
	ctx := context.Background()
	user := uuid.NewString()
	if err := svc.OnUserRegistered(ctx, uuid.NewString(), user, "SG"); err != nil {
		t.Fatal(err)
	}
	cross := domain.MarginRef{UserID: user, AccountType: domain.AccountMarginCross}
	iso := domain.MarginRef{UserID: user, AccountType: domain.AccountMarginIsolated, Scope: "BTC-USDT"}
	post := func(key string, a domain.MarginRef, moves ...domain.MarginMove) ([]string, error) {
		t.Helper()
		res, err := svc.PostMargin(ctx, domain.MarginRequest{IdemKey: key, Account: a, Reference: key, Moves: moves})
		return res.Journals, err
	}
	move := func(typ, asset, amount string) domain.MarginMove {
		return domain.MarginMove{Type: typ, Asset: asset, Amount: d(amount)}
	}
	rows := func() []domain.Account {
		t.Helper()
		list, err := svc.MarginBalances(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		return list
	}

	if _, err := post("in", cross, move(domain.MarginTransferIn, "USDT", "1000")); err != nil {
		t.Fatal(err)
	}
	if av, _ := usdt(t, svc, user, domain.AccountSpot); !av.Equal(d("9000")) {
		t.Fatalf("SPOT after the transfer in %s", av)
	}
	// Borrow 2000 with the first hour, 0.02.
	first, err := post("b1", cross, move(domain.MarginBorrow, "USDT", "2000"), move(domain.MarginInterest, "USDT", "0.02"))
	if err != nil || len(first) != 2 {
		t.Fatalf("borrow %v %v", first, err)
	}
	again, err := svc.PostMargin(ctx, domain.MarginRequest{
		IdemKey: "b1", Account: cross, Reference: "b1",
		Moves: []domain.MarginMove{move(domain.MarginBorrow, "USDT", "2000"), move(domain.MarginInterest, "USDT", "0.02")},
	})
	if err != nil || !again.Replayed || again.Journals[0] != first[0] {
		t.Fatalf("replay %+v %v", again, err)
	}
	if _, err := post("b1", cross, move(domain.MarginBorrow, "USDT", "3000")); !apperr.Is(err, apperr.CodeIdempotencyConflict) {
		t.Fatalf("a reused key: %v", err)
	}
	list := rows()
	if a := marginRow(t, list, cross.Assets("USDT")); !a.Available.Equal(d("3000")) {
		t.Fatalf("assets %s", a.Available)
	}
	if debt, interest := marginRow(t, list, cross.DebtRow("USDT")), marginRow(t, list, cross.InterestRow("USDT")); !debt.Available.Equal(d("-2000")) ||
		!interest.Available.Equal(d("-0.02")) {
		t.Fatalf("debt %s, interest %s", debt.Available, interest.Available)
	}
	// Out: never what the debt holds (3000 - 1000.01 < 2000.02).
	if _, err := post("out-big", cross, move(domain.MarginTransferOut, "USDT", "1000.01")); !apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE") {
		t.Fatalf("out of what the debt holds: %v", err)
	}
	if _, err := post("out", cross, move(domain.MarginTransferOut, "USDT", "900")); err != nil {
		t.Fatal(err)
	}
	// Repay 500, 0.02 of it interest; then more than is owed.
	if _, err := post("r1", cross, domain.MarginMove{Type: domain.MarginRepay, Asset: "USDT", Amount: d("500"), Interest: d("0.02")}); err != nil {
		t.Fatal(err)
	}
	list = rows()
	if a, debt, interest := marginRow(t, list, cross.Assets("USDT")), marginRow(t, list, cross.DebtRow("USDT")),
		marginRow(t, list, cross.InterestRow("USDT")); !a.Available.Equal(d("1600")) || !debt.Available.Equal(d("-1500.02")) ||
		!interest.Available.IsZero() {
		t.Fatalf("after repaying: %s %s %s", a.Available, debt.Available, interest.Available)
	}
	if _, err := post("r2", cross, move(domain.MarginRepay, "USDT", "1500.03")); !apperr.Is(err, "LEDGER_DEBT_OVERPAID") {
		t.Fatalf("overpaid: %v", err)
	}
	// Precision and shape are the ledger's to check too.
	if _, err := post("p", cross, move(domain.MarginBorrow, "USDT", "0.0000001")); !apperr.Is(err, "LEDGER_AMOUNT_PRECISION") {
		t.Fatalf("precision: %v", err)
	}
	if _, err := post("s", domain.MarginRef{UserID: user, AccountType: domain.AccountMarginIsolated}, move(domain.MarginBorrow, "USDT", "1")); err == nil {
		t.Fatal("an isolated account without its pair")
	}

	// The isolated account of BTC-USDT: its own rows, scoped.
	if _, err := post("iso-in", iso, move(domain.MarginTransferIn, "BTC", "0.05")); err != nil {
		t.Fatal(err)
	}
	if _, err := post("iso-b", iso, move(domain.MarginBorrow, "USDT", "1000")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.FreezeScoped(ctx, "order-iso", domain.EntryOrderFreeze, user, domain.AccountMarginIsolated, "BTC-USDT", "USDT",
		d("400"), "order"); err != nil {
		t.Fatal(err)
	}
	list = rows()
	if a := marginRow(t, list, iso.Assets("USDT")); !a.Available.Equal(d("600")) || !a.Frozen.Equal(d("400")) {
		t.Fatalf("isolated USDT %s/%s", a.Available, a.Frozen)
	}
	if a := marginRow(t, list, iso.Assets("BTC")); !a.Available.Equal(d("0.05")) {
		t.Fatalf("isolated BTC %s", a.Available)
	}
	// SPOT and FUTURES balances leave the margin rows out.
	spot, err := svc.Balances(ctx, user, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range spot {
		if domain.MarginType(a.Key.Type) {
			t.Fatalf("a margin row among the balances: %s", a.Key)
		}
	}

	// An hour's interest of USDT on both accounts, one journal.
	req := domain.InterestRequest{
		IdemKey: "USDT:" + time.Now().Truncate(time.Hour).Format(time.RFC3339), Asset: "USDT", Reference: "hour",
		Lines: []domain.InterestLine{{Account: cross, Amount: d("0.015")}, {Account: iso, Amount: d("0.01")}},
	}
	res, err := svc.AccrueInterest(ctx, req)
	if err != nil || res.Replayed {
		t.Fatalf("interest %+v %v", res, err)
	}
	if res2, err := svc.AccrueInterest(ctx, req); err != nil || !res2.Replayed || res2.JournalID != res.JournalID {
		t.Fatalf("interest again %+v %v", res2, err)
	}
	list = rows()
	if a := marginRow(t, list, iso.InterestRow("USDT")); !a.Available.Equal(d("-0.01")) {
		t.Fatalf("isolated interest %s", a.Available)
	}
	income, err := svc.SystemBalances(ctx, "USDT")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range income {
		if a.Key.Type == domain.AccountMarginInterestIncome && !a.Available.Equal(d("0.045")) {
			t.Fatalf("HOUSE's interest income %s", a.Available)
		}
	}
	debts, err := svc.MarginDebts(ctx)
	if err != nil || len(debts) != 4 {
		t.Fatalf("debts %v %v", debts, err)
	}

	// Every check holds, the margin ones included.
	results, err := store.Reconcile(ctx, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if len(r.Mismatches) > 0 {
			t.Errorf("%s: %v", r.Check, r.Mismatches)
		}
	}
}

// RepayReleased (B160): what an order that borrowed for its freeze gives
// back repays the account's debt of the asset, interest first, at most
// what is owed and what is available (to the asset's decimals), once per
// order, under the automatic repayments' key prefix with the order in the
// memo (margin-service records it from there); nothing is posted without
// a debt.
func TestRepayReleased(t *testing.T) {
	svc, _, db := setup(t)
	ctx := context.Background()
	user := uuid.NewString()
	if err := svc.OnUserRegistered(ctx, uuid.NewString(), user, "SG"); err != nil {
		t.Fatal(err)
	}
	cross := domain.MarginRef{UserID: user, AccountType: domain.AccountMarginCross}
	for _, req := range []domain.MarginRequest{
		{IdemKey: "in", Account: cross, Reference: "in", Moves: []domain.MarginMove{{Type: domain.MarginTransferIn, Asset: "USDT", Amount: d("100")}}},
		{IdemKey: "b", Account: cross, Reference: "b", Moves: []domain.MarginMove{
			{Type: domain.MarginBorrow, Asset: "USDT", Amount: d("50")}, {Type: domain.MarginInterest, Asset: "USDT", Amount: d("0.05")},
		}},
	} {
		if _, err := svc.PostMargin(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	rows := func() (assets, debt, interest decimal.Decimal) {
		t.Helper()
		list, err := svc.MarginBalances(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		return marginRow(t, list, cross.Assets("USDT")).Available, marginRow(t, list, cross.DebtRow("USDT")).Available,
			marginRow(t, list, cross.InterestRow("USDT")).Available
	}
	order := uuid.NewString()
	// The order filled 0.0001 BTC: nothing is repaid before the ledger has
	// settled its trades (B163), then it is.
	if _, _, err := svc.RepayReleased(ctx, order, user, domain.AccountMarginCross, "", "USDT", d("6"), d("0.0001"), false); !apperr.Is(err, "LEDGER_TRADES_UNSETTLED") {
		t.Fatalf("before the settlement: %v", err)
	}
	if a, debt, _ := rows(); !a.Equal(d("150")) || !debt.Equal(d("-50")) {
		t.Fatalf("repaid before the settlement: assets %s, debt %s", a, debt)
	}
	trade := func(order, status string) {
		t.Helper()
		if _, err := db.Exec(ctx, `INSERT INTO trades (trade_id, symbol, trade_number, base_asset, quote_asset, price, quantity, quote_quantity,
			buyer_order_id, buyer_user_id, seller_order_id, seller_user_id, buyer_is_maker, buyer_fee, seller_fee, event_id, executed_at, status,
			buyer_account_type) VALUES ($1, 'BTC-USDT', 0, 'BTC', 'USDT', 60000, 0.0001, 6, $2, $3, '', '', false, 0, 0, $4, now(), $5,
			'MARGIN_CROSS')`, uuid.NewString(), order, user, uuid.NewString(), status); err != nil {
			t.Fatal(err)
		}
	}
	trade(order, "SETTLED")
	// 6.0000004 down to USDT's 6 decimals; the interest first.
	res, repaid, err := svc.RepayReleased(ctx, order, user, domain.AccountMarginCross, "", "USDT", d("6.0000004"), d("0.0001"), false)
	if err != nil || res.JournalID == "" || !repaid.Equal(d("6")) {
		t.Fatalf("repay: %+v %s %v", res, repaid, err)
	}
	if a, debt, interest := rows(); !a.Equal(d("144")) || !debt.Equal(d("-44.05")) || !interest.IsZero() {
		t.Fatalf("after: assets %s, debt %s, interest %s", a, debt, interest)
	}
	var memo, entry string
	if err := db.QueryRow(ctx, `SELECT memo, entry_type FROM journals WHERE idem_key = $1`, "trade-repay:release:"+order).Scan(&memo, &entry); err != nil ||
		memo != "auto-repay order "+order+" release" || entry != domain.EntryMarginRepay {
		t.Fatalf("journal: %q %q %v", memo, entry, err)
	}
	// Once per order.
	again, repaid, err := svc.RepayReleased(ctx, order, user, domain.AccountMarginCross, "", "USDT", d("10"), decimal.Zero, false)
	if err != nil || !again.Replayed || again.JournalID != res.JournalID || !repaid.IsZero() {
		t.Fatalf("again: %+v %s %v", again, repaid, err)
	}
	// Trades parked as FAILED (B164) are waited for unless the caller says
	// the order ended long ago; trades not recorded yet are waited for all
	// the same.
	parked := uuid.NewString()
	if _, _, err := svc.RepayReleased(ctx, parked, user, domain.AccountMarginCross, "", "USDT", d("4"), d("0.0002"), true); !apperr.Is(err, "LEDGER_TRADES_UNSETTLED") {
		t.Fatalf("nothing recorded yet: %v", err)
	}
	trade(parked, "SETTLED")
	trade(parked, "FAILED")
	if _, _, err := svc.RepayReleased(ctx, parked, user, domain.AccountMarginCross, "", "USDT", d("4"), d("0.0002"), false); !apperr.Is(err, "LEDGER_TRADES_UNSETTLED") {
		t.Fatalf("a trade parked as FAILED: %v", err)
	}
	if _, repaid, err := svc.RepayReleased(ctx, parked, user, domain.AccountMarginCross, "", "USDT", d("4"), d("0.0002"), true); err != nil || !repaid.Equal(d("4")) {
		t.Fatalf("past the trade parked as FAILED: %s %v", repaid, err)
	}
	// At most what is owed.
	if _, repaid, err := svc.RepayReleased(ctx, uuid.NewString(), user, domain.AccountMarginCross, "", "USDT", d("1000"), decimal.Zero, false); err != nil || !repaid.Equal(d("40.05")) {
		t.Fatalf("all of it: %s %v", repaid, err)
	}
	if a, debt, _ := rows(); !a.Equal(d("99.95")) || !debt.IsZero() {
		t.Fatalf("paid off: assets %s, debt %s", a, debt)
	}
	// Nothing owed: nothing posted, and the order may still repay later.
	none, repaid, err := svc.RepayReleased(ctx, uuid.NewString(), user, domain.AccountMarginCross, "", "USDT", d("5"), decimal.Zero, false)
	if err != nil || none.JournalID != "" || !repaid.IsZero() {
		t.Fatalf("nothing owed: %+v %s %v", none, repaid, err)
	}
	if _, _, err := svc.RepayReleased(ctx, "not-a-uuid", user, domain.AccountMarginCross, "", "USDT", d("1"), decimal.Zero, false); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("a bad order id: %v", err)
	}
}
