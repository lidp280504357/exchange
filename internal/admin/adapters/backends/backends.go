// Package backends connects the admin console to the services it acts on:
// gRPC to auth-, user-, ledger- and instrument-service, internal REST to
// wallet-, spot-trading- and derivatives-service (never routed by the
// gateway), the shared config schema for feature flags, and ClickHouse
// for the audit trail and the read models.
package backends

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	authv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/auth/v1"
	instrumentv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/instrument/v1"
	ledgerv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/ledger/v1"
	userv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/user/v1"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/flags"
	"github.com/lidp280504357/exchange/internal/platform/outbox"
	"github.com/lidp280504357/exchange/internal/platform/pagecursor"
	"github.com/lidp280504357/exchange/internal/platform/pg"
)

// Users implements ports.Users.
type Users struct {
	Auth   authv1.AuthServiceClient
	User   userv1.UserServiceClient
	Ledger ledgerv1.LedgerServiceClient
}

// Find returns the user of an email address or phone number.
func (u Users) Find(ctx context.Context, identifier string) (string, error) {
	resp, err := u.Auth.FindUser(ctx, &authv1.FindUserRequest{Identifier: identifier})
	if err != nil {
		return "", err
	}
	return resp.GetUserId(), nil
}

// Get returns an account.
func (u Users) Get(ctx context.Context, userID string) (ports.User, error) {
	resp, err := u.User.GetUser(ctx, &userv1.GetUserRequest{UserId: userID})
	if err != nil {
		return ports.User{}, err
	}
	p := resp.GetUser()
	out := ports.User{ID: p.GetId(), Status: p.GetStatus(), Region: p.GetRegion(), Language: p.GetLanguage(), KYCLevel: p.GetKycLevel()}
	if t := p.GetCreatedAt(); t != nil {
		out.CreatedAt = t.AsTime()
	}
	return out, nil
}

// Balances returns a user's balances.
func (u Users) Balances(ctx context.Context, userID string) ([]ports.Balance, error) {
	resp, err := u.Ledger.GetBalances(ctx, &ledgerv1.GetBalancesRequest{UserId: userID})
	if err != nil {
		return nil, err
	}
	out := make([]ports.Balance, 0, len(resp.GetBalances()))
	for _, b := range resp.GetBalances() {
		out = append(out, ports.Balance{AccountType: b.GetAccountType(), Asset: b.GetAsset(), Available: b.GetAvailable(), Frozen: b.GetFrozen()})
	}
	return out, nil
}

// ChangeStatus moves an account to another status.
func (u Users) ChangeStatus(ctx context.Context, userID, to, reason, actor, note string) (string, error) {
	resp, err := u.User.ChangeStatus(ctx, &userv1.ChangeStatusRequest{UserId: userID, ToStatus: to, ReasonCode: reason, Actor: actor, Note: note})
	if err != nil {
		return "", err
	}
	return resp.GetFromStatus(), nil
}

// List pages through accounts newest first.
func (u Users) List(ctx context.Context, q ports.UserQuery) ([]ports.User, string, error) {
	req := &userv1.ListUsersRequest{Status: q.Status, Region: q.Region, Cursor: q.Cursor, Limit: int32(min(q.Limit, 200))} //nolint:gosec // bounded
	if !q.CreatedFrom.IsZero() {
		req.CreatedFrom = timestamppb.New(q.CreatedFrom)
	}
	if !q.CreatedBefore.IsZero() {
		req.CreatedBefore = timestamppb.New(q.CreatedBefore)
	}
	resp, err := u.User.ListUsers(ctx, req)
	if err != nil {
		return nil, "", err
	}
	out := make([]ports.User, 0, len(resp.GetUsers()))
	for _, p := range resp.GetUsers() {
		v := ports.User{ID: p.GetId(), Status: p.GetStatus(), Region: p.GetRegion(), Language: p.GetLanguage(), KYCLevel: p.GetKycLevel()}
		if t := p.GetCreatedAt(); t != nil {
			v.CreatedAt = t.AsTime()
		}
		out = append(out, v)
	}
	return out, resp.GetNextCursor(), nil
}

