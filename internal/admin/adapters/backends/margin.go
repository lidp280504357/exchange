package backends

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// Margin implements ports.Margin over margin-service's internal API for
// the console (/internal/margin/*, C5): never routed by the gateway, not
// signed; a write names its administrator in X-Admin-Id.
type Margin struct {
	REST
	Base string
}

// headerAdminID is margin-service's header of the administrator a write
// is for (its updated_by and frozen_by).
const headerAdminID = "X-Admin-Id"

func (m Margin) path(parts ...string) string {
	escaped := make([]string, len(parts))
	for i, p := range parts {
		escaped[i] = url.PathEscape(p)
	}
	return m.Base + "/internal/margin/" + strings.Join(escaped, "/")
}

// withVersion is a terms body with its expected_version: the terms'
// numbers are kept as written.
func withVersion(terms json.RawMessage, version int64) (map[string]any, error) {
	body := map[string]any{}
	d := json.NewDecoder(bytes.NewReader(terms))
	d.UseNumber()
	if err := d.Decode(&body); err != nil {
		return nil, apperr.Invalid("the terms are not a JSON object")
	}
	body["expected_version"] = version
	return body, nil
}

// Assets returns the margin assets' terms with what is lent of each.
func (m Margin) Assets(ctx context.Context) (json.RawMessage, error) {
	return m.do(ctx, http.MethodGet, m.path("assets"), nil, nil)
}

// SetAsset replaces an asset's terms as of expectedVersion, in admin's name.
func (m Margin) SetAsset(ctx context.Context, asset string, terms json.RawMessage, expectedVersion int64, admin string) (json.RawMessage, error) {
	body, err := withVersion(terms, expectedVersion)
	if err != nil {
		return nil, err
	}
	return m.do(ctx, http.MethodPut, m.path("assets", strings.ToUpper(asset)), body, map[string]string{headerAdminID: admin})
}

// Settings returns the cross account's terms and the suggested thresholds.
func (m Margin) Settings(ctx context.Context) (json.RawMessage, error) {
	return m.do(ctx, http.MethodGet, m.path("settings"), nil, nil)
}

// SetCross replaces the cross account's terms as of expectedVersion.
func (m Margin) SetCross(ctx context.Context, cross json.RawMessage, expectedVersion int64, admin string) (json.RawMessage, error) {
	return m.do(ctx, http.MethodPut, m.path("settings"), map[string]any{"cross": cross, "expected_version": expectedVersion},
		map[string]string{headerAdminID: admin})
}

// Pairs returns the pairs' isolated terms.
func (m Margin) Pairs(ctx context.Context) (json.RawMessage, error) {
	return m.do(ctx, http.MethodGet, m.path("pairs"), nil, nil)
}

// SetPair replaces a pair's isolated terms as of expectedVersion.
func (m Margin) SetPair(ctx context.Context, symbol string, terms json.RawMessage, expectedVersion int64, admin string) (json.RawMessage, error) {
	body, err := withVersion(terms, expectedVersion)
	if err != nil {
		return nil, err
	}
	return m.do(ctx, http.MethodPut, m.path("pairs", strings.ToUpper(symbol)), body, map[string]string{headerAdminID: admin})
}

