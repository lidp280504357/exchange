package application

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Reports over a period in buckets (design 2026-10-02 §4.6, C4c): the
// trading, wallet and contract figures, the users' activity and HOUSE's
// result, per day, week (from Monday) or month.

// ReportQuery is a report's period as asked: from and to (YYYY-MM-DD, UTC,
// both included; to defaults to today), or the last days (7 by default,
// at most 90); and the bucket, day by default.
type ReportQuery struct {
	Days   int
	From   string
	To     string
	Bucket string
}

// The longest periods: a year in days, three years in weeks or months.
const (
	maxDailyDays  = 366
	maxBucketDays = 3 * 366
)

// reportRange checks the reader and works out the period.
func (s *Service) reportRange(p Principal, q ReportQuery) (ports.ReportRange, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return ports.ReportRange{}, err
	}
	bucket := strings.ToLower(strings.TrimSpace(q.Bucket))
	switch bucket {
	case "":
		bucket = ports.BucketDay
	case ports.BucketDay, ports.BucketWeek, ports.BucketMonth:
	default:
		return ports.ReportRange{}, apperr.Invalid("bucket must be day, week or month")
	}
	today := s.Now().UTC().Truncate(24 * time.Hour)
	if q.From == "" && q.To == "" {
		days := reportDays(q.Days)
		return ports.ReportRange{From: today.AddDate(0, 0, 1-days), To: today, Bucket: bucket}, nil
	}
	day := func(name, v string) (time.Time, error) {
		t, err := time.Parse(time.DateOnly, v)
		if err != nil {
			return time.Time{}, apperr.Invalid(name + " must be a date (YYYY-MM-DD)")
		}
		return t, nil
	}
	if q.From == "" {
		return ports.ReportRange{}, apperr.Invalid("from is required with to")
	}
	from, err := day("from", q.From)
	if err != nil {
		return ports.ReportRange{}, err
	}
	to := today
	if q.To != "" {
		if to, err = day("to", q.To); err != nil {
			return ports.ReportRange{}, err
		}
	}
	span := int(to.Sub(from).Hours()/24) + 1
	limit := maxBucketDays
	if bucket == ports.BucketDay {
		limit = maxDailyDays
	}
	switch {
	case from.After(to):
		return ports.ReportRange{}, apperr.Invalid("from is after to")
	case to.After(today):
		return ports.ReportRange{}, apperr.Invalid("to is after today")
	case span > limit:
		return ports.ReportRange{}, apperr.Invalid("the period is too long: a year by day, three years by week or month")
	}
	return ports.ReportRange{From: from, To: to, Bucket: bucket}, nil
}

