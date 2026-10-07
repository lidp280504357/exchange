package postgres_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/ledger/adapters/postgres"
	"github.com/skill/exchange/internal/ledger/application"
	"github.com/skill/exchange/internal/ledger/domain"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/migrations"
)

type decimals map[string]int32

func (d decimals) Decimals(_ context.Context, asset string) (int32, error) {
	n, ok := d[asset]
	if !ok {
		return 0, apperr.NotFound("no such asset")
	}
	return n, nil
}

type eligibility struct{ reason string }

func (e eligibility) Check(context.Context, string, string) (bool, string, error) {
	return e.reason == "", e.reason, nil
}

type allFlags bool

func (f allFlags) Enabled(string, flags.Subject) bool { return bool(f) }

func setup(t *testing.T) (*application.Service, *postgres.Store, *pg.DB) {
	t.Helper()
	db := testenv.Postgres(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Ledger(), log); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(db, event.NewFactory("ledger-service", "test"))
	svc := &application.Service{
		Store: store, Assets: decimals{"USDT": 6, "BTC": 8}, Eligibility: eligibility{}, Flags: allFlags(true),
		Log: log, Now: time.Now,
	}
	if err := svc.SeedWelcomeCredits(ctx, []application.Credit{
		{Asset: "USDT", Amount: decimal.NewFromInt(10000)}, {Asset: "BTC", Amount: decimal.RequireFromString("0.1")},
	}); err != nil {
		t.Fatal(err)
	}
	return svc, store, db
}

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// usdt returns a user's USDT balances in one account type.
func usdt(t *testing.T, svc *application.Service, user, accountType string) (decimal.Decimal, decimal.Decimal) {
	t.Helper()
	list, err := svc.Balances(context.Background(), user, accountType)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range list {
		if a.Key.Asset == "USDT" {
			return a.Available, a.Frozen
		}
	}
	return decimal.Zero, decimal.Zero
}