// Accounts returns the accounts q selects, riskiest first; empty filters
// are not sent.
func (m Margin) Accounts(ctx context.Context, q ports.MarginAccountQuery) (json.RawMessage, error) {
	v := url.Values{}
	for k, s := range map[string]string{"status": q.Status, "account": q.Account, "symbol": q.Symbol, "user_id": q.UserID} {
		if s != "" {
			v.Set(k, s)
		}
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	return m.list(ctx, m.path("accounts"), v, q.ByKind)
}

// Account returns one account in full.
func (m Margin) Account(ctx context.Context, userID, account string) (json.RawMessage, error) {
	return m.do(ctx, http.MethodGet, m.path("accounts", userID, account), nil, nil)
}

// Freeze freezes an account in admin's name, for reason.
func (m Margin) Freeze(ctx context.Context, userID, account, admin, reason string) (json.RawMessage, error) {
	return m.do(ctx, http.MethodPost, m.path("accounts", userID, account, "freeze"), map[string]string{"reason": reason},
		map[string]string{headerAdminID: admin})
}

// Unfreeze lifts an administrator's freeze.
func (m Margin) Unfreeze(ctx context.Context, userID, account, admin string) (json.RawMessage, error) {
	return m.do(ctx, http.MethodPost, m.path("accounts", userID, account, "unfreeze"), map[string]string{},
		map[string]string{headerAdminID: admin})
}

// Liquidate starts the liquidation an approval asks for (margin-service's
// E3 takes the approval alone; the reasons are the approval's and the
// audit trail's).
func (m Margin) Liquidate(ctx context.Context, userID, account, approvalID, admin string) (json.RawMessage, error) {
	return m.do(ctx, http.MethodPost, m.path("accounts", userID, account, "liquidate"), map[string]string{"approval_id": approvalID},
		map[string]string{headerAdminID: admin})
}

// marginLiquidations merges each liquidation's two events (anyLast skips
// what an event left NULL) and pages them by when they started (their end
// when the start is not known), newest first; {kind} is the accounts'
// kinds' condition (L1, kindCond on uid).
const marginLiquidations = `SELECT id, uid, acct, sym, trig, appr, lvl, assets, liabilities, rep, fee_paid, covered, rem, started, completed, at
	FROM (
		SELECT toString(liquidation_id) AS id, toString(anyLast(user_id)) AS uid, anyLast(account_type) AS acct, anyLast(symbol) AS sym,
			anyLast(trigger) AS trig, anyLast(approval_id) AS appr, anyLast(margin_level) AS lvl, anyLast(total_asset) AS assets,
			anyLast(total_liability) AS liabilities, anyLast(repaid) AS rep, anyLast(fee) AS fee_paid, anyLast(insurance_covered) AS covered,
			anyLast(remaining) AS rem, anyLast(started_at) AS started, anyLast(completed_at) AS completed,
			assumeNotNull(coalesce(started, completed)) AS at
		FROM margin_liquidations GROUP BY liquidation_id
	)
	WHERE at >= toDateTime64(today() - ?, 3, 'UTC') AND (? = '' OR acct = ?) AND (? = '' OR sym = ?) AND (? = '' OR trig = ?)
		AND (? = '' OR uid = ?) AND {kind} AND (NOT ? OR (at, id) < (` + ms + `, ?))
	ORDER BY at DESC, id DESC LIMIT ?`

// MarginLiquidations returns a page of margin liquidations, newest first.
func (r Reports) MarginLiquidations(ctx context.Context, q ports.MarginLiquidationQuery) ([]ports.MarginLiquidation, string, error) {
	pc, err := pageOf(q.Cursor, q.Limit)
	if err != nil {
		return nil, "", err
	}
	byKind, byKindArgs := kindCond("uid", q.ByKind)
	args := append([]any{q.Days - 1, q.Account, q.Account, q.Symbol, q.Symbol, q.Trigger, q.Trigger, q.UserID, q.UserID}, byKindArgs...)
	args = append(args, pc.on, pc.at.UnixMilli(), pc.id, pc.limit+1)
	rows, err := r.Conn.Query(kindCtx(ctx, q.ByKind), strings.Replace(marginLiquidations, "{kind}", byKind, 1), args...)
	if err != nil {
		return nil, "", unavailable(err)
	}
	defer func() { _ = rows.Close() }()
	out := []ports.MarginLiquidation{}
	var ats []time.Time
	for rows.Next() {
		var l ports.MarginLiquidation
		var userID, account, symbol, repaid, remaining *string
		var level, assets, liabilities, fee, insurance *decimal.Decimal
		var at time.Time
		if err := rows.Scan(&l.ID, &userID, &account, &symbol, &l.Trigger, &l.ApprovalID, &level, &assets, &liabilities, &repaid, &fee, &insurance,
			&remaining, &l.StartedAt, &l.CompletedAt, &at); err != nil {
			return nil, "", unavailable(err)
		}
		l.UserID, l.Account = deref(userID), deref(account)
		if s := deref(symbol); s != "" {
			l.Symbol = &s
		}
		l.MarginLevel, l.TotalAsset, l.TotalLiability = decimalText(level), decimalText(assets), decimalText(liabilities)
		l.Fee, l.InsuranceCovered = decimalText(fee), decimalText(insurance)
		l.Repaid, l.Remaining = amountsOf(repaid), amountsOf(remaining)
		l.Status = "STARTED"
		if l.CompletedAt != nil {
			l.Status = "COMPLETED"
		}
		out = append(out, l)
		ats = append(ats, at)
	}
	if err := rows.Err(); err != nil {
		return nil, "", unavailable(err)
	}
	next := pc.next(len(out), func(i int) (time.Time, string) { return ats[i], out[i].ID })
	return out[:min(len(out), pc.limit)], next, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func decimalText(d *decimal.Decimal) *string {
	if d == nil {
		return nil
	}
	s := d.String()
	return &s
}

// amountsOf reads a JSON array of {asset, amount}; none for NULL or
// anything else.
func amountsOf(s *string) []ports.MarginAmount {
	out := []ports.MarginAmount{}
	if s != nil {
		_ = json.Unmarshal([]byte(*s), &out)
	}
	if out == nil {
		out = []ports.MarginAmount{}
	}
	return out
}

// The interest report (margin design §4.3): charged and repaid from the
// ledger's interest rows (a charge lowers one, a repayment raises it),
// owed carried from before the period; the principal and the rate from
// margin_interest's hourly charges; the USDT values at each bucket's last
// trade of the asset's USDT pair. Of the accounts of the kinds kept (L1;
// {kind} in the one not ranged).
const (
	marginInterestRows   = `account_type IN ('MARGIN_CROSS_INTEREST', 'MARGIN_ISOLATED_INTEREST')`
	marginInterestBefore = `SELECT asset, -sum(amount) FROM ledger_entries FINAL
		WHERE ` + marginInterestRows + ` AND posted_at < toDateTime64(?, 3, 'UTC') AND (? = '' OR asset = ?) AND {kind} GROUP BY asset`
	marginInterestLedger = `SELECT {day:posted_at} AS day, asset, -sumIf(amount, amount < 0), sumIf(amount, amount > 0), sum(amount)
		FROM ledger_entries FINAL WHERE ` + marginInterestRows + ` AND {range:posted_at} AND {kind:owner_id} AND (? = '' OR asset = ?)
		GROUP BY day, asset ORDER BY day, asset`
	marginInterestCharges = `SELECT {day:hour} AS day, asset, sum(principal), uniqExact(hour), sum(interest),
			uniqExact(user_id, account_type, symbol)
		FROM margin_interest FINAL WHERE {range:hour} AND {kind:user_id} AND (? = '' OR asset = ?) GROUP BY day, asset`
	// marginInterestPrices takes the pairs it reads (%s): one asset's own
	// (symbol = ?, the trades' key) or every USDT pair.
	marginInterestPrices = `SELECT {day:executed_at} AS day, substring(symbol, 1, length(symbol) - 5) AS base, argMax(price, executed_at)
		FROM trades FINAL WHERE {range:executed_at} AND %s GROUP BY day, symbol`
)

// MarginInterest returns the interest per bucket and asset of the period.
func (r Reports) MarginInterest(ctx context.Context, rng ports.ReportRange, asset string) ([]ports.MarginInterestBucket, error) {
	ctx = kindCtx(ctx, rng.ByKind)
	owed := map[string]decimal.Decimal{}
	byKind, byKindArgs := kindCond("owner_id", rng.ByKind)
	rows, err := r.Conn.Query(ctx, strings.Replace(marginInterestBefore, "{kind}", byKind, 1),
		append([]any{rng.From.UTC().Format(chTime), asset, asset}, byKindArgs...)...)
	if err != nil {
		return nil, unavailable(err)
	}
	for rows.Next() {
		var a string
		var v decimal.Decimal
		if err := rows.Scan(&a, &v); err != nil {
			_ = rows.Close()
			return nil, unavailable(err)
		}
		owed[a] = v
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	type key struct{ day, asset string }
	out := []ports.MarginInterestBucket{}
	index := map[key]int{}
	query, args := ranged(marginInterestLedger, rng)
	if rows, err = r.Conn.Query(ctx, query, append(args, asset, asset)...); err != nil {
		return nil, unavailable(err)
	}
	for rows.Next() {
		var day time.Time
		var b ports.MarginInterestBucket
		var net decimal.Decimal
		if err := rows.Scan(&day, &b.Asset, &b.Charged, &b.Repaid, &net); err != nil {
			_ = rows.Close()
			return nil, unavailable(err)
		}
		b.Day = day.Format(time.DateOnly)
		owed[b.Asset] = owed[b.Asset].Sub(net)
		b.Owed = owed[b.Asset]
		index[key{b.Day, b.Asset}] = len(out)
		out = append(out, b)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	query, args = ranged(marginInterestCharges, rng)
	if rows, err = r.Conn.Query(ctx, query, append(args, asset, asset)...); err != nil {
		return nil, unavailable(err)
	}
	for rows.Next() {
		var day time.Time
		var a string
		var principal, interest decimal.Decimal
		var hours, accounts uint64
		if err := rows.Scan(&day, &a, &principal, &hours, &interest, &accounts); err != nil {
			_ = rows.Close()
			return nil, unavailable(err)
		}
		i, ok := index[key{day.Format(time.DateOnly), a}]
		if !ok {
			continue // charged in margin-service, not yet in the ledger's lines
		}
		b := &out[i]
		if hours > 0 {
			b.PrincipalAvg = principal.DivRound(decimal.NewFromUint64(hours), 8)
		}
		if principal.IsPositive() {
			b.HourlyRateAvg = interest.DivRound(principal, 12)
		}
		b.Accounts = accounts
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	pairs, pairArgs := "endsWith(symbol, '-USDT')", []any(nil)
	if asset != "" {
		pairs, pairArgs = "symbol = ?", []any{asset + "-USDT"}
	}
	query, args = ranged(fmt.Sprintf(marginInterestPrices, pairs), rng)
	if rows, err = r.Conn.Query(ctx, query, append(args, pairArgs...)...); err != nil {
		return nil, unavailable(err)
	}
	prices := map[key]decimal.Decimal{}
	for rows.Next() {
		var day time.Time
		var base string
		var price decimal.Decimal
		if err := rows.Scan(&day, &base, &price); err != nil {
			_ = rows.Close()
			return nil, unavailable(err)
		}
		prices[key{day.Format(time.DateOnly), base}] = price
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	for i := range out {
		b := &out[i]
		price, ok := prices[key{b.Day, b.Asset}]
		if b.Asset == "USDT" {
			price, ok = decimal.NewFromInt(1), true
		}
		if ok && price.IsPositive() {
			charged, repaid := b.Charged.Mul(price).Round(8).String(), b.Repaid.Mul(price).Round(8).String()
			b.ChargedUSDT, b.RepaidUSDT = &charged, &repaid
		}
	}
	return out, nil
}

// closeRows ends a read, with the error that stopped it.
func closeRows(rows interface {
	Err() error
	Close() error
},
) error {
	err := rows.Err()
	if cerr := rows.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return unavailable(err)
	}
	return nil
}
