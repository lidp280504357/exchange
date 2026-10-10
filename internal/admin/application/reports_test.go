package application

import (
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

type fakeReports struct {
	ports.Reports
	rng       ports.ReportRange
	users     []ports.UsersBucket
	before    uint64
	spot      []ports.HouseSpotDay
	contracts []ports.HouseContractDay
	house     string
}

func (f *fakeReports) Trading(_ context.Context, r ports.ReportRange) ([]ports.TradingDay, error) {
	f.rng = r
	return []ports.TradingDay{}, nil
}

func (f *fakeReports) Users(_ context.Context, r ports.ReportRange) ([]ports.UsersBucket, uint64, error) {
	f.rng = r
	return f.users, f.before, nil
}

func (f *fakeReports) HouseSpot(_ context.Context, r ports.ReportRange) ([]ports.HouseSpotDay, error) {
	f.rng = r
	return f.spot, nil
}

func (f *fakeReports) HouseContracts(_ context.Context, _ ports.ReportRange, user string) ([]ports.HouseContractDay, error) {
	f.house = user
	return f.contracts, nil
}

type fakeBots struct {
	ids []string
	err error
}

func (b fakeBots) BotUsers(context.Context) ([]string, error) { return b.ids, b.err }

var reader = Principal{Admin: domain.Admin{Role: domain.RoleAuditor}}

func date(s string) time.Time {
	t, _ := time.Parse(time.DateOnly, s)
	return t
}

func TestAReportsPeriodAndBuckets(t *testing.T) {
	reports := &fakeReports{}
	svc := &Service{Reports: reports, Now: func() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) }}
	ctx := context.Background()
	if _, err := svc.TradingReport(ctx, reader, ReportQuery{}); err != nil ||
		!reports.rng.From.Equal(date("2026-09-26")) || !reports.rng.To.Equal(date("2026-10-02")) || reports.rng.Bucket != ports.BucketDay {
		t.Fatalf("the last 7 days by default: %+v %v", reports.rng, err)
	}
	if _, err := svc.TradingReport(ctx, reader, ReportQuery{Days: 400}); err != nil || !reports.rng.From.Equal(date("2026-07-05")) {
		t.Fatalf("at most 90 days back: %+v %v", reports.rng, err)
	}
	if _, err := svc.TradingReport(ctx, reader, ReportQuery{From: "2025-01-01", Bucket: "WEEK"}); err != nil ||
		!reports.rng.To.Equal(date("2026-10-02")) || reports.rng.Bucket != ports.BucketWeek {
		t.Fatalf("from a date to today by week: %+v %v", reports.rng, err)
	}
	for name, q := range map[string]ReportQuery{
		"a bucket":          {Bucket: "year"},
		"to alone":          {To: "2026-10-01"},
		"a date":            {From: "1/10/2026"},
		"from after to":     {From: "2026-10-02", To: "2026-10-01"},
		"to after today":    {From: "2026-10-01", To: "2026-10-03"},
		"a long daily span": {From: "2025-09-01", To: "2026-10-01"},
		"a longer span":     {From: "2022-01-01", To: "2026-10-01", Bucket: "month"},
	} {
		if _, err := svc.TradingReport(ctx, reader, q); !apperr.Is(err, apperr.CodeInvalidArgument) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got := buckets(ports.ReportRange{From: date("2026-09-25"), To: date("2026-10-06"), Bucket: ports.BucketWeek}); len(got) != 3 ||
		!got[0].Equal(date("2026-09-21")) || !got[2].Equal(date("2026-10-05")) {
		t.Fatalf("weeks from Monday: %v", got)
	}
	if got := buckets(ports.ReportRange{From: date("2026-08-31"), To: date("2026-10-02"), Bucket: ports.BucketMonth}); len(got) != 3 ||
		!got[1].Equal(date("2026-09-01")) {
		t.Fatalf("months: %v", got)
	}
}