// Stats counts accounts.
func (u Users) Stats(ctx context.Context, since time.Time, days int) (ports.UserStats, error) {
	resp, err := u.User.UserStats(ctx, &userv1.UserStatsRequest{Since: timestamppb.New(since), Days: int32(min(days, 90))}) //nolint:gosec // bounded
	if err != nil {
		return ports.UserStats{}, err
	}
	out := ports.UserStats{Total: resp.GetTotal(), CreatedSince: resp.GetCreatedSince(), Days: map[string]int64{}}
	for _, d := range resp.GetDays() {
		out.Days[d.GetDay()] = d.GetCount()
	}
	return out, nil
}

// Ledger implements ports.Ledger.
type Ledger struct{ C ledgerv1.LedgerServiceClient }

// Adjust books an approved manual adjustment.
func (l Ledger) Adjust(ctx context.Context, key, userID, asset string, amount decimal.Decimal, actor, reason string) (string, error) {
	resp, err := l.C.Adjust(ctx, &ledgerv1.AdjustRequest{
		IdempotencyKey: key, UserId: userID, Asset: asset, Amount: amount.String(), Reason: reason, Actor: actor,
	})
	if err != nil {
		return "", err
	}
	return resp.GetPosting().GetJournalId(), nil
}

// FundInsurance books an approved contribution to the insurance fund.
func (l Ledger) FundInsurance(ctx context.Context, key, asset string, amount decimal.Decimal, actor, reason string) (string, error) {
	resp, err := l.C.FundInsurance(ctx, &ledgerv1.FundInsuranceRequest{
		IdempotencyKey: key, Asset: asset, Amount: amount.String(), Reason: reason, Actor: actor,
	})
	if err != nil {
		return "", err
	}
	return resp.GetPosting().GetJournalId(), nil
}

// SystemBalances returns the platform's system accounts in an asset.
func (l Ledger) SystemBalances(ctx context.Context, asset string) ([]ports.Balance, error) {
	resp, err := l.C.GetSystemBalances(ctx, &ledgerv1.GetSystemBalancesRequest{Asset: asset})
	if err != nil {
		return nil, err
	}
	out := make([]ports.Balance, 0, len(resp.GetBalances()))
	for _, b := range resp.GetBalances() {
		out = append(out, ports.Balance{AccountType: b.GetAccountType(), Asset: b.GetAsset(), Available: b.GetAvailable(), Frozen: b.GetFrozen()})
	}
	return out, nil
}

// Instruments implements ports.Instruments.
type Instruments struct {
	C instrumentv1.InstrumentServiceClient
}

var protoJSON = protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}

// List returns the assets, pairs and contracts as JSON.
func (i Instruments) List(ctx context.Context) (json.RawMessage, error) {
	assets, err := i.C.ListAssets(ctx, &instrumentv1.ListAssetsRequest{})
	if err != nil {
		return nil, err
	}
	pairs, err := i.C.ListTradingPairs(ctx, &instrumentv1.ListTradingPairsRequest{})
	if err != nil {
		return nil, err
	}
	contracts, err := i.C.ListContracts(ctx, &instrumentv1.ListContractsRequest{})
	if err != nil {
		return nil, err
	}
	a, err := protoJSON.Marshal(assets)
	if err != nil {
		return nil, err
	}
	p, err := protoJSON.Marshal(pairs)
	if err != nil {
		return nil, err
	}
	c, err := protoJSON.Marshal(contracts)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(`{"assets":` + string(field(a, "assets")) + `,"pairs":` + string(field(p, "pairs")) +
		`,"contracts":` + string(field(c, "contracts")) + `}`), nil
}

// field extracts one array field of a JSON object ([] when missing).
func field(obj []byte, name string) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(obj, &m) != nil || m[name] == nil {
		return json.RawMessage("[]")
	}
	return m[name]
}

// SetPairStatus moves a trading pair to another status.
func (i Instruments) SetPairStatus(ctx context.Context, symbol, to, reason, actor string) (string, error) {
	resp, err := i.C.SetPairStatus(ctx, &instrumentv1.SetPairStatusRequest{Symbol: strings.ToUpper(symbol), ToStatus: to, Reason: reason, Actor: actor})
	if err != nil {
		return "", err
	}
	return resp.GetFromStatus(), nil
}

