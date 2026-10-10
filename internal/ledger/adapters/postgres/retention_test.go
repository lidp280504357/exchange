package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/ledger/adapters/postgres"
	"github.com/skill/exchange/internal/ledger/domain"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/retention"
)

// The retention run (M1, ADR-0022): history from before the window goes,
// the reconciliation still finds nothing, a posting with a deleted key is a
// replay of it, the journals a check adds up in full stay, and nothing
// else may delete a journal.
func TestRetention(t *testing.T) {
	svc, store, db := setup(t)
	ctx := context.Background()
	old := time.Now().AddDate(0, 0, -20)
	svc.Now = func() time.Time { return old }
	buyer, seller := uuid.NewString(), uuid.NewString()
	fund(t, svc, buyer, domain.Credit{Asset: "USDT", Amount: d("1000"), Decimals: 6})
	fund(t, svc, seller, domain.Credit{Asset: "BTC", Amount: d("1"), Decimals: 8})
	freeze(t, svc, "order:b", buyer, "USDT", "210.3")
	freeze(t, svc, "order:s", seller, "BTC", "0.002")
	trade := domain.Trade{
		ID: uuid.NewString(), Number: 1, Symbol: "BTC-USDT", BaseAsset: "BTC", QuoteAsset: "USDT",
		Price: d("70000"), Quantity: d("0.002"), Quote: d("140"),
		BuyerOrderID: "b", BuyerUserID: buyer, SellerOrderID: "s", SellerUserID: seller,
		BuyerFee: d("0.000002"), SellerFee: d("0.14"), BuyerLimit: d("70100"),
		EventID: uuid.NewString(), ExecutedAt: old,
	}
	if res, err := svc.Settle(ctx, []domain.Trade{trade}); err != nil || res.Settled != 1 {
		t.Fatalf("settle: %+v %v", res, err)
	}
	// A custody reset is added up in full (its reversal's limit): it stays.
	reset, err := domain.AdjustmentPosting("custody-reset:test", seller, []domain.Credit{{Asset: "USDT", Amount: d("5"), Decimals: 6}}, "reset")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Post(ctx, reset); err != nil {
		t.Fatal(err)
	}
	// Past the topic's replay the trade goes too.
	if _, err := db.Exec(ctx, `UPDATE trades SET recorded_at = now() - interval '40 days'`); err != nil {
		t.Fatal(err)
	}
	svc.Now = time.Now
	freeze(t, svc, "order:b2", buyer, "USDT", "10")
	// Keys of deleted journals: an order freeze's goes after 35 days, any
	// other's after the keys' window.
	for _, k := range []struct {
		key, entry string
		days       int
	}{{"freeze-100", "ORDER_FREEZE", 100}, {"settle-100", "TRADE_SETTLE", 100}, {"release-50", "ORDER_UNFREEZE", 50}, {"settle-50", "TRADE_SETTLE", 50}} {
		if _, err := db.Exec(ctx, `INSERT INTO journal_keys VALUES ($1, '\x00', $2, 0, $3, now() - make_interval(days => $4))`,
			k.key, uuid.NewString(), k.entry, k.days); err != nil {
			t.Fatal(err)
		}
	}
	if got := mismatches(t, store); len(got) != 0 {
		t.Fatalf("reconciliation before: %v", got)
	}
	journals := count(t, db, `SELECT count(*) FROM journals`)

	rows := func(results []retention.Result) map[string]int64 {
		out := map[string]int64{}
		for _, r := range results {
			out[r.Table] += r.Rows
		}
		return out
	}
	w := retention.Window{Now: time.Now(), Days: 15, KeyDays: 90, Batch: 2, DryRun: true}
	dry, err := postgres.Retention{}.Run(ctx, db, w)
	if err != nil {
		t.Fatal(err)
	}
	// 2 funds, 2 freezes, the trade's settlement, fee and release journals.
	got := rows(dry)
	if got["journals"] < 5 || got["journal_lines"] < 2*got["journals"] || got["journal_line_types"] != got["journal_lines"] ||
		got["trades"] != 1 || got["journal_keys"] != 3 {
		t.Fatalf("dry run: %v", got)
	}
	if n := count(t, db, `SELECT count(*) FROM journals`); n != journals {
		t.Fatalf("the dry run deleted journals: %d of %d", n, journals)
	}

	w.DryRun = false
	run, err := postgres.Retention{}.Run(ctx, db, w)
	if err != nil {
		t.Fatal(err)
	}
	if done := rows(run); done["journals"] != got["journals"] || done["journal_lines"] != got["journal_lines"] ||
		done["trades"] != 1 || done["journal_keys"] != 3 {
		t.Fatalf("run: %v, the dry run counted %v", done, got)
	}
	if n := count(t, db, `SELECT count(*) FROM journals`); n != journals-int(got["journals"]) {
		t.Fatalf("%d journals left of %d", n, journals)
	}
	// A freeze's key is the user's (u:<user>:<key>).
	if count(t, db, `SELECT count(*) FROM journals WHERE idem_key = 'custody-reset:test'`) != 1 ||
		count(t, db, `SELECT count(*) FROM journals WHERE idem_key = $1`, "u:"+buyer+":order:b2") != 1 {
		t.Fatal("the custody reset and the recent freeze should stay")
	}
	if n := count(t, db, `SELECT count(*) FROM journal_keys WHERE idem_key <> 'settle-50'`); n != int(got["journals"]) {
		t.Fatalf("%d keys kept for %d deleted journals", n, got["journals"])
	}
	if count(t, db, `SELECT count(*) FROM journal_keys WHERE idem_key = 'settle-50'`) != 1 {
		t.Fatal("a settlement's key of 50 days should stay for the keys' window")
	}
	if got := mismatches(t, store); len(got) != 0 {
		t.Fatalf("reconciliation after: %v", got)
	}
	// The balances are the accounts', untouched.
	if b := balance(t, svc, buyer, "USDT"); b != "779.9 80.1" {
		t.Fatalf("buyer USDT %s", b)
	}

	// A posting with a deleted key is a replay of its journal; other content
	// under it is refused.
	first, err := svc.Freeze(ctx, "order:b", domain.EntryOrderFreeze, buyer, domain.AccountSpot, "USDT", d("210.3"), "order")
	if err != nil || !first.Replayed {
		t.Fatalf("replay of a deleted key: %+v %v", first, err)
	}
	var kept string
	if err := db.QueryRow(ctx, `SELECT journal_id::text FROM journal_keys WHERE idem_key = $1`, "u:"+buyer+":order:b").Scan(&kept); err != nil || kept != first.JournalID {
		t.Fatalf("the replay names %s, the key %s (%v)", first.JournalID, kept, err)
	}
	if _, err := svc.Freeze(ctx, "order:b", domain.EntryOrderFreeze, buyer, domain.AccountSpot, "USDT", d("1"), "order"); !apperr.Is(err, apperr.CodeIdempotencyConflict) {
		t.Fatalf("other content under a deleted key: %v", err)
	}
	if b := balance(t, svc, buyer, "USDT"); b != "779.9 80.1" {
		t.Fatalf("the replays moved money: buyer USDT %s", b)
	}

	// Outside the run a journal's lines cannot be deleted.
	if _, err := db.Exec(ctx, `DELETE FROM journal_lines WHERE journal_id = (SELECT id FROM journals WHERE idem_key = $1)`,
		"u:"+buyer+":order:b2"); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("a journal's lines deleted outside the retention run: %v", err)
	}
	// A run again deletes nothing more.
	again, err := postgres.Retention{}.Run(ctx, db, w)
	if err != nil {
		t.Fatal(err)
	}
	for table, n := range rows(again) {
		if n != 0 {
			t.Fatalf("again: %d from %s", n, table)
		}
	}
}