func count(t *testing.T, db *pg.DB, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPostingsAreIdempotent(t *testing.T) {
	svc, store, db := setup(t)
	ctx := context.Background()
	user := uuid.NewString()

	registered := uuid.NewString()
	if err := svc.OnUserRegistered(ctx, registered, user, "SG"); err != nil {
		t.Fatal(err)
	}
	if err := svc.OnUserRegistered(ctx, registered, user, "SG"); err != nil { // redelivered
		t.Fatal(err)
	}
	if av, _ := usdt(t, svc, user, domain.AccountSpot); !av.Equal(d("10000")) {
		t.Fatalf("welcome credit once: %s", av)
	}

	first, err := svc.Freeze(ctx, "order-1", domain.EntryOrderFreeze, user, domain.AccountSpot, "USDT", d("100"), "order 1")
	if err != nil || first.Replayed {
		t.Fatalf("freeze: %+v %v", first, err)
	}
	again, err := svc.Freeze(ctx, "order-1", domain.EntryOrderFreeze, user, domain.AccountSpot, "USDT", d("100"), "order 1")
	if err != nil || !again.Replayed || again.JournalID != first.JournalID {
		t.Fatalf("replay: %+v %v", again, err)
	}
	_, err = svc.Freeze(ctx, "order-1", domain.EntryOrderFreeze, user, domain.AccountSpot, "USDT", d("200"), "order 1")
	if !apperr.Is(err, apperr.CodeIdempotencyConflict) {
		t.Fatalf("same key, other amount: %v", err)
	}
	if av, fr := usdt(t, svc, user, domain.AccountSpot); !av.Equal(d("9900")) || !fr.Equal(d("100")) {
		t.Fatalf("after freeze: %s/%s", av, fr)
	}

	_, err = svc.Freeze(ctx, "order-2", domain.EntryOrderFreeze, user, domain.AccountSpot, "USDT", d("9900.000001"), "")
	if !apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE") {
		t.Fatalf("overdraft: %v", err)
	}
	_, err = svc.Freeze(ctx, "order-3", domain.EntryOrderFreeze, user, domain.AccountSpot, "USDT", d("1.0000001"), "")
	if !apperr.Is(err, "LEDGER_AMOUNT_PRECISION") {
		t.Fatalf("precision: %v", err)
	}
	if _, err := svc.Unfreeze(ctx, "order-1-cancel", domain.EntryOrderUnfreeze, user, domain.AccountSpot, "USDT", d("100"), "cancel"); err != nil {
		t.Fatal(err)
	}
	if av, fr := usdt(t, svc, user, domain.AccountSpot); !av.Equal(d("10000")) || !fr.IsZero() {
		t.Fatalf("after unfreeze: %s/%s", av, fr)
	}

	// Events: one EntryPosted per journal, one BalanceChanged per user account touched.
	if n := count(t, db, `SELECT count(*) FROM outbox WHERE event_type = 'ledger.EntryPosted'`); n != 3 {
		t.Fatalf("EntryPosted: %d", n)
	}
	if n := count(t, db, `SELECT count(*) FROM outbox WHERE event_type = 'ledger.BalanceChanged'`); n != 4 {
		t.Fatalf("BalanceChanged: %d", n)
	}

	entries, next, err := svc.Entries(ctx, user, "USDT", "", 0, 2)
	if err != nil || len(entries) != 2 || next == 0 || entries[0].EntryType != domain.EntryOrderUnfreeze {
		t.Fatalf("entries: %+v %d %v", entries, next, err)
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
	// The admin console reads the runs back: the latest of every check.
	latest, _, err := store.ReconciliationRuns(ctx, 5)
	if err != nil || len(latest) != len(results) || latest[0].Details == nil {
		t.Fatalf("runs: %d of %d checks, %v", len(latest), len(results), err)
	}
}

func TestTransfers(t *testing.T) {
	svc, _, db := setup(t)
	ctx := context.Background()
	user := uuid.NewString()
	if err := svc.OnUserRegistered(ctx, uuid.NewString(), user, "SG"); err != nil {
		t.Fatal(err)
	}
	in := application.TransferInput{UserID: user, IdemKey: "t-1", Asset: "USDT", Amount: d("2500"), From: domain.AccountSpot, To: domain.AccountFutures}
	tr, err := svc.Transfer(ctx, in)
	if err != nil || tr.Status != domain.TransferCompleted || tr.JournalID == "" {
		t.Fatalf("transfer: %+v %v", tr, err)
	}
	again, err := svc.Transfer(ctx, in)
	if err != nil || again.ID != tr.ID {
		t.Fatalf("replay: %+v %v", again, err)
	}
	in.Amount = d("1")
	if _, err := svc.Transfer(ctx, in); !apperr.Is(err, apperr.CodeIdempotencyConflict) {
		t.Fatalf("same key, other body: %v", err)
	}
	if av, _ := usdt(t, svc, user, domain.AccountFutures); !av.Equal(d("2500")) {
		t.Fatalf("futures: %s", av)
	}

	// Too much: the failure is kept and replayed as is.
	big := application.TransferInput{UserID: user, IdemKey: "t-2", Asset: "USDT", Amount: d("7500.000001"), From: domain.AccountSpot, To: domain.AccountFutures}
	failed, err := svc.Transfer(ctx, big)
	if !apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE") || failed.Status != domain.TransferFailed {
		t.Fatalf("overdraft: %+v %v", failed, err)
	}
	if _, err := svc.Transfer(ctx, big); !apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE") {
		t.Fatalf("overdraft replay: %v", err)
	}
	if n := count(t, db, `SELECT count(*) FROM transfers WHERE user_id = $1`, user); n != 2 {
		t.Fatalf("transfers kept: %d", n)
	}
	if n := count(t, db, `SELECT count(*) FROM outbox WHERE event_type = 'account.AccountTransferFailed'`); n != 1 {
		t.Fatalf("failed events: %d", n)
	}
	list, next, err := svc.Transfers(ctx, user, "", 1)
	if err != nil || len(list) != 1 || list[0].Status != domain.TransferFailed || next == "" {
		t.Fatalf("list: %+v %q %v", list, next, err)
	}

	blocked := setupWith(t, svc, eligibility{reason: "USER_FROZEN"})
	if _, err := blocked.Transfer(ctx, application.TransferInput{
		UserID: user, IdemKey: "t-3", Asset: "USDT", Amount: d("1"),
		From: domain.AccountFutures, To: domain.AccountSpot,
	}); !apperr.Is(err, "USER_FROZEN") {
		t.Fatalf("frozen: %v", err)
	}
	if _, err := svc.Transfer(ctx, application.TransferInput{
		UserID: user, IdemKey: "t-4", Asset: "DOGE", Amount: d("1"),
		From: domain.AccountFutures, To: domain.AccountSpot,
	}); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("unknown asset: %v", err)
	}
}

