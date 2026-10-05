package application_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/margin/application"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// TestConsole checks the console's side (review C5): terms changed over
// the version read and within the policy, the accounts riskiest first,
// one account in full, and an administrator's freeze.
func TestConsole(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	const admin = "ops@example.com"

	assets, err := r.svc.ConsoleAssets(ctx)
	if err != nil || len(assets) != 4 {
		t.Fatalf("assets %d %v", len(assets), err)
	}
	var usdt application.ConsoleAsset
	for _, a := range assets {
		if a.Terms.Asset == "USDT" {
			usdt = a
		}
	}
	if usdt.Meta.Version != 1 || usdt.Meta.UpdatedBy != "test" || !usdt.Lent.IsZero() || usdt.Borrowers != 0 {
		t.Fatalf("USDT %+v", usdt)
	}
	// Over a version that moved, out of policy, then applied.
	next := usdt.Terms
	next.UserCap = d("150000")
	if _, err := r.svc.SetAssetTerms(ctx, next, usdt.Meta.Version+1, admin); code(err) != "MARGIN_PARAMS_CHANGED" {
		t.Fatalf("a stale version: %v", err)
	}
	bad := next
	bad.Haircut = d("1.5")
	if _, err := r.svc.SetAssetTerms(ctx, bad, usdt.Meta.Version, admin); apperr.From(err).Kind != apperr.KindInvalid {
		t.Fatalf("a haircut above 1: %v", err)
	}
	bad = next
	bad.Floating.MaxRate = d("0.000001")
	if _, err := r.svc.SetAssetTerms(ctx, bad, usdt.Meta.Version, admin); apperr.From(err).Kind != apperr.KindInvalid {
		t.Fatalf("a falling curve under the fixed model: %v", err)
	}
	got, err := r.svc.SetAssetTerms(ctx, next, usdt.Meta.Version, admin)
	if err != nil || got.Meta.Version != 2 || got.Meta.UpdatedBy != admin || !got.Terms.UserCap.Equal(d("150000")) {
		t.Fatalf("set USDT %+v %v", got, err)
	}

	settings, err := r.svc.ConsoleSettings(ctx)
	if err != nil || settings.Cross.Leverage != 3 || len(settings.IsolatedDefaults) != 3 {
		t.Fatalf("settings %+v %v", settings, err)
	}
	cross := domain.DefaultTerms(domain.AccountCross, 4)
	if _, err := r.svc.SetCrossTerms(ctx, cross, settings.Meta.Version, admin); apperr.From(err).Kind != apperr.KindInvalid {
		t.Fatalf("4x cross: %v", err)
	}
	cross = domain.DefaultTerms(domain.AccountCross, 5)
	if s, err := r.svc.SetCrossTerms(ctx, cross, settings.Meta.Version, admin); err != nil || s.Cross.Leverage != 5 ||
		!s.Cross.WarnLevel.Equal(d("1.2")) {
		t.Fatalf("5x cross: %+v %v", s, err)
	}
	pairs, err := r.svc.ConsolePairs(ctx)
	if err != nil || len(pairs) != 2 {
		t.Fatalf("pairs %+v %v", pairs, err)
	}
	p := pairs[0].Pair
	p.Terms = domain.DefaultTerms(domain.AccountIsolated, 7)
	if _, err := r.svc.SetPairTerms(ctx, p, pairs[0].Meta.Version, admin); apperr.From(err).Kind != apperr.KindInvalid {
		t.Fatalf("7x isolated: %v", err)
	}
	p.Terms = domain.DefaultTerms(domain.AccountIsolated, 5)
	if c, err := r.svc.SetPairTerms(ctx, p, pairs[0].Meta.Version, admin); err != nil || c.Pair.Terms.Leverage != 5 || c.Meta.UpdatedBy != admin {
		t.Fatalf("5x isolated: %+v %v", c, err)
	}

	// Two users: one borrowing, one holding only.
	cross5 := domain.Cross()
	risky, safe := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	for _, u := range []string{risky, safe} {
		r.ledger.fund(u, "USDT", d("1000"))
		if _, err := r.svc.Transfer(ctx, application.TransferInput{
			UserID: u, IdemKey: "t", Direction: domain.DirectionIn, Account: cross5, Asset: "USDT", Amount: d("1000"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: risky, IdemKey: "b", Account: cross5, Asset: "USDT", Amount: d("2000")}); err != nil {
		t.Fatal(err)
	}
	list, truncated, err := r.svc.ConsoleAccounts(ctx, ports.AccountFilter{}, 10)
	if err != nil || truncated || len(list) < 2 || list[0].State.UserID != risky {
		t.Fatalf("accounts %+v %v %v", list, truncated, err)
	}
	if list, _, err = r.svc.ConsoleAccounts(ctx, ports.AccountFilter{UserID: safe}, 10); err != nil || len(list) != 1 ||
		list[0].View.Valuation.HasDebt() {
		t.Fatalf("one user's %+v %v", list, err)
	}
	detail, err := r.svc.ConsoleAccountDetail(ctx, risky, cross5)
	if err != nil || len(detail.Loans) != 1 || detail.Loans[0].Loan.OpenedAt.IsZero() || len(detail.Balances) != 1 {
		t.Fatalf("detail %+v %v", detail, err)
	}
	kinds := map[string]string{}
	for _, c := range detail.Changes {
		kinds[c.Kind] = c.JournalKey
	}
	if len(kinds) != 2 || kinds[ports.ChangeInterest] == "" || kinds[ports.ChangeBorrow] == kinds[ports.ChangeInterest] {
		t.Fatalf("loan changes %+v", detail.Changes)
	}
	if _, err := r.svc.ConsoleAccountDetail(ctx, uuid.Must(uuid.NewV7()).String(), cross5); code(err) != "MARGIN_ACCOUNT_NOT_FOUND" {
		t.Fatalf("no such account: %v", err)
	}

	// An administrator's freeze: no borrowing or transfers out, repaying
	// stays open; once only, lifted once.
	c, err := r.svc.Freeze(ctx, risky, cross5, admin, "checking the account")
	if err != nil || c.State.Status != domain.StatusFrozen || c.State.FrozenBy != admin || c.State.FrozenAt.IsZero() {
		t.Fatalf("freeze %+v %v", c, err)
	}
	if _, err := r.svc.Freeze(ctx, risky, cross5, admin, "again"); code(err) != "MARGIN_FROZEN" {
		t.Fatalf("frozen twice: %v", err)
	}
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: risky, IdemKey: "b-frozen", Account: cross5, Asset: "USDT", Amount: d("1")}); code(err) != "MARGIN_FROZEN" {
		t.Fatalf("borrow while frozen: %v", err)
	}
	if _, err := r.svc.Transfer(ctx, application.TransferInput{
		UserID: risky, IdemKey: "t-out", Direction: domain.DirectionOut, Account: cross5, Asset: "USDT", Amount: d("1"),
	}); code(err) != "MARGIN_FROZEN" {
		t.Fatalf("transfer out while frozen: %v", err)
	}
	if _, err := r.svc.Repay(ctx, application.RepayInput{UserID: risky, IdemKey: "r", Account: cross5, Asset: "USDT", Amount: d("10")}); err != nil {
		t.Fatalf("repay while frozen: %v", err)
	}
	if c, err = r.svc.Unfreeze(ctx, risky, cross5, admin); err != nil || c.State.Status != domain.StatusNormal || c.State.FrozenBy != "" {
		t.Fatalf("unfreeze %+v %v", c, err)
	}
	if _, err := r.svc.Unfreeze(ctx, risky, cross5, admin); code(err) != "MARGIN_NOT_FROZEN" {
		t.Fatalf("unfrozen twice: %v", err)
	}
}