// SetContractStatus moves a perpetual contract to another status.
func (i Instruments) SetContractStatus(ctx context.Context, symbol, to, reason, actor string) (string, error) {
	resp, err := i.C.SetContractStatus(ctx, &instrumentv1.SetContractStatusRequest{
		Symbol: strings.ToUpper(symbol), ToStatus: to, Reason: reason, Actor: actor,
	})
	if err != nil {
		return "", err
	}
	return resp.GetFromStatus(), nil
}

// REST calls internal HTTP APIs.
type REST struct {
	Client *http.Client
}

// restError turns an error answer of a service into its apperr.
func restError(resp *http.Response, body []byte) error {
	var e struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	}
	kind := apperr.KindInternal
	switch {
	case resp.StatusCode == http.StatusNotFound:
		kind = apperr.KindNotFound
	case resp.StatusCode == http.StatusConflict:
		kind = apperr.KindConflict
	case resp.StatusCode == http.StatusForbidden:
		kind = apperr.KindForbidden
	case resp.StatusCode == http.StatusBadRequest:
		kind = apperr.KindInvalid
	case resp.StatusCode == http.StatusUnprocessableEntity:
		kind = apperr.KindUnprocessable
	case resp.StatusCode >= 500:
		kind = apperr.KindUnavailable
	}
	if json.Unmarshal(body, &e) != nil || e.Code == "" {
		return apperr.New(kind, apperr.CodeUnavailable, fmt.Sprintf("the service answered HTTP %d", resp.StatusCode))
	}
	err := apperr.New(kind, e.Code, e.Message)
	for k, v := range e.Details {
		err = err.WithDetail(k, v)
	}
	return err
}

func (r REST) do(ctx context.Context, method, url string, body any, header map[string]string) (json.RawMessage, error) {
	var payload io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, payload)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "service unreachable")
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, restError(resp, raw)
	}
	return raw, nil
}

// Wallet implements ports.Withdrawals over wallet-service's internal API.
type Wallet struct {
	REST
	Base string
}

// List returns a page of withdrawals.
func (w Wallet) List(ctx context.Context, q ports.WithdrawalQuery) (json.RawMessage, error) {
	v := url.Values{}
	for k, x := range map[string]string{"status": q.Status, "user_id": q.UserID, "asset": q.Asset, "cursor": q.Cursor, "order": q.Order} {
		if x != "" {
			v.Set(k, x)
		}
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	return w.do(ctx, http.MethodGet, w.Base+"/internal/wallet/withdrawals?"+v.Encode(), nil, nil)
}

// Market implements ports.Market over market-data-service's internal API.
type Market struct {
	REST
	Base string
}

// Feed returns the reference feed's state.
func (m Market) Feed(ctx context.Context) (ports.FeedStatus, error) {
	raw, err := m.do(ctx, http.MethodGet, m.Base+"/internal/market/feed", nil, nil)
	if err != nil {
		return ports.FeedStatus{}, err
	}
	var out ports.FeedStatus
	if err := json.Unmarshal(raw, &out); err != nil {
		return ports.FeedStatus{}, apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "market-data answered badly")
	}
	return out, nil
}

// Review approves or rejects a withdrawal.
func (w Wallet) Review(ctx context.Context, id string, approve bool, reviewer, reason string) (json.RawMessage, error) {
	return w.do(ctx, http.MethodPost, w.Base+"/internal/wallet/withdrawals/"+url.PathEscape(id)+"/review",
		map[string]any{"approve": approve, "reviewer": reviewer, "reason": reason}, nil)
}

// Trading implements ports.Orders over spot-trading-service's API with the
// identity header the gateway would set.
type Trading struct {
	REST
	Base string
}

// CancelAll asks the engine to cancel every open order of a user.
func (t Trading) CancelAll(ctx context.Context, userID string) error {
	_, err := t.do(ctx, http.MethodDelete, t.Base+"/v1/orders", nil, map[string]string{"X-User-Id": userID})
	return err
}

// Derivatives implements ports.Derivatives over derivatives-service's
// internal API.
type Derivatives struct {
	REST
	Base string
}