// TestEntriesAcrossAccounts pages a user's lines over its accounts newest
// first: the query reads each account's newest lines and merges them by
// id, so a page holds the newest of all and the next one goes on where it
// stopped; the filters apply before a page is cut.
func TestEntriesAcrossAccounts(t *testing.T) {
	svc, _, _ := setup(t)
	ctx := context.Background()
	user := uuid.NewString()
	if err := svc.OnUserRegistered(ctx, uuid.NewString(), user, "SG"); err != nil {
		t.Fatal(err)
	}
	// Another user's lines in between are not this one's.
	other := uuid.NewString()
	if err := svc.OnUserRegistered(ctx, uuid.NewString(), other, "SG"); err != nil {
		t.Fatal(err)
	}
	moves := [][2]string{
		{domain.AccountSpot, domain.AccountFutures},
		{domain.AccountFutures, domain.AccountSpot},
		{domain.AccountSpot, domain.AccountFutures},
		{domain.AccountSpot, domain.AccountFutures},
	}
	for i, m := range moves {
		for _, who := range []string{user, other} {
			in := application.TransferInput{UserID: who, IdemKey: "move-" + string(rune('a'+i)), Asset: "USDT", Amount: d("10"), From: m[0], To: m[1]}
			if _, err := svc.Transfer(ctx, in); err != nil {
				t.Fatal(err)
			}
		}
	}
	// A trade against HOUSE (ADR-0015) shows and filters as TRADE_SETTLE.
	house := domain.Posting{IdemKey: "house-trade-" + user, EntryType: domain.EntryHouseTradeSettle, Lines: []domain.Line{
		{Account: domain.UserAccount(user, domain.AccountSpot, "USDT"), Amount: d("-3"), Kind: domain.Available},
		{Account: domain.SystemAccount(domain.AccountMarketMaker, "USDT"), Amount: d("3"), Kind: domain.Available},
	}}
	if _, err := svc.Post(ctx, house); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Freeze(ctx, "entries-order", domain.EntryOrderFreeze, user, domain.AccountSpot, "USDT", d("5"), ""); err != nil {
		t.Fatal(err)
	}

	all, next, err := svc.Entries(ctx, user, "", "", 0, 200)
	if err != nil || next != 0 {
		t.Fatalf("entries: %d, next %d, %v", len(all), next, err)
	}
	transfers, types := 0, map[string]bool{}
	for i, e := range all {
		if i > 0 && e.ID >= all[i-1].ID {
			t.Fatalf("not newest first at %d: %d after %d", i, e.ID, all[i-1].ID)
		}
		if e.EntryType == domain.EntryAccountTransfer {
			transfers++
		}
		types[e.AccountType] = true
	}
	// Four transfers of two lines each, the trade's, the freeze's two lines
	// and the welcome credit's (USDT and BTC).
	if transfers != 8 || !types[domain.AccountSpot] || !types[domain.AccountFutures] || all[0].EntryType != domain.EntryOrderFreeze {
		t.Fatalf("lines: %d transfer lines of %d, account types %v, newest %s", transfers, len(all), types, all[0].EntryType)
	}

	var paged []domain.Entry
	var before int64
	for range len(all) {
		page, next, err := svc.Entries(ctx, user, "", "", before, 3)
		if err != nil {
			t.Fatal(err)
		}
		paged = append(paged, page...)
		if next == 0 {
			break
		}
		before = next
	}
	if len(paged) != len(all) {
		t.Fatalf("pages of 3 hold %d lines, one page %d", len(paged), len(all))
	}
	for i := range all {
		if paged[i].ID != all[i].ID {
			t.Fatalf("page line %d: %d, want %d", i, paged[i].ID, all[i].ID)
		}
	}

	only, _, err := svc.Entries(ctx, user, "", domain.EntryAccountTransfer, 0, 3)
	if err != nil || len(only) != 3 {
		t.Fatalf("transfers only: %+v %v", only, err)
	}
	for _, e := range only {
		if e.EntryType != domain.EntryAccountTransfer {
			t.Fatalf("type filter let through %s", e.EntryType)
		}
	}
	// The filtered pages read journal_line_types (B141): pages of 3 go on
	// where the last stopped.
	var transfersPaged int
	before = 0
	for range len(all) {
		page, next, err := svc.Entries(ctx, user, "", domain.EntryAccountTransfer, before, 3)
		if err != nil {
			t.Fatal(err)
		}
		for i, e := range page {
			if e.EntryType != domain.EntryAccountTransfer || (i > 0 && e.ID >= page[i-1].ID) || (before > 0 && e.ID >= before) {
				t.Fatalf("transfer page after %d: %+v", before, page)
			}
		}
		transfersPaged += len(page)
		if next == 0 {
			break
		}
		before = next
	}
	if transfersPaged != transfers {
		t.Fatalf("transfer pages hold %d lines, want %d", transfersPaged, transfers)
	}
	trades, _, err := svc.Entries(ctx, user, "", domain.EntryTradeSettle, 0, 10)
	if err != nil || len(trades) != 1 || trades[0].EntryType != domain.EntryTradeSettle || !trades[0].Amount.Equal(d("-3")) {
		t.Fatalf("trades against HOUSE as TRADE_SETTLE: %+v %v", trades, err)
	}
	// The welcome credit's BTC line is the only BTC one.
	want := 0
	for _, e := range all {
		if e.Asset == "BTC" {
			want++
		}
	}
	btc, _, err := svc.Entries(ctx, user, "BTC", "", 0, 10)
	if err != nil || len(btc) != want {
		t.Fatalf("BTC: %+v, want %d lines, %v", btc, want, err)
	}
	for _, e := range btc {
		if e.Asset != "BTC" {
			t.Fatalf("asset filter let through %s", e.Asset)
		}
	}
}