// bucketStart is the first day of the bucket holding day.
func bucketStart(day time.Time, bucket string) time.Time {
	switch bucket {
	case ports.BucketWeek:
		return day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
	case ports.BucketMonth:
		return time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	return day
}

// buckets lists the first days of the period's buckets, oldest first.
func buckets(r ports.ReportRange) []time.Time {
	var out []time.Time
	for d := r.From; !d.After(r.To); d = d.AddDate(0, 0, 1) {
		if b := bucketStart(d, r.Bucket); len(out) == 0 || !out[len(out)-1].Equal(b) {
			out = append(out, b)
		}
	}
	return out
}

// UsersReport is the users' activity per bucket; Partial names what could
// not be read ("bots": the simulated market's bots are counted).
type UsersReport struct {
	Items   []ports.UsersBucket `json:"items"`
	Partial []string            `json:"partial"`
}

// UsersReport returns, per bucket of the period and oldest first, the
// accounts registered, signed in, trading and with a deposit credited,
// and every account registered by the bucket's end. HOUSE and the
// simulated market's bots are left out.
func (s *Service) UsersReport(ctx context.Context, p Principal, q ReportQuery) (UsersReport, error) {
	rng, err := s.reportRange(p, q)
	if err != nil {
		return UsersReport{}, err
	}
	out := UsersReport{Items: []ports.UsersBucket{}, Partial: []string{}}
	var exclude []string
	if s.HouseBook.User != "" {
		exclude = append(exclude, s.HouseBook.User)
	}
	if s.SimBots == nil {
		out.Partial = append(out.Partial, "bots")
	} else if bots, err := s.SimBots.BotUsers(ctx); err != nil {
		s.Log.WarnContext(ctx, "reports: the bots are unknown", "error", err)
		out.Partial = append(out.Partial, "bots")
	} else {
		exclude = append(exclude, bots...)
	}
	rows, before, err := s.Reports.Users(ctx, rng, exclude)
	if err != nil {
		return UsersReport{}, err
	}
	byDay := map[string]ports.UsersBucket{}
	for _, r := range rows {
		byDay[r.Day] = r
	}
	total := before
	for _, b := range buckets(rng) {
		day := dayKey(b)
		row := byDay[day]
		row.Day = day
		total += row.Registered
		row.Total = total
		out.Items = append(out.Items, row)
	}
	return out, nil
}

// HousePnL is HOUSE's result per bucket in USDT (ADR-0013, ADR-0015).
type HousePnL struct {
	Items []HousePnLBucket `json:"items"`
	// Unpriced names the pairs left out of the spot result: no price of
	// their own or of their quote asset in USDT.
	Unpriced []string `json:"unpriced"`
}

// HousePnLBucket is HOUSE's result in one bucket: the change of its spot
// result (what it traded, valued at the pairs' last prices each day),
// its contract fills' realized results less fees, the funding it got,
// their sum and the running sum over the period; SpotResult is its spot
// result since it began, at the bucket's end.
type HousePnLBucket struct {
	Day        string `json:"day"`
	Spot       string `json:"spot_pnl"`
	Contracts  string `json:"contracts_pnl"`
	Funding    string `json:"funding"`
	Total      string `json:"total"`
	Cumulative string `json:"cumulative"`
	SpotResult string `json:"spot_result"`
}

const quoteUSDT = "USDT"

// houseHolding is HOUSE's position on a pair as the days go by.
type houseHolding struct {
	quote      string
	base, cash decimal.Decimal
	close      decimal.Decimal
}

// HousePnLReport returns HOUSE's result per bucket of the period.
func (s *Service) HousePnLReport(ctx context.Context, p Principal, q ReportQuery) (HousePnL, error) {
	rng, err := s.reportRange(p, q)
	if err != nil {
		return HousePnL{}, err
	}
	spot, err := s.Reports.HouseSpot(ctx, rng)
	if err != nil {
		return HousePnL{}, err
	}
	var contracts []ports.HouseContractDay
	if s.HouseBook.User != "" {
		if contracts, err = s.Reports.HouseContracts(ctx, rng, s.HouseBook.User); err != nil {
			return HousePnL{}, err
		}
	}
	days, unpriced := houseSpotResults(rng, spot)
	out := HousePnL{Items: []HousePnLBucket{}, Unpriced: unpriced}
	type sums struct{ spot, contracts, funding, result decimal.Decimal }
	per := map[string]*sums{}
	for _, b := range buckets(rng) {
		per[dayKey(b)] = &sums{}
	}
	prev := days.start
	for d := rng.From; !d.After(rng.To); d = d.AddDate(0, 0, 1) {
		b := per[dayKey(bucketStart(d, rng.Bucket))]
		result := days.at[dayKey(d)]
		b.spot = b.spot.Add(result.Sub(prev))
		b.result, prev = result, result
	}
	for _, c := range contracts {
		if b := per[dayKey(bucketStart(c.Day.UTC(), rng.Bucket))]; b != nil {
			b.contracts, b.funding = b.contracts.Add(c.Realized), b.funding.Add(c.Funding)
		}
	}
	cumulative := decimal.Zero
	for _, b := range buckets(rng) {
		sum := per[dayKey(b)]
		total := sum.spot.Add(sum.contracts).Add(sum.funding)
		cumulative = cumulative.Add(total)
		out.Items = append(out.Items, HousePnLBucket{
			Day: dayKey(b), Spot: sum.spot.StringFixed(2), Contracts: sum.contracts.StringFixed(2), Funding: sum.funding.StringFixed(2),
			Total: total.StringFixed(2), Cumulative: cumulative.StringFixed(2), SpotResult: sum.result.StringFixed(2),
		})
	}
	return out, nil
}

// spotResults is HOUSE's spot result before the period and at the end of
// each of its days (by dayKey).
type spotResults struct {
	start decimal.Decimal
	at    map[string]decimal.Decimal
}

// dayKey is a day as YYYY-MM-DD.
func dayKey(t time.Time) string { return t.UTC().Format(time.DateOnly) }

// houseSpotResults values HOUSE's spot trading day by day: on each pair the
// quote it got less paid plus the base it holds from trading at the pair's
// last price, in USDT at the last price of the quote asset's USDT pair.
// A pair without a price when it is needed is left out of every day.
func houseSpotResults(rng ports.ReportRange, rows []ports.HouseSpotDay) (spotResults, []string) {
	value := func(pairs map[string]*houseHolding, skip map[string]bool, missing map[string]bool) decimal.Decimal {
		sum := decimal.Zero
		for symbol, h := range pairs {
			if skip[symbol] {
				continue
			}
			rate := decimal.NewFromInt(1)
			if h.quote != quoteUSDT {
				q := pairs[h.quote+"-"+quoteUSDT]
				if q == nil || !q.close.IsPositive() {
					missing[symbol] = true
					continue
				}
				rate = q.close
			}
			if !h.base.IsZero() && !h.close.IsPositive() {
				missing[symbol] = true
				continue
			}
			sum = sum.Add(h.cash.Add(h.base.Mul(h.close)).Mul(rate))
		}
		return sum
	}
	byDay := map[string][]ports.HouseSpotDay{}
	for _, r := range rows {
		if !r.BeforePeriod {
			byDay[dayKey(r.Day)] = append(byDay[dayKey(r.Day)], r)
		}
	}
	run := func(skip map[string]bool) (spotResults, map[string]bool) {
		pairs, missing := map[string]*houseHolding{}, map[string]bool{}
		apply := func(r ports.HouseSpotDay) {
			h := pairs[r.Symbol]
			if h == nil {
				h = &houseHolding{quote: r.QuoteAsset}
				pairs[r.Symbol] = h
			}
			h.base, h.cash = h.base.Add(r.NetBase), h.cash.Add(r.NetQuote)
			if r.HasClose {
				h.close = r.Close
			}
		}
		for _, r := range rows {
			if r.BeforePeriod {
				apply(r)
			}
		}
		out := spotResults{start: value(pairs, skip, missing), at: map[string]decimal.Decimal{}}
		for d := rng.From; !d.After(rng.To); d = d.AddDate(0, 0, 1) {
			for _, r := range byDay[dayKey(d)] {
				apply(r)
			}
			out.at[dayKey(d)] = value(pairs, skip, missing)
		}
		return out, missing
	}
	results, missing := run(map[string]bool{})
	if len(missing) == 0 {
		return results, []string{}
	}
	results, _ = run(missing)
	unpriced := make([]string, 0, len(missing))
	for symbol := range missing {
		unpriced = append(unpriced, symbol)
	}
	slices.Sort(unpriced)
	return results, unpriced
}
