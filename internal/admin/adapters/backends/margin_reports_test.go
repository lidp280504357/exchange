package backends_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/skill/exchange/internal/admin/adapters/backends"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/chx"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/migrations"
)

// TestMarginReports reads margin trading's read models (E5): a
// liquidation's two events merged, one seen completed only (before
// ClickHouse 00010's trigger), paged newest first and filtered; the
// interest per day from the ledger's interest rows with what was owed
// before, the principal and rate from the hourly charges, in USDT at the
// day's last trade.
func TestMarginReports(t *testing.T) {
	ctx := context.Background()
	cfg := testenv.ClickHouse(t)
	conn, err := chx.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	db := chx.OpenDB(cfg)
	defer db.Close()
	if err := migrate.UpClickHouse(ctx, db, migrations.ClickHouse(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	const (
		user   = "0199a000-0000-7000-8000-0000000000e5"
		manual = "0199a000-0000-7000-8000-0000000000a1"
		older  = "0199a000-0000-7000-8000-0000000000a2"
	)
	for _, q := range []string{
		`INSERT INTO margin_liquidations (liquidation_id, user_id, account_type, symbol, trigger, approval_id, margin_level, total_asset,
			total_liability, started_at) VALUES
			('` + manual + `', '` + user + `', 'MARGIN_ISOLATED', 'BTC-USDT', 'MANUAL', '0199a000-0000-7000-8000-00000000aaaa', 1.04, 1040, 1000,
				now64(3) - INTERVAL 1 HOUR)`,
		`INSERT INTO margin_liquidations (liquidation_id, user_id, account_type, symbol, repaid, fee, insurance_covered, remaining,
			completed_at) VALUES
			('` + manual + `', '` + user + `', 'MARGIN_ISOLATED', 'BTC-USDT', '[{"asset":"USDT","amount":"1000"}]', 20.8, 0,
				'[{"asset":"BTC","amount":"0.0003"}]', now64(3) - INTERVAL 50 MINUTE),
			('` + older + `', '` + user + `', 'MARGIN_CROSS', '', '[{"asset":"USDT","amount":"50"}]', 1, 2, '[]',
				now64(3) - INTERVAL 2 HOUR)`,
		`INSERT INTO ledger_entries (journal_id, seq, line_no, entry_type, account_id, owner_type, owner_id, account_type, asset, amount,
			balance_kind, available_after, frozen_after, posted_at) VALUES
			(generateUUIDv4(), 1, 0, 'MARGIN_INTEREST', generateUUIDv4(), 'USER', 'u', 'MARGIN_CROSS_INTEREST', 'ZETA', -0.5, 'AVAILABLE', 0, 0,
				toDateTime64(today() - 3, 3, 'UTC')),
			(generateUUIDv4(), 2, 0, 'MARGIN_INTEREST', generateUUIDv4(), 'USER', 'u', 'MARGIN_CROSS_INTEREST', 'ZETA', -0.1, 'AVAILABLE', 0, 0,
				toDateTime64(today(), 3, 'UTC') + INTERVAL 1 HOUR),
			(generateUUIDv4(), 3, 0, 'MARGIN_INTEREST', generateUUIDv4(), 'USER', 'u', 'MARGIN_ISOLATED_INTEREST', 'ZETA', -0.2, 'AVAILABLE', 0, 0,
				toDateTime64(today(), 3, 'UTC') + INTERVAL 2 HOUR),
			(generateUUIDv4(), 4, 0, 'MARGIN_REPAY', generateUUIDv4(), 'USER', 'u', 'MARGIN_CROSS_INTEREST', 'ZETA', 0.15, 'AVAILABLE', 0, 0,
				toDateTime64(today(), 3, 'UTC') + INTERVAL 3 HOUR),
			(generateUUIDv4(), 2, 1, 'MARGIN_INTEREST', generateUUIDv4(), 'HOUSE', 'h', 'MARGIN_INTEREST_INCOME', 'ZETA', 0.1, 'AVAILABLE', 0, 0,
				toDateTime64(today(), 3, 'UTC') + INTERVAL 1 HOUR)`,
		`INSERT INTO margin_interest (interest_id, user_id, account_type, symbol, asset, principal, interest_model, hourly_rate, interest,
			interest_owed, hour, journal_id) VALUES
			(generateUUIDv4(), '` + user + `', 'MARGIN_CROSS', '', 'ZETA', 100, 'FIXED', 0.001, 0.1, 0.6, toDateTime64(today(), 3, 'UTC') + INTERVAL 1 HOUR, 'j1'),
			(generateUUIDv4(), '` + user + `', 'MARGIN_ISOLATED', 'ZETA-USDT', 'ZETA', 100, 'FIXED', 0.002, 0.2, 0.2,
				toDateTime64(today(), 3, 'UTC') + INTERVAL 2 HOUR, 'j2')`,
		`INSERT INTO trades (trade_id, symbol, price, quantity, quote_quantity, sequence, executed_at) VALUES
			(generateUUIDv4(), 'ZETA-USDT', 3, 1, 3, 1, toDateTime64(today(), 3, 'UTC') + INTERVAL 1 HOUR),
			(generateUUIDv4(), 'ZETA-USDT', 2, 1, 2, 2, toDateTime64(today(), 3, 'UTC') + INTERVAL 4 HOUR)`,
	} {
		if err := conn.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	r := backends.Reports{Conn: conn}

	first, next, err := r.MarginLiquidations(ctx, ports.MarginLiquidationQuery{Days: 7, UserID: user, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || next == "" {
		t.Fatalf("the first page %+v %q", first, next)
	}
	l := first[0]
	if l.ID != manual || l.Status != "COMPLETED" || l.Account != "MARGIN_ISOLATED" || l.Symbol == nil || *l.Symbol != "BTC-USDT" ||
		l.Trigger == nil || *l.Trigger != "MANUAL" || l.ApprovalID == nil || *l.MarginLevel != "1.04" || *l.Fee != "20.8" ||
		len(l.Repaid) != 1 || l.Repaid[0] != (ports.MarginAmount{Asset: "USDT", Amount: "1000"}) || len(l.Remaining) != 1 || l.StartedAt == nil ||
		l.CompletedAt == nil {
		t.Fatalf("the manual liquidation %+v", l)
	}
	second, last, err := r.MarginLiquidations(ctx, ports.MarginLiquidationQuery{Days: 7, UserID: user, Limit: 1, Cursor: next})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || last != "" {
		t.Fatalf("the second page %+v %q", second, last)
	}
	l = second[0]
	if l.ID != older || l.Status != "COMPLETED" || l.Symbol != nil || l.Trigger != nil || l.ApprovalID != nil || l.MarginLevel != nil ||
		l.StartedAt != nil || *l.InsuranceCovered != "2" || len(l.Remaining) != 0 {
		t.Fatalf("the completed-only liquidation %+v", l)
	}
	for name, q := range map[string]ports.MarginLiquidationQuery{
		"MANUAL":       {Days: 7, Trigger: "MANUAL", Limit: 10},
		"MARGIN_CROSS": {Days: 7, Account: "MARGIN_CROSS", Limit: 10},
		"BTC-USDT":     {Days: 7, Symbol: "BTC-USDT", Limit: 10},
	} {
		list, _, err := r.MarginLiquidations(ctx, q)
		if err != nil || len(list) != 1 {
			t.Fatalf("filtered by %s: %+v %v", name, list, err)
		}
	}
	// By kind (L1): the user's two when it is kept, none when left out.
	for name, c := range map[string]struct {
		f    ports.KindFilter
		want int
	}{
		"kept": {ports.KindFilter{Only: []string{user}}, 2}, "left out": {ports.KindFilter{Except: []string{user}}, 0},
	} {
		list, _, err := r.MarginLiquidations(ctx, ports.MarginLiquidationQuery{Days: 7, Limit: 10, ByKind: c.f})
		if err != nil || len(list) != c.want {
			t.Fatalf("the user %s: %+v %v", name, list, err)
		}
	}

	midnight := time.Now().UTC().Truncate(24 * time.Hour)
	buckets, err := r.MarginInterest(ctx, ports.ReportRange{From: midnight.AddDate(0, 0, -1), To: midnight, Bucket: ports.BucketDay}, "ZETA")
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 1 {
		t.Fatalf("the interest %+v", buckets)
	}
	b := buckets[0]
	if b.Day != midnight.Format(time.DateOnly) || b.Asset != "ZETA" || b.Charged.String() != "0.3" || b.Repaid.String() != "0.15" ||
		b.Owed.String() != "0.65" || b.PrincipalAvg.String() != "100" || b.HourlyRateAvg.String() != "0.0015" || b.Accounts != 2 ||
		b.ChargedUSDT == nil || *b.ChargedUSDT != "0.6" || *b.RepaidUSDT != "0.3" {
		t.Fatalf("the day's interest %+v", b)
	}
	// The interest of the accounts of a kind (L1): the ledger's owner u
	// left out, nothing charged or owed.
	others := ports.ReportRange{From: midnight.AddDate(0, 0, -1), To: midnight, Bucket: ports.BucketDay, ByKind: ports.KindFilter{Except: []string{"u"}}}
	if buckets, err = r.MarginInterest(ctx, others, "ZETA"); err != nil || len(buckets) != 0 {
		t.Fatalf("u left out: %+v %v", buckets, err)
	}
}