// Contracts returns each contract's status, reduce-only state, mark price
// and open interest.
func (d Derivatives) Contracts(ctx context.Context) (json.RawMessage, error) {
	return d.do(ctx, http.MethodGet, d.Base+"/internal/derivatives/contracts", nil, nil)
}

// LiftReduceOnly ends a contract's reduce-only.
func (d Derivatives) LiftReduceOnly(ctx context.Context, symbol, actor string) (json.RawMessage, error) {
	return d.do(ctx, http.MethodPost, d.Base+"/internal/derivatives/contracts/"+url.PathEscape(strings.ToUpper(symbol))+"/lift-reduce-only",
		map[string]string{"actor": actor}, nil)
}

// Risk returns the positions warned, taken over or close to it.
func (d Derivatives) Risk(ctx context.Context) (json.RawMessage, error) {
	return d.do(ctx, http.MethodGet, d.Base+"/internal/derivatives/risk", nil, nil)
}

// Flags implements ports.Flags on the shared config schema; a switch is
// written with its ConfigChanged audit event in one transaction, published
// by the config schema's outbox relay.
type Flags struct {
	DB     *pg.DB
	Events *event.Factory
}

func toPort(f flags.Flag) ports.Flag {
	rules, _ := json.Marshal(f.Rules)
	out := ports.Flag{Key: f.Key, Enabled: f.Enabled, Description: f.Description, Rules: rules, Version: f.Version, UpdatedBy: f.UpdatedBy}
	if !f.UpdatedAt.IsZero() {
		out.UpdatedAt = &f.UpdatedAt
	}
	return out
}

// List returns every flag by key.
func (f Flags) List(ctx context.Context) ([]ports.Flag, error) {
	all, err := flags.Load(ctx, f.DB)
	if err != nil {
		return nil, err
	}
	// Known flags never set are off (requirements §5.14).
	for key, desc := range flags.Known {
		if _, ok := all[key]; !ok {
			all[key] = flags.Flag{Key: key, Description: desc}
		}
	}
	out := make([]ports.Flag, 0, len(all))
	for _, fl := range all {
		out = append(out, toPort(fl))
	}
	slices.SortFunc(out, func(a, b ports.Flag) int { return strings.Compare(a.Key, b.Key) })
	return out, nil
}

// Switch turns a known flag on or off, keeping its rules; a flag never
// set is created without rules.
func (f Flags) Switch(ctx context.Context, key string, enabled bool, actor, reason string) (ports.Flag, error) {
	all, err := flags.Load(ctx, f.DB)
	if err != nil {
		return ports.Flag{}, err
	}
	cur, ok := all[key]
	if !ok {
		desc, known := flags.Known[key]
		if !known {
			return ports.Flag{}, apperr.NotFound("no such flag")
		}
		cur = flags.Flag{Key: key, Description: desc}
	}
	cur.Enabled = enabled
	var stored flags.Flag
	err = f.DB.InTx(ctx, func(tx pgx.Tx) error {
		old, s, err := flags.Set(ctx, tx, cur, actor, reason)
		if err != nil {
			return err
		}
		stored = s
		oldJSON, _ := json.Marshal(old)
		newJSON, _ := json.Marshal(s)
		env, err := f.Events.New(ctx, &auditv1.ConfigChanged{
			Target: "flag:" + key, OldValue: string(oldJSON), NewValue: string(newJSON), Actor: actor, Reason: reason,
		}, "actor", actor)
		if err != nil {
			return err
		}
		return outbox.Add(ctx, tx, event.TopicAudit, env)
	})
	if err != nil {
		return ports.Flag{}, err
	}
	return toPort(stored), nil
}

// Audit implements ports.AuditLog on ClickHouse audit_logs.
type Audit struct{ Conn driver.Conn }

// page is a decoded cursor: the last item's time and ID.
type page struct {
	on    bool
	at    time.Time
	id    string
	limit int
}

func pageOf(cursor string, limit int) (page, error) {
	at, id, err := pagecursor.Decode(cursor)
	if err != nil {
		return page{}, apperr.Invalid("bad cursor")
	}
	return page{on: cursor != "", at: at, id: id, limit: limit}, nil
}