func TestTheUsersReportCountsEveryBucket(t *testing.T) {
	reports := &fakeReports{
		before: 100,
		users: []ports.UsersBucket{
			{Day: "2026-09-21", Registered: 3, SignedIn: 10, Traders: 4, Depositors: 1},
			{Day: "2026-10-05", Registered: 2, SignedIn: 7, Traders: 2},
		},
	}
	svc := &Service{
		Reports: reports, KindIDs: &fakeKindIDs{of: map[string][]string{"BOT": {"bot-1", "bot-2"}, "SYSTEM": {"house"}}},
		Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return time.Date(2026, 10, 6, 1, 0, 0, 0, time.UTC) },
	}
	out, err := svc.UsersReport(context.Background(), reader, ReportQuery{From: "2026-09-25", To: "2026-10-06", Bucket: "week"})
	if err != nil {
		t.Fatal(err)
	}
	// The humans' (L1): HOUSE and the bots left out by kind.
	if !slices.Equal(reports.rng.ByKind.Except, []string{"bot-1", "bot-2", "house"}) || len(out.Partial) != 0 {
		t.Fatalf("HOUSE and the bots left out: %+v %v", reports.rng.ByKind, out.Partial)
	}
	want := []ports.UsersBucket{
		{Day: "2026-09-21", Registered: 3, SignedIn: 10, Traders: 4, Depositors: 1, Total: 103},
		{Day: "2026-09-28", Total: 103},
		{Day: "2026-10-05", Registered: 2, SignedIn: 7, Traders: 2, Total: 105},
	}
	if !slices.Equal(out.Items, want) {
		t.Fatalf("every week, the running total: %+v", out.Items)
	}
	// Without the kinds the report is unavailable, not everyone's.
	svc.KindIDs = failingKindIDs{}
	if _, err := svc.UsersReport(context.Background(), reader, ReportQuery{}); code(err) != apperr.CodeUnavailable {
		t.Fatalf("without the kinds: %v", err)
	}
}

func TestHousesResultIsValuedDayByDay(t *testing.T) {
	d := decimal.RequireFromString
	day1, day2 := date("2026-10-01"), date("2026-10-02")
	reports := &fakeReports{
		spot: []ports.HouseSpotDay{
			// Before the period: 1 BTC bought at 50,000; 10 ETH for 0.5 BTC;
			// DOGE-ETH has no ETH-USDT to value its ETH.
			{Symbol: "BTC-USDT", QuoteAsset: "USDT", NetBase: d("1"), NetQuote: d("-50000"), Close: d("50000"), HasClose: true, BeforePeriod: true},
			{Symbol: "ETH-BTC", QuoteAsset: "BTC", NetBase: d("10"), NetQuote: d("-0.5"), Close: d("0.05"), HasClose: true, BeforePeriod: true},
			{Symbol: "DOGE-ETH", QuoteAsset: "ETH", NetBase: d("100"), NetQuote: d("-0.01"), Close: d("0.0001"), HasClose: true, BeforePeriod: true},
			// Day 1: half the BTC sold at 51,000, the last price.
			{Day: day1, Symbol: "BTC-USDT", QuoteAsset: "USDT", NetBase: d("-0.5"), NetQuote: d("25500"), Close: d("51000"), HasClose: true},
			// Day 2: BTC back to 50,000 without HOUSE.
			{Day: day2, Symbol: "BTC-USDT", QuoteAsset: "USDT", Close: d("50000"), HasClose: true},
		},
		contracts: []ports.HouseContractDay{{Day: day1, Realized: d("30"), Funding: d("-5")}},
	}
	svc := &Service{Reports: reports, HouseBook: HouseDeps{User: "house"}, Now: func() time.Time { return day2.Add(time.Hour) }}
	out, err := svc.HousePnLReport(context.Background(), reader, ReportQuery{From: "2026-10-01"})
	if err != nil {
		t.Fatal(err)
	}
	// Day 1: BTC holds 0.5 at 51,000 with 24,500 paid: +1,000; ETH-BTC
	// stays 0 (10 × 0.05 − 0.5 BTC). Day 2: 0.5 BTC loses 500.
	want := []HousePnLBucket{
		{Day: "2026-10-01", Spot: "1000.00", Contracts: "30.00", Funding: "-5.00", Total: "1025.00", Cumulative: "1025.00", SpotResult: "1000.00"},
		{Day: "2026-10-02", Spot: "-500.00", Contracts: "0.00", Funding: "0.00", Total: "-500.00", Cumulative: "525.00", SpotResult: "500.00"},
	}
	if !slices.Equal(out.Items, want) || !slices.Equal(out.Unpriced, []string{"DOGE-ETH"}) || reports.house != "house" {
		t.Fatalf("by day %+v unpriced %v house %q", out.Items, out.Unpriced, reports.house)
	}
	out, err = svc.HousePnLReport(context.Background(), reader, ReportQuery{From: "2026-10-01", Bucket: "week"})
	if err != nil || len(out.Items) != 1 || out.Items[0] != (HousePnLBucket{
		Day: "2026-09-28", Spot: "500.00", Contracts: "30.00", Funding: "-5.00", Total: "525.00", Cumulative: "525.00", SpotResult: "500.00",
	}) {
		t.Fatalf("by week %+v %v", out.Items, err)
	}
}