func setupWith(t *testing.T, base *application.Service, e eligibility) *application.Service {
	t.Helper()
	return &application.Service{
		Store: base.Store, Assets: base.Assets, Eligibility: e, Flags: base.Flags, Futures: base.Futures, Log: base.Log, Now: base.Now,
		Runs: base.Runs,
	}
}

// TestConcurrentFreezes races freezes against one balance: exactly the
// affordable ones succeed and the ledger stays consistent.
func TestConcurrentFreezes(t *testing.T) {
	svc, store, _ := setup(t)
	ctx := context.Background()
	user := uuid.NewString()
	if err := svc.OnUserRegistered(ctx, uuid.NewString(), user, "SG"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, refused := 0, 0
	for i := range 30 {
		wg.Go(func() {
			// 30 x 400 = 12000 > 10000: exactly 25 fit.
			_, err := svc.Freeze(ctx, uuid.NewString(), domain.EntryOrderFreeze, user, domain.AccountSpot, "USDT", d("400"), "")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE"):
				refused++
			default:
				t.Errorf("freeze %d: %v", i, err)
			}
		})
	}
	wg.Wait()
	av, fr := usdt(t, svc, user, domain.AccountSpot)
	if ok != 25 || refused != 5 || !av.IsZero() || !fr.Equal(d("10000")) {
		t.Fatalf("ok %d refused %d, balances %s/%s", ok, refused, av, fr)
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
