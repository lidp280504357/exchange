package backends

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/admin/ports"
)

// A report's period and buckets (design 2026-10-02 §4.6, C4c).

// bucketOf is the ClickHouse function that takes a time to the first day
// of its bucket.
var bucketOf = map[string]string{ports.BucketDay: "toDate", ports.BucketWeek: "toMonday", ports.BucketMonth: "toStartOfMonth"}

var reportMarker = regexp.MustCompile(`\{(day|range):([a-z_]+)\}`)

// chTime is how a bound goes to toDateTime64 (UTC).
const chTime = "2006-01-02 15:04:05"

// ranged fills a report's query for the period: {day:column} becomes the
// column's bucket, {range:column} the period's bounds on the column, with
// their arguments in the order they appear.
func ranged(query string, r ports.ReportRange) (string, []any) {
	fn, ok := bucketOf[r.Bucket]
	if !ok {
		fn = "toDate"
	}
	from, to := r.From.UTC().Format(chTime), r.To.UTC().AddDate(0, 0, 1).Format(chTime)
	var args []any
	out := reportMarker.ReplaceAllStringFunc(query, func(m string) string {
		p := reportMarker.FindStringSubmatch(m)
		if p[1] == "day" {
			return fn + "(" + p[2] + ")"
		}
		args = append(args, from, to)
		return p[2] + " >= toDateTime64(?, 3, 'UTC') AND " + p[2] + " < toDateTime64(?, 3, 'UTC')"
	})
	return out, args
}

// usersReport counts per bucket the accounts registered and signed in
// (auth events), trading on either side of a spot trade or with a
// contract fill, and with a deposit credited; each once a bucket. The
// accounts in the excluded list ({excluded}: HOUSE, the bots) are left
// out.
const usersReport = `SELECT day, sum(registered), sum(signed_in), sum(traders), sum(depositors) FROM
	(
		SELECT {day:occurred_at} AS day, uniqExactIf(aggregate_id, event_type = 'auth.UserRegistered') AS registered,
			uniqExactIf(aggregate_id, event_type = 'auth.LoginSucceeded') AS signed_in, toUInt64(0) AS traders, toUInt64(0) AS depositors
		FROM events WHERE topic = 'auth.events' AND event_type IN ('auth.UserRegistered', 'auth.LoginSucceeded') AND {range:occurred_at}
			AND NOT has({excluded}, aggregate_id)
		GROUP BY day
		UNION ALL
		SELECT day, toUInt64(0), toUInt64(0), uniqExact(user), toUInt64(0) FROM
		(
			SELECT {day:executed_at} AS day, arrayJoin([toString(buyer_user_id), toString(seller_user_id)]) AS user
			FROM trades FINAL WHERE {range:executed_at}
			UNION ALL
			SELECT {day:executed_at} AS day, toString(user_id) AS user FROM derivatives_fills FINAL WHERE {range:executed_at}
		)
		WHERE NOT has({excluded}, user) AND user != '00000000-0000-0000-0000-000000000000'
		GROUP BY day
		UNION ALL
		SELECT {day:updated_at} AS day, toUInt64(0), toUInt64(0), toUInt64(0), uniqExact(user_id)
		FROM wallet_deposits FINAL WHERE status = 'CREDITED' AND NOT unclaimed AND {range:updated_at} AND NOT has({excluded}, user_id)
		GROUP BY day
	)
	GROUP BY day ORDER BY day`

// Users returns the users' activity per bucket of the period and how
// many accounts registered before it.
func (r Reports) Users(ctx context.Context, rng ports.ReportRange, exclude []string) ([]ports.UsersBucket, uint64, error) {
	// Never empty: an empty array literal has no element type.
	exclude = append([]string{""}, exclude...)
	query, args := ranged(usersReport, rng)
	// Each {excluded} takes the list, in its place among the bounds.
	query, args = excluded(query, args, exclude)
	rows, err := r.Conn.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, unavailable(err)
	}
	defer func() { _ = rows.Close() }()
	out := []ports.UsersBucket{}
	for rows.Next() {
		var b ports.UsersBucket
		var day time.Time
		if err := rows.Scan(&day, &b.Registered, &b.SignedIn, &b.Traders, &b.Depositors); err != nil {
			return nil, 0, unavailable(err)
		}
		b.Day = day.Format(time.DateOnly)
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, unavailable(err)
	}
	var before uint64
	if err := r.Conn.QueryRow(ctx, `SELECT uniqExact(aggregate_id) FROM events
		WHERE topic = 'auth.events' AND event_type = 'auth.UserRegistered' AND occurred_at < toDateTime64(?, 3, 'UTC')
			AND NOT has(?, aggregate_id)`, rng.From.UTC().Format(chTime), exclude).Scan(&before); err != nil {
		return nil, 0, unavailable(err)
	}
	return out, before, nil
}