// next is the cursor after the n-th item when the query found more
// (limit+1 rows asked for).
func (p page) next(found int, at func(i int) (time.Time, string)) string {
	if found <= p.limit {
		return ""
	}
	t, id := at(p.limit - 1)
	return pagecursor.Encode(t, id)
}

// timeRange bounds a ClickHouse time column; zero ends are open.
func timeRange(from, to time.Time) (time.Time, time.Time) {
	if to.IsZero() {
		to = time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return from.UTC(), to.UTC()
}

// Search returns a page of audit entries, newest first.
func (a Audit) Search(ctx context.Context, q ports.AuditQuery) ([]ports.AuditEntry, string, error) {
	pc, err := pageOf(q.Cursor, q.Limit)
	if err != nil {
		return nil, "", err
	}
	from, to := timeRange(q.From, q.To)
	rows, err := a.Conn.Query(ctx, `SELECT toString(event_id), event_type, actor_id, target, occurred_at, payload FROM audit_logs FINAL
		WHERE (? = '' OR actor_id = ?) AND (? = '' OR target = ?) AND (? = '' OR event_type = ?)
		AND occurred_at >= ? AND occurred_at < ? AND (NOT ? OR (occurred_at, toString(event_id)) < (?, ?))
		ORDER BY occurred_at DESC, toString(event_id) DESC LIMIT ?`,
		q.Actor, q.Actor, q.Target, q.Target, q.EventType, q.EventType, from, to, pc.on, pc.at, pc.id, pc.limit+1)
	if err != nil {
		return nil, "", apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "the audit trail is unavailable")
	}
	defer func() { _ = rows.Close() }()
	out := []ports.AuditEntry{}
	for rows.Next() {
		var e ports.AuditEntry
		var payload string
		if err := rows.Scan(&e.EventID, &e.EventType, &e.Actor, &e.Target, &e.OccurredAt, &payload); err != nil {
			return nil, "", err
		}
		if json.Valid([]byte(payload)) {
			e.Payload = json.RawMessage(payload)
		} else {
			e.Payload, _ = json.Marshal(payload)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := pc.next(len(out), func(i int) (time.Time, string) { return out[i].OccurredAt, out[i].EventID })
	return out[:min(len(out), pc.limit)], next, nil
}

// Reports implements ports.Reports on the ClickHouse read models
// (migrations/clickhouse/00004_read_models.sql).
type Reports struct{ Conn driver.Conn }

const tradingReport = `SELECT day, symbol, trades, volume, quote_volume, orders, rejected FROM
	(
		SELECT toDate(executed_at) AS day, symbol, count() AS trades, sum(quantity) AS volume, sum(quote_quantity) AS quote_volume
		FROM trades FINAL WHERE executed_at >= toDateTime64(today() - ?, 3, 'UTC') GROUP BY day, symbol
	) AS t
	FULL OUTER JOIN
	(
		SELECT toDate(occurred_at) AS day, symbol, countIf(status = 'NEW') AS orders, countIf(status = 'REJECTED') AS rejected
		FROM order_updates FINAL WHERE occurred_at >= toDateTime64(today() - ?, 3, 'UTC') GROUP BY day, symbol
	) AS o USING (day, symbol)
	ORDER BY day DESC, symbol`

const walletReport = `SELECT day, asset, deposits, deposit_amount, withdrawals, withdrawal_amount, withdrawal_fees FROM
	(
		SELECT toDate(updated_at) AS day, asset, count() AS deposits, sum(amount) AS deposit_amount
		FROM wallet_deposits FINAL WHERE status = 'CREDITED' AND NOT unclaimed AND updated_at >= toDateTime64(today() - ?, 3, 'UTC')
		GROUP BY day, asset
	) AS d
	FULL OUTER JOIN
	(
		SELECT toDate(updated_at) AS day, asset, count() AS withdrawals, sum(amount) AS withdrawal_amount, sum(fee) AS withdrawal_fees
		FROM wallet_withdrawals FINAL WHERE status = 'CONFIRMED' AND updated_at >= toDateTime64(today() - ?, 3, 'UTC')
		GROUP BY day, asset
	) AS w USING (day, asset)
	ORDER BY day DESC, asset`

// derivativesReport sums each contract's day: its fills (volume and
// notional once per trade, from the buying side), the funding its
// positions paid and received at the day's settlements, and its
// liquidations.
const derivativesReport = `SELECT day, symbol, fills, volume, notional, fees, realized_pnl, funding_paid, funding_received,
		liquidations, adl, insurance_paid FROM
	(
		SELECT toDate(executed_at) AS day, symbol, count() AS fills, sumIf(quantity, side = 'BUY') AS volume,
			sumIf(notional, side = 'BUY') AS notional, sum(fee) AS fees, sum(realized_pnl) AS realized_pnl
		FROM derivatives_fills FINAL WHERE executed_at >= toDateTime64(today() - ?, 3, 'UTC') GROUP BY day, symbol
	) AS f
	FULL OUTER JOIN
	(
		SELECT toDate(funding_time) AS day, symbol, -sumIf(amount, amount < 0) AS funding_paid,
			sumIf(amount, amount > 0) AS funding_received
		FROM derivatives_funding FINAL WHERE funding_time >= toDateTime(today() - ?, 'UTC') GROUP BY day, symbol
	) AS u USING (day, symbol)
	FULL OUTER JOIN
	(
		SELECT toDate(occurred_at) AS day, symbol, countIf(kind = 'STARTED') AS liquidations, countIf(kind = 'ADL') AS adl,
			sumIf(insurance_paid, kind = 'FILLED') AS insurance_paid
		FROM derivatives_liquidations FINAL WHERE symbol != '' AND occurred_at >= toDateTime64(today() - ?, 3, 'UTC')
		GROUP BY day, symbol
	) AS l USING (day, symbol)
	ORDER BY day DESC, symbol`

func unavailable(err error) error {
	return apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "the read models are unavailable")
}

// Trading returns trades and orders per symbol and day for the last days
// (today included).
func (r Reports) Trading(ctx context.Context, days int) ([]ports.TradingDay, error) {
	rows, err := r.Conn.Query(ctx, tradingReport, days-1, days-1)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()
	out := []ports.TradingDay{}
	for rows.Next() {
		var d ports.TradingDay
		var day time.Time
		var volume, quote decimal.Decimal
		if err := rows.Scan(&day, &d.Symbol, &d.Trades, &volume, &quote, &d.Orders, &d.Rejected); err != nil {
			return nil, unavailable(err)
		}
		d.Day, d.Volume, d.QuoteVolume = day.Format(time.DateOnly), volume.String(), quote.String()
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// Wallet returns credited deposits and confirmed withdrawals per asset and
// day for the last days.
func (r Reports) Wallet(ctx context.Context, days int) ([]ports.WalletDay, error) {
	rows, err := r.Conn.Query(ctx, walletReport, days-1, days-1)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()
	out := []ports.WalletDay{}
	for rows.Next() {
		var d ports.WalletDay
		var day time.Time
		var deposited, withdrawn, fees decimal.Decimal
		if err := rows.Scan(&day, &d.Asset, &d.Deposits, &deposited, &d.Withdrawals, &withdrawn, &fees); err != nil {
			return nil, unavailable(err)
		}
		d.Day, d.DepositAmount, d.WithdrawalAmount, d.WithdrawalFees = day.Format(time.DateOnly), deposited.String(), withdrawn.String(),
			fees.String()
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// Candles returns the newest candles of an interval, newest first.
func (r Reports) Candles(ctx context.Context, symbol string, seconds uint32, limit int) ([]ports.Candle, error) {
	rows, err := r.Conn.Query(ctx, `SELECT open_time, open, high, low, close, volume, quote_volume, trades
		FROM candles(symbol = ?, seconds = ?) ORDER BY open_time DESC LIMIT ?`, symbol, seconds, limit)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()
	out := []ports.Candle{}
	for rows.Next() {
		var c ports.Candle
		var o, h, l, cl, v, qv decimal.Decimal
		if err := rows.Scan(&c.OpenTime, &o, &h, &l, &cl, &v, &qv, &c.Trades); err != nil {
			return nil, unavailable(err)
		}
		c.Open, c.High, c.Low, c.Close, c.Volume, c.QuoteVolume = o.String(), h.String(), l.String(), cl.String(), v.String(), qv.String()
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// Derivatives returns each contract's trading, funding and liquidations
// per day for the last days.
func (r Reports) Derivatives(ctx context.Context, days int) ([]ports.DerivativesDay, error) {
	rows, err := r.Conn.Query(ctx, derivativesReport, days-1, days-1, days-1)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()
	out := []ports.DerivativesDay{}
	for rows.Next() {
		var d ports.DerivativesDay
		var day time.Time
		var volume, notional, fees, pnl, paid, received, insurance decimal.Decimal
		if err := rows.Scan(&day, &d.Symbol, &d.Fills, &volume, &notional, &fees, &pnl, &paid, &received, &d.Liquidations, &d.ADL,
			&insurance); err != nil {
			return nil, unavailable(err)
		}
		d.Day, d.Volume, d.Notional, d.Fees, d.RealizedPnL = day.Format(time.DateOnly), volume.String(), notional.String(), fees.String(),
			pnl.String()
		d.FundingPaid, d.FundingReceived, d.InsurancePaid = paid.String(), received.String(), insurance.String()
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// OpenInterest returns each contract's open long and short quantity and
// positions from the latest position snapshots.
func (r Reports) OpenInterest(ctx context.Context) ([]ports.OpenInterest, error) {
	rows, err := r.Conn.Query(ctx, `SELECT symbol, sumIf(quantity, quantity > 0) AS long_qty, -sumIf(quantity, quantity < 0) AS short_qty,
		countIf(quantity != 0) AS open_positions
		FROM derivatives_positions FINAL GROUP BY symbol HAVING open_positions > 0 ORDER BY symbol`)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()
	out := []ports.OpenInterest{}
	for rows.Next() {
		var o ports.OpenInterest
		var long, short decimal.Decimal
		if err := rows.Scan(&o.Symbol, &long, &short, &o.Positions); err != nil {
			return nil, unavailable(err)
		}
		o.Long, o.Short = long.String(), short.String()
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// Liquidations returns a page of the liquidation steps of the last days,
// newest first.
func (r Reports) Liquidations(ctx context.Context, days int, kind, cursor string, limit int) ([]ports.LiquidationStep, string, error) {
	pc, err := pageOf(cursor, limit)
	if err != nil {
		return nil, "", err
	}
	rows, err := r.Conn.Query(ctx, `SELECT toString(event_id), kind, toString(user_id), symbol, position_side, cross_margin, adl, trade_id,
		price, quantity, realized_pnl, insurance_paid, mark_price, bankruptcy_price, margin_balance, maintenance_margin, occurred_at
		FROM derivatives_liquidations FINAL
		WHERE occurred_at >= toDateTime64(today() - ?, 3, 'UTC') AND (? = '' OR kind = ?)
		AND (NOT ? OR (occurred_at, toString(event_id)) < (?, ?))
		ORDER BY occurred_at DESC, toString(event_id) DESC LIMIT ?`, days-1, kind, kind, pc.on, pc.at, pc.id, pc.limit+1)
	if err != nil {
		return nil, "", unavailable(err)
	}
	defer func() { _ = rows.Close() }()
	out := []ports.LiquidationStep{}
	for rows.Next() {
		var l ports.LiquidationStep
		var v [8]decimal.Decimal
		if err := rows.Scan(&l.EventID, &l.Kind, &l.UserID, &l.Symbol, &l.PositionSide, &l.Cross, &l.ADL, &l.TradeID, &v[0], &v[1], &v[2],
			&v[3], &v[4], &v[5], &v[6], &v[7], &l.OccurredAt); err != nil {
			return nil, "", unavailable(err)
		}
		l.Price, l.Quantity, l.RealizedPnL, l.InsurancePaid = v[0].String(), v[1].String(), v[2].String(), v[3].String()
		l.MarkPrice, l.BankruptcyPrice, l.MarginBalance, l.MaintenanceMargin = v[4].String(), v[5].String(), v[6].String(), v[7].String()
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, "", unavailable(err)
	}
	next := pc.next(len(out), func(i int) (time.Time, string) { return out[i].OccurredAt, out[i].EventID })
	return out[:min(len(out), pc.limit)], next, nil
}