// excluded puts the excluded accounts in place of each {excluded}: the
// arguments follow the placeholders' order, the bounds' pairs and the
// list interleaved as they appear in the query.
func excluded(query string, bounds []any, list []string) (string, []any) {
	args := make([]any, 0, len(bounds)+4)
	var b strings.Builder
	next := 0
	for {
		i := strings.IndexAny(query, "?{")
		if i < 0 {
			b.WriteString(query)
			break
		}
		if query[i] == '?' {
			b.WriteString(query[:i+1])
			query = query[i+1:]
			args = append(args, bounds[next])
			next++
			continue
		}
		if strings.HasPrefix(query[i:], "{excluded}") {
			b.WriteString(query[:i] + "?")
			query = query[i+len("{excluded}"):]
			args = append(args, list)
			continue
		}
		b.WriteString(query[:i+1])
		query = query[i+1:]
	}
	return b.String(), args
}

// houseSpotBefore sums HOUSE's spot trading per pair up to the period with
// the pair's last price; houseSpotDays the same per day in the period.
const (
	houseSpotBefore = `SELECT symbol,
			sumIf(quantity, house_side = 'BUY') - sumIf(quantity, house_side = 'SELL') AS net_base,
			sumIf(quote_quantity, house_side = 'SELL') - sumIf(quote_quantity, house_side = 'BUY') AS net_quote,
			argMax(price, executed_at) AS close
		FROM trades FINAL WHERE NOT endsWith(symbol, '-PERP') AND executed_at < toDateTime64(?, 3, 'UTC')
		GROUP BY symbol`
	houseSpotDays = `SELECT toDate(executed_at) AS day, symbol,
			sumIf(quantity, house_side = 'BUY') - sumIf(quantity, house_side = 'SELL') AS net_base,
			sumIf(quote_quantity, house_side = 'SELL') - sumIf(quote_quantity, house_side = 'BUY') AS net_quote,
			argMax(price, executed_at) AS close
		FROM trades FINAL WHERE NOT endsWith(symbol, '-PERP') AND {range:executed_at}
		GROUP BY day, symbol ORDER BY day, symbol`
)

// HouseSpot returns HOUSE's spot trading per pair before the period and
// per pair and day in it, with each pair's last price.
func (r Reports) HouseSpot(ctx context.Context, rng ports.ReportRange) ([]ports.HouseSpotDay, error) {
	out := []ports.HouseSpotDay{}
	scan := func(query string, args []any, before bool) error {
		rows, err := r.Conn.Query(ctx, query, args...)
		if err != nil {
			return unavailable(err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			d := ports.HouseSpotDay{BeforePeriod: before}
			var day time.Time
			dest := []any{&d.Symbol, &d.NetBase, &d.NetQuote, &d.Close}
			if !before {
				dest = append([]any{&day}, dest...)
			}
			if err := rows.Scan(dest...); err != nil {
				return unavailable(err)
			}
			d.Day, d.QuoteAsset, d.HasClose = day, quoteOf(d.Symbol), d.Close.IsPositive()
			out = append(out, d)
		}
		return rows.Err()
	}
	if err := scan(houseSpotBefore, []any{rng.From.UTC().Format(chTime)}, true); err != nil {
		return nil, unavailable(err)
	}
	query, args := ranged(houseSpotDays, ports.ReportRange{From: rng.From, To: rng.To, Bucket: ports.BucketDay})
	if err := scan(query, args, false); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// quoteOf is a pair's quote asset: what follows its last "-".
func quoteOf(symbol string) string {
	return symbol[strings.LastIndex(symbol, "-")+1:]
}

// houseContracts sums HOUSE's contract results per day: its fills'
// realized results less their fees, and its funding.
const houseContracts = `SELECT day, sum(realized), sum(funding) FROM
	(
		SELECT toDate(executed_at) AS day, sum(realized_pnl) - sum(fee) AS realized, toDecimal128(0, 18) AS funding
		FROM derivatives_fills FINAL WHERE user_id = toUUID(?) AND {range:executed_at} GROUP BY day
		UNION ALL
		SELECT toDate(funding_time) AS day, toDecimal128(0, 18), sum(amount)
		FROM derivatives_funding FINAL WHERE user_id = toUUID(?) AND {range:funding_time} GROUP BY day
	)
	GROUP BY day ORDER BY day`

// HouseContracts returns HOUSE's contract results per day of the period.
func (r Reports) HouseContracts(ctx context.Context, rng ports.ReportRange, houseUser string) ([]ports.HouseContractDay, error) {
	query, bounds := ranged(houseContracts, ports.ReportRange{From: rng.From, To: rng.To, Bucket: ports.BucketDay})
	rows, err := r.Conn.Query(ctx, query, houseUser, bounds[0], bounds[1], houseUser, bounds[2], bounds[3])
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()
	out := []ports.HouseContractDay{}
	for rows.Next() {
		var d ports.HouseContractDay
		var realized, funding decimal.Decimal
		if err := rows.Scan(&d.Day, &realized, &funding); err != nil {
			return nil, unavailable(err)
		}
		d.Realized, d.Funding = realized, funding
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}
