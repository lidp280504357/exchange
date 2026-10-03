package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/flags"
	"github.com/lidp280504357/exchange/internal/platform/password"
	"github.com/lidp280504357/exchange/internal/platform/secretbox"
	"github.com/lidp280504357/exchange/internal/platform/totp"
)

// memStore keeps the admin schema in maps; a transaction is not rolled
// back, which the service never relies on after a failed step.
type memStore struct {
	admins    map[string]domain.Admin
	sessions  map[string]domain.Session
	revoked   map[string]bool
	approvals map[string]domain.Approval
	settings  *domain.Settings
	notes     []domain.Note
	tags      map[string][]string
	changes   []domain.InstrumentChange
	audits    []*auditv1.AdminActionPerformed
	keys      map[string]memKey
}

// memKey is a claimed Idempotency-Key.
type memKey struct {
	hash []byte
	ref  string
	at   time.Time
}

func newMemStore() *memStore {
	return &memStore{
		admins: map[string]domain.Admin{}, sessions: map[string]domain.Session{}, revoked: map[string]bool{},
		approvals: map[string]domain.Approval{}, tags: map[string][]string{}, keys: map[string]memKey{},
	}
}

func (m *memStore) Keys() ports.IdempotencyRepo { return memKeys{m} }

type memKeys struct{ m *memStore }

func (r memKeys) Claim(_ context.Context, scope, key string, hash []byte, ref string, now time.Time) ([]byte, string, bool, error) {
	if k, ok := r.m.keys[scope+"\x00"+key]; ok {
		return k.hash, k.ref, false, nil
	}
	r.m.keys[scope+"\x00"+key] = memKey{hash: hash, ref: ref, at: now}
	return hash, ref, true, nil
}

func (r memKeys) Purge(_ context.Context, cutoff time.Time) (int64, error) {
	var n int64
	for id, k := range r.m.keys {
		if k.at.Before(cutoff) {
			delete(r.m.keys, id)
			n++
		}
	}
	return n, nil
}

func (m *memStore) Changes() ports.ChangeRepo { return memChanges{m} }

type memChanges struct{ m *memStore }

// withEmails fills in the emails of a change's administrators.
func (r memChanges) withEmails(c domain.InstrumentChange) domain.InstrumentChange {
	c.RequestedByEmail, c.ApprovedByEmail, c.ClosedByEmail = r.m.admins[c.RequestedBy].Email, r.m.admins[c.ApprovedBy].Email,
		r.m.admins[c.ClosedBy].Email
	return c
}

func (r memChanges) Create(_ context.Context, c domain.InstrumentChange) error {
	r.m.changes = append(r.m.changes, c)
	return nil
}

func (r memChanges) Get(_ context.Context, id string) (*domain.InstrumentChange, error) {
	for _, c := range r.m.changes {
		if c.ID == id {
			c = r.withEmails(c)
			return &c, nil
		}
	}
	return nil, nil
}

func (r memChanges) GetForUpdate(ctx context.Context, id string) (*domain.InstrumentChange, error) {
	return r.Get(ctx, id)
}

func (r memChanges) Update(_ context.Context, c domain.InstrumentChange) error {
	for i := range r.m.changes {
		if r.m.changes[i].ID == c.ID {
			r.m.changes[i] = c
		}
	}
	return nil
}

func (r memChanges) List(_ context.Context, status string, afterTime time.Time, afterID string, limit int) ([]domain.InstrumentChange, error) {
	var out []domain.InstrumentChange
	for i := len(r.m.changes) - 1; i >= 0; i-- {
		c := r.m.changes[i]
		after := afterID == "" || c.CreatedAt.Before(afterTime) || (c.CreatedAt.Equal(afterTime) && c.ID < afterID)
		if (status == "" || c.Status == status) && after {
			out = append(out, r.withEmails(c))
		}
	}
	return out[:min(limit, len(out))], nil
}

func (r memChanges) Due(_ context.Context, now time.Time, limit int) ([]domain.InstrumentChange, error) {
	var out []domain.InstrumentChange
	for _, c := range r.m.changes {
		claimed := !c.ApplyingAt.IsZero() && c.ApplyingAt.After(now.Add(-domain.ClaimHold))
		if c.Status == domain.ChangeScheduled && !c.EffectiveAt.After(now) && !claimed && len(out) < limit {
			out = append(out, r.withEmails(c))
		}
	}
	return out, nil
}

func (r memChanges) ByConfirmation(_ context.Context, hash string) (*domain.InstrumentChange, error) {
	for _, c := range r.m.changes {
		if c.ConfirmationHash == hash {
			c = r.withEmails(c)
			return &c, nil
		}
	}
	return nil, nil
}

func (r memChanges) Open(context.Context) (int, error) {
	n := 0
	for _, c := range r.m.changes {
		if c.Open() {
			n++
		}
	}
	return n, nil
}

func (m *memStore) Notes() ports.NoteRepo { return memNotes{m} }

func (m *memStore) Tags() ports.TagRepo { return memTags{m} }

type memNotes struct{ m *memStore }

func (r memNotes) Insert(_ context.Context, n domain.Note) error {
	r.m.notes = append(r.m.notes, n)
	return nil
}

func (r memNotes) List(_ context.Context, userID string, afterTime time.Time, afterID string, limit int) ([]domain.Note, error) {
	var out []domain.Note
	for i := len(r.m.notes) - 1; i >= 0; i-- {
		n := r.m.notes[i]
		after := afterID == "" || n.CreatedAt.Before(afterTime) || (n.CreatedAt.Equal(afterTime) && n.ID < afterID)
		if n.UserID == userID && after {
			out = append(out, n)
		}
	}
	return out[:min(limit, len(out))], nil
}

type memTags struct{ m *memStore }

func (r memTags) Of(_ context.Context, ids []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, id := range ids {
		if t := r.m.tags[id]; len(t) > 0 {
			out[id] = slices.Clone(t)
		}
	}
	return out, nil
}

func (r memTags) Set(_ context.Context, userID string, tags []string, _ string, _ time.Time) error {
	r.m.tags[userID] = slices.Clone(tags)
	return nil
}

func (r memTags) Users(_ context.Context, tag string, limit int) ([]string, error) {
	var out []string
	for user, tags := range r.m.tags {
		if slices.Contains(tags, tag) && len(out) < limit {
			out = append(out, user)
		}
	}
	slices.Sort(out)
	return out, nil
}

// fakeUsers knows some accounts.
type fakeUsers struct {
	known    map[string]ports.User
	balances []ports.Balance
}

func (u *fakeUsers) Find(context.Context, string) (string, error) {
	return "", apperr.NotFound("no such user")
}

func (u *fakeUsers) Get(_ context.Context, id string) (ports.User, error) {
	if v, ok := u.known[id]; ok {
		return v, nil
	}
	return ports.User{}, apperr.NotFound("no such user")
}

func (u *fakeUsers) Balances(context.Context, string) ([]ports.Balance, error) {
	return u.balances, nil
}

func (u *fakeUsers) ChangeStatus(context.Context, string, string, string, string, string) (string, error) {
	return "ACTIVE", nil
}

func (u *fakeUsers) List(context.Context, ports.UserQuery) ([]ports.User, string, error) {
	var out []ports.User
	for _, v := range u.known {
		out = append(out, v)
	}
	return out, "", nil
}

func (u *fakeUsers) Stats(context.Context, time.Time, int) (ports.UserStats, error) {
	return ports.UserStats{}, nil
}

func (m *memStore) Tx(_ context.Context, fn func(ports.Repos) error) error { return fn(m) }

func (m *memStore) Read() ports.Repos { return m }

func (m *memStore) Admins() ports.AdminRepo { return memAdmins{m} }

func (m *memStore) Sessions() ports.SessionRepo { return memSessions{m} }

func (m *memStore) Approvals() ports.ApprovalRepo { return memApprovals{m} }

func (m *memStore) Settings() ports.SettingsRepo { return memSettings{m} }

type memSettings struct{ m *memStore }

func (r memSettings) Get(context.Context) (*domain.Settings, error) { return r.m.settings, nil }

func (r memSettings) Put(_ context.Context, s domain.Settings) error {
	r.m.settings = &s
	return nil
}

func (m *memStore) Audit(_ context.Context, msg proto.Message, _ string) error {
	m.audits = append(m.audits, msg.(*auditv1.AdminActionPerformed))
	return nil
}

func (m *memStore) actions() []string {
	var out []string
	for _, a := range m.audits {
		out = append(out, a.GetAction())
	}
	return out
}

type memAdmins struct{ m *memStore }

func (r memAdmins) Insert(_ context.Context, a domain.Admin) error { r.m.admins[a.ID] = a; return nil }

func (r memAdmins) Update(_ context.Context, a domain.Admin) error { r.m.admins[a.ID] = a; return nil }

func (r memAdmins) ByEmail(_ context.Context, email string) (*domain.Admin, error) {
	for _, a := range r.m.admins {
		if a.Email == email {
			return &a, nil
		}
	}
	return nil, nil
}

func (r memAdmins) ByEmailForUpdate(ctx context.Context, email string) (*domain.Admin, error) {
	return r.ByEmail(ctx, email)
}

func (r memAdmins) Get(_ context.Context, id string) (*domain.Admin, error) {
	if a, ok := r.m.admins[id]; ok {
		return &a, nil
	}
	return nil, nil
}

func (r memAdmins) GetForUpdate(ctx context.Context, id string) (*domain.Admin, error) {
	return r.Get(ctx, id)
}

func (r memAdmins) BySetupForUpdate(_ context.Context, hash []byte) (*domain.Admin, error) {
	for _, a := range r.m.admins {
		if len(a.SetupHash) > 0 && string(a.SetupHash) == string(hash) {
			return &a, nil
		}
	}
	return nil, nil
}

func (memAdmins) LockRoster(context.Context) error { return nil }

func (r memAdmins) List(context.Context) ([]domain.Admin, error) {
	var out []domain.Admin
	for _, a := range r.m.admins {
		out = append(out, a)
	}
	return out, nil
}

type memSessions struct{ m *memStore }

func (r memSessions) Insert(_ context.Context, s domain.Session) error {
	r.m.sessions[string(s.TokenHash)] = s
	return nil
}

func (r memSessions) Get(_ context.Context, hash []byte) (*domain.Session, error) {
	s, ok := r.m.sessions[string(hash)]
	if !ok || r.m.revoked[string(hash)] {
		return nil, nil
	}
	return &s, nil
}

func (r memSessions) Touch(_ context.Context, hash []byte, now time.Time) error {
	s := r.m.sessions[string(hash)]
	s.LastSeenAt = now
	r.m.sessions[string(hash)] = s
	return nil
}

func (r memSessions) Revoke(_ context.Context, hash []byte, _ time.Time) error {
	r.m.revoked[string(hash)] = true
	return nil
}

func (r memSessions) RevokeAll(_ context.Context, adminID string, _ time.Time) error {
	for h, s := range r.m.sessions {
		if s.AdminID == adminID {
			r.m.revoked[h] = true
		}
	}
	return nil
}

func (r memSessions) RevokeOthers(_ context.Context, adminID string, keep []byte, _ time.Time) error {
	for h, s := range r.m.sessions {
		if s.AdminID == adminID && h != string(keep) {
			r.m.revoked[h] = true
		}
	}
	return nil
}

func (r memSessions) Live(_ context.Context, adminID string, now time.Time) ([]domain.Session, error) {
	out := []domain.Session{}
	for h, s := range r.m.sessions {
		if s.AdminID == adminID && !r.m.revoked[h] && s.Live(now) {
			out = append(out, s)
		}
	}
	return out, nil
}

type memApprovals struct{ m *memStore }

func (r memApprovals) Insert(_ context.Context, a domain.Approval) error {
	r.m.approvals[a.ID] = a
	return nil
}

func (r memApprovals) Update(_ context.Context, a domain.Approval) error {
	r.m.approvals[a.ID] = a
	return nil
}

func (r memApprovals) GetForUpdate(_ context.Context, id string) (*domain.Approval, error) {
	if a, ok := r.m.approvals[id]; ok {
		return &a, nil
	}
	return nil, nil
}

func (r memApprovals) Get(ctx context.Context, id string) (*domain.Approval, error) {
	return r.GetForUpdate(ctx, id)
}

func (r memApprovals) MarkAttempted(_ context.Context, id string, at time.Time, note string) error {
	a, ok := r.m.approvals[id]
	if !ok || a.Status != domain.ApprovalPending {
		return nil
	}
	if a.AttemptedAt.IsZero() {
		a.AttemptedAt = at
	}
	if note != "" {
		a.Result = note
	}
	r.m.approvals[id] = a
	return nil
}

func (r memApprovals) SingleUsage(_ context.Context, adminID string, since time.Time) (decimal.Decimal, error) {
	sum := decimal.Zero
	for _, a := range r.m.approvals {
		live := a.Status == domain.ApprovalPending || a.Status == domain.ApprovalExecuted
		if a.RequestedBy == adminID && a.Mode == domain.ModeSingle && !a.CreatedAt.Before(since) && live && a.ValueUSDT != nil {
			sum = sum.Add(a.ValueUSDT.Abs())
		}
	}
	return sum, nil
}

func (r memApprovals) CountPending(context.Context) (int, error) {
	n := 0
	for _, a := range r.m.approvals {
		if a.Status == domain.ApprovalPending {
			n++
		}
	}
	return n, nil
}

func (r memApprovals) List(_ context.Context, status string, afterTime time.Time, afterID string, limit int) ([]domain.Approval, error) {
	var out []domain.Approval
	for _, a := range r.m.approvals {
		after := afterID == "" || a.CreatedAt.Before(afterTime) || (a.CreatedAt.Equal(afterTime) && a.ID < afterID)
		if (status == "" || a.Status == status) && after {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b domain.Approval) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	return out[:min(limit, len(out))], nil
}

type adjustment struct {
	key, userID, account, asset, actor, memo string
	amount                                   decimal.Decimal
}

type fakeLedger struct {
	calls []adjustment
	err   error
	// refuse refuses the adjustments of one user (with err).
	refuse string
	holds  []ports.Hold
}

func (l *fakeLedger) Adjust(_ context.Context, key, userID, account, asset string, amount decimal.Decimal, actor, memo string) (string, error) {
	l.calls = append(l.calls, adjustment{key: key, userID: userID, account: account, asset: asset, actor: actor, amount: amount, memo: memo})
	if l.err != nil && (l.refuse == "" || l.refuse == userID) {
		return "", l.err
	}
	return "journal-1", nil
}

func (l *fakeLedger) PlaceHold(_ context.Context, id, userID, asset string, amount decimal.Decimal, actor, reason string) (ports.Hold, error) {
	if l.err != nil {
		return ports.Hold{}, l.err
	}
	for _, h := range l.holds {
		if h.ID == id { // the ledger returns a repeated hold
			return h, nil
		}
	}
	h := ports.Hold{ID: id, UserID: userID, AccountType: "SPOT", Asset: asset, Amount: amount.String(), Reason: reason, Actor: actor}
	l.holds = append(l.holds, h)
	return h, nil
}

func (l *fakeLedger) ReleaseHold(_ context.Context, id, actor, reason string) (ports.Hold, error) {
	for i, h := range l.holds {
		if h.ID == id {
			if !h.ReleasedAt.IsZero() {
				return ports.Hold{}, apperr.New(apperr.KindConflict, "LEDGER_HOLD_RELEASED", "released")
			}
			h.ReleasedAt, h.ReleasedBy, h.ReleaseReason = time.Now(), actor, reason
			l.holds[i] = h
			return h, nil
		}
	}
	return ports.Hold{}, apperr.NotFound("no such hold")
}

func (l *fakeLedger) Holds(_ context.Context, userID string, activeOnly bool) ([]ports.Hold, error) {
	var out []ports.Hold
	for _, h := range l.holds {
		if h.UserID == userID && (!activeOnly || h.ReleasedAt.IsZero()) {
			out = append(out, h)
		}
	}
	return out, nil
}

func (l *fakeLedger) FundInsurance(_ context.Context, key, asset string, amount decimal.Decimal, actor, memo string) (string, error) {
	l.calls = append(l.calls, adjustment{key: key, asset: asset, actor: actor, amount: amount, memo: memo})
	if l.err != nil {
		return "", l.err
	}
	return "journal-2", nil
}

func (l *fakeLedger) SystemBalances(_ context.Context, asset string) ([]ports.Balance, error) {
	return []ports.Balance{
		{AccountType: "FEE_REVENUE", Asset: asset, Available: "7", Frozen: "0"},
		{AccountType: "INSURANCE_FUND", Asset: asset, Available: "1000000", Frozen: "0"},
		{AccountType: "PNL_CLEARING", Asset: asset, Available: "-12.5", Frozen: "0"},
	}, nil
}

type fakeDerivatives struct {
	lifted []string
	// pending is how many close attempts answer DERIV_CLOSE_PENDING.
	pending  int
	closes   []string
	canceled []string
	queries  []ports.PositionQuery
	// impactDown makes TierImpact fail; tiers records what it measured;
	// unmeasured and more are positions it cannot measure and liquidates
	// beyond the 3 it does.
	impactDown bool
	tiers      []string
	unmeasured int
	more       int
	// outcome is how the closing order ends (FILLED unless set), seen
	// after looks of Order.
	outcome string
	filled  string
	looks   int
}

func (d *fakeDerivatives) CrossMargin(_ context.Context, _ string, debit decimal.Decimal) (json.RawMessage, error) {
	state := "HEALTHY"
	if debit.GreaterThan(decimal.NewFromInt(500)) {
		state = "LIQUIDATE"
	}
	return json.RawMessage(`{"asset":"USDT","positions":1,"unmeasured":false,"equity":"1000","maintenance":"500","state":"HEALTHY",` +
		`"equity_after":"` + decimal.NewFromInt(1000).Sub(debit).String() + `","state_after":"` + state + `"}`), nil
}

func (d *fakeDerivatives) Order(_ context.Context, _, id string) (json.RawMessage, error) {
	d.looks++
	status, filled := d.outcome, d.filled
	if status == "" {
		status, filled = "FILLED", "0.2"
	}
	return json.RawMessage(`{"order_id":"` + id + `","status":"` + status + `","quantity":"0.2","filled_quantity":"` + filled + `"}`), nil
}

func (d *fakeDerivatives) TierImpact(_ context.Context, symbol string, tiers json.RawMessage) (ports.TierImpact, error) {
	if d.impactDown {
		return ports.TierImpact{}, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "derivatives-service is down")
	}
	d.tiers = append(d.tiers, symbol+" "+string(tiers))
	return ports.TierImpact{
		Symbol: symbol, Positions: 12, Liquidated: 3 + d.more, Notional: "45000.00", Accounts: 2, Unmeasured: d.unmeasured,
		Examples: json.RawMessage("[]"),
	}, nil
}

func (d *fakeDerivatives) PriceImpact(_ context.Context, symbol, price string) (json.RawMessage, error) {
	d.tiers = append(d.tiers, symbol+" at "+price)
	return json.RawMessage(`{"symbol":"` + symbol + `","target_price":"` + price + `","positions":7,"liquidated":2,"notional":"900.00","accounts":1,` +
		`"insurance_cost":"120.00","unmeasured":0,"examples":[]}`), nil
}

func (d *fakeDerivatives) Positions(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`[{"symbol":"BTC-USDT-PERP","position_side":"BOTH","quantity":"0.2"}]`), nil
}

func (d *fakeDerivatives) OpenPositions(_ context.Context, q ports.PositionQuery) (json.RawMessage, error) {
	d.queries = append(d.queries, q)
	return json.RawMessage(`{"positions":[{"user_id":"0192a000-0000-7000-8000-000000000001","symbol":"BTC-USDT-PERP","quantity":"0.5"},` +
		`{"user_id":"0192a000-0000-7000-8000-000000000002","symbol":"BTC-USDT-PERP","quantity":"-0.2"},` +
		`{"user_id":"house","symbol":"BTC-USDT-PERP","quantity":"-3"}],"truncated":false}`), nil
}

func (d *fakeDerivatives) OpenOrders(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{"items":[],"next_cursor":null}`), nil
}

func (d *fakeDerivatives) CancelOrder(_ context.Context, _, id string) (json.RawMessage, error) {
	d.canceled = append(d.canceled, id)
	return json.RawMessage(`{"order_id":"` + id + `","status":"OPEN","cancel_requested":true}`), nil
}

func (d *fakeDerivatives) ClosePosition(_ context.Context, _, symbol, side, client string) (json.RawMessage, error) {
	d.closes = append(d.closes, symbol+" "+side+" "+client)
	if d.pending > 0 {
		d.pending--
		return nil, apperr.New(apperr.KindConflict, "DERIV_CLOSE_PENDING", "pending")
	}
	return json.RawMessage(`{"order_id":"0192a000-0000-7000-8000-0000000000c1","client_order_id":"` + client + `","status":"NEW"}`), nil
}

func (d *fakeDerivatives) Contracts(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"contracts":[]}`), nil
}

func (d *fakeDerivatives) LiftReduceOnly(_ context.Context, symbol, actor string) (json.RawMessage, error) {
	d.lifted = append(d.lifted, symbol+" by "+actor)
	return json.RawMessage(`{"symbol":"` + symbol + `","lifted":true}`), nil
}

func (d *fakeDerivatives) Risk(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"positions":[]}`), nil
}

type fakeFlags struct{ switched []string }

func (f *fakeFlags) List(context.Context) ([]ports.Flag, error) {
	return []ports.Flag{{Key: "wallet.withdraw"}}, nil
}

func (f *fakeFlags) Switch(_ context.Context, key string, enabled bool, actor, _ string) (ports.Flag, error) {
	f.switched = append(f.switched, key)
	return ports.Flag{Key: key, Enabled: enabled, UpdatedBy: actor}, nil
}

type fakeOrders struct{ canceled []string }

func (o *fakeOrders) CancelAll(_ context.Context, userID string) error {
	o.canceled = append(o.canceled, userID)
	return nil
}

func (o *fakeOrders) Cancel(_ context.Context, userID, id string) (json.RawMessage, error) {
	o.canceled = append(o.canceled, userID+"/"+id)
	return json.RawMessage(`{"order_id":"` + id + `","status":"OPEN"}`), nil
}

type fakeWallet struct {
	reviewer string
	soleMax  decimal.Decimal
	reviewed []string
	// pending is the review queue's length.
	pending int
	err     error
	// atLeast is the last review's raise of the reviewers needed.
	atLeast int
	// withdrawals are the ones reviewed or set, by ID.
	withdrawals map[string]reviewedWithdrawal
	// suspended are the assets whose withdrawals are suspended.
	suspended map[string]ports.Suspension
}

func (w *fakeWallet) List(context.Context, ports.WithdrawalQuery) (json.RawMessage, error) {
	if w.err != nil {
		return nil, w.err
	}
	items := make([]string, w.pending)
	for i := range items {
		items[i] = "{}"
	}
	return json.RawMessage(`{"items":[` + strings.Join(items, ",") + `]}`), nil
}

func (w *fakeWallet) Custody(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (w *fakeWallet) Callbacks(context.Context, ports.CallbackQuery) (json.RawMessage, error) {
	return json.RawMessage(`{"items":[]}`), nil
}

func (w *fakeWallet) Callback(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (w *fakeWallet) Replay(_ context.Context, _, actor, _ string) (json.RawMessage, error) {
	w.reviewer = actor
	return json.RawMessage(`{}`), nil
}

func (w *fakeWallet) Review(_ context.Context, id string, approve bool, reviewer, reason string, soleMax decimal.Decimal, atLeast int,
) (json.RawMessage, error) {
	w.reviewer, w.soleMax, w.atLeast = reviewer, soleMax, atLeast
	w.reviewed = append(w.reviewed, id)
	d := w.detail(id)
	switch {
	case id == "busy" || d.Status != "PENDING_REVIEW":
		return nil, apperr.New(apperr.KindConflict, apperr.CodeConflict, "the withdrawal is APPROVED, not waiting for review")
	case approve && slices.Contains(d.Approvals, reviewer):
		return nil, apperr.New(apperr.KindConflict, apperr.CodeConflict, "each approval must come from another reviewer")
	case approve:
		d.Approvals, d.Status = append(d.Approvals, reviewer), "APPROVED"
	default:
		reject := "REVIEW: " + reason
		d.Status, d.RejectReason = "REJECTED", &reject
	}
	w.withdrawals[id] = d
	raw, _ := json.Marshal(d)
	return raw, nil
}

// detail is a withdrawal as the wallet keeps it: one of 100 USDT in review
// unless set.
func (w *fakeWallet) detail(id string) reviewedWithdrawal {
	if w.withdrawals == nil {
		w.withdrawals = map[string]reviewedWithdrawal{}
	}
	if d, ok := w.withdrawals[id]; ok {
		return d
	}
	return reviewedWithdrawal{Asset: "USDT", Amount: "100", Status: "PENDING_REVIEW", Approvals: []string{}}
}

func (w *fakeWallet) Detail(_ context.Context, id string) (json.RawMessage, error) {
	raw, _ := json.Marshal(w.detail(id))
	return json.RawMessage(`{"withdrawal":` + string(raw) + `}`), nil
}

func (w *fakeWallet) Suspensions(context.Context) ([]ports.Suspension, error) {
	out := []ports.Suspension{}
	for _, x := range w.suspended {
		out = append(out, x)
	}
	return out, nil
}

func (w *fakeWallet) Resume(_ context.Context, asset, actor, reason string) (ports.Suspension, error) {
	x, ok := w.suspended[asset]
	if !ok {
		return ports.Suspension{}, apperr.NotFound("withdrawals of " + asset + " are not suspended")
	}
	delete(w.suspended, asset)
	w.reviewed = append(w.reviewed, "resume "+asset+" "+actor+" "+reason)
	return x, nil
}

func (w *fakeWallet) Hold(_ context.Context, id string, hold bool, reviewer, _ string) (json.RawMessage, error) {
	w.reviewer = reviewer
	w.reviewed = append(w.reviewed, fmt.Sprintf("hold %s %t", id, hold))
	return json.RawMessage(`{}`), nil
}

// fakeDeposits is wallet-service's deposits that need a person.
type fakeDeposits struct {
	attention int
	booked    []ports.ManualDeposit
	decided   []string
	known     map[string]string // trade ID -> deposit ID
	credited  map[string]string // deposit ID -> who credited it
}

func (d *fakeDeposits) List(_ context.Context, q ports.DepositReviewQuery) (json.RawMessage, error) {
	n := 0
	if q.Attention {
		n = d.attention
	}
	items := make([]string, n)
	for i := range items {
		items[i] = "{}"
	}
	return json.RawMessage(`{"items":[` + strings.Join(items, ",") + `],"next_cursor":null}`), nil
}

func (d *fakeDeposits) Get(_ context.Context, id string) (json.RawMessage, error) {
	if by, ok := d.credited[id]; ok {
		return json.RawMessage(`{"id":"` + id + `","status":"CREDITED","resolution":"CREDITED","resolved_by":"` + by + `"}`), nil
	}
	return json.RawMessage(`{"id":"` + id + `"}`), nil
}

func (d *fakeDeposits) Credit(ctx context.Context, id, actor, _ string) (json.RawMessage, error) {
	if _, ok := d.credited[id]; ok {
		return nil, apperr.New(apperr.KindConflict, "WALLET_DEPOSIT_NOT_RELEASABLE", "credited already")
	}
	if d.credited == nil {
		d.credited = map[string]string{}
	}
	d.credited[id] = actor
	d.decided = append(d.decided, "credit "+id+" by "+actor)
	return d.Get(ctx, id)
}

func (d *fakeDeposits) Dismiss(_ context.Context, id, actor, _ string) (json.RawMessage, error) {
	d.decided = append(d.decided, "dismiss "+id+" by "+actor)
	return json.RawMessage(`{"id":"` + id + `","resolution":"DISMISSED"}`), nil
}

func (d *fakeDeposits) CheckManual(_ context.Context, m ports.ManualDeposit) (ports.ManualCheck, error) {
	if m.Address == "nobody" {
		return ports.ManualCheck{}, apperr.Invalid("no user's deposit address")
	}
	return ports.ManualCheck{UserID: someUser, Asset: "USDT"}, nil
}

func (d *fakeDeposits) BookManual(_ context.Context, m ports.ManualDeposit, _ string) (json.RawMessage, error) {
	if d.known == nil {
		d.known = map[string]string{}
	}
	id, ok := d.known[m.TradeID]
	if !ok {
		id = fmt.Sprintf("0192a000-0000-7000-8000-%012d", len(d.known)+1)
		d.known[m.TradeID] = id
		d.booked = append(d.booked, m)
	}
	return json.RawMessage(`{"id":"` + id + `","status":"CONFIRMED","source":"MANUAL"}`), nil
}

// fakePrices are the last prices of the USDT pairs.
type fakePrices ports.Prices

func (p fakePrices) Prices(context.Context, time.Duration) (ports.Prices, error) {
	return ports.Prices(p), nil
}

// countingPrices counts the reads of the prices: a review that values
// nothing reads none (C5.5 ⑮, ⑰).
type countingPrices struct {
	fakePrices
	reads int
}

func (p *countingPrices) Prices(ctx context.Context, maxAge time.Duration) (ports.Prices, error) {
	p.reads++
	return p.fakePrices.Prices(ctx, maxAge)
}

type harness struct {
	svc         *Service
	store       *memStore
	users       *fakeUsers
	ledger      *fakeLedger
	flags       *fakeFlags
	orders      *fakeOrders
	wallet      *fakeWallet
	derivatives *fakeDerivatives
	security    *fakeSecurity
	deposits    *fakeDeposits
	now         time.Time
	// secrets by email, for signing in.
	secrets map[string][]byte
}

var testCost = password.Cost{MemoryKiB: 64, Iterations: 1}

func newHarness(t *testing.T) *harness {
	t.Helper()
	box, err := secretbox.New("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{
		store: newMemStore(), ledger: &fakeLedger{}, flags: &fakeFlags{}, orders: &fakeOrders{}, wallet: &fakeWallet{},
		derivatives: &fakeDerivatives{}, now: time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC), secrets: map[string][]byte{},
		users: &fakeUsers{known: map[string]ports.User{}}, security: newFakeSecurity(), deposits: &fakeDeposits{},
	}
	h.svc = &Service{
		Store: h.store, Hasher: password.NewHasher(1, testCost), Box: box, Orders: h.orders, Wallet: h.wallet, Flags: h.flags,
		Ledger: h.ledger, Derivatives: h.derivatives, Users: h.users, Security: h.security, History: h.security, Risk: h.security,
		Deposits: h.deposits,
		Log:      slog.New(slog.DiscardHandler), Now: func() time.Time { return h.now },
	}
	return h
}

const testPassword = "correct horse battery"

func (h *harness) admin(t *testing.T, email, role string) {
	t.Helper()
	secret := totp.NewSecret()
	a, err := NewAdmin(context.Background(), h.store, h.svc.Hasher, h.svc.Box, email, "Test", role, testPassword, secret, "cli:test", false, h.now)
	if err != nil {
		t.Fatal(err)
	}
	h.secrets[a.Email] = secret
}

func (h *harness) code(email string) string { return totp.Code(h.secrets[email], totp.Step(h.now)) }

func (h *harness) login(t *testing.T, email string) Principal {
	t.Helper()
	token, _, _, err := h.svc.Login(context.Background(), email, testPassword, h.code(email), "192.0.2.1", "test")
	if err != nil {
		t.Fatalf("login %s: %v", email, err)
	}
	p, err := h.svc.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(totp.Period) // the next login needs a new code
	return p
}

func code(err error) string {
	var e *apperr.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestLoginNeedsPasswordAndFreshCode(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "ann@example.com", domain.RoleAdmin)
	if _, _, _, err := h.svc.Login(ctx, "ann@example.com", "wrong password!", h.code("ann@example.com"), "ip", "ua"); code(err) != "ADMIN_LOGIN_FAILED" {
		t.Fatalf("wrong password: %v", err)
	}
	if _, _, _, err := h.svc.Login(ctx, "ann@example.com", testPassword, "000000", "ip", "ua"); code(err) != "ADMIN_LOGIN_FAILED" {
		t.Fatalf("wrong code: %v", err)
	}
	if _, _, _, err := h.svc.Login(ctx, "nobody@example.com", testPassword, "000000", "ip", "ua"); code(err) != "ADMIN_LOGIN_FAILED" {
		t.Fatalf("unknown email: %v", err)
	}
	c := h.code("ann@example.com")
	token, sess, a, err := h.svc.Login(ctx, " Ann@Example.com ", testPassword, c, "ip", "ua")
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || a.FailedAttempts != 0 || !sess.ExpiresAt.Equal(h.now.Add(domain.SessionTTL)) {
		t.Fatalf("session %+v admin %+v", sess, a)
	}
	// The same code does not open a second session.
	if _, _, _, err := h.svc.Login(ctx, "ann@example.com", testPassword, c, "ip", "ua"); code(err) != "ADMIN_LOGIN_FAILED" {
		t.Fatalf("replayed code: %v", err)
	}
	want := []string{"admin.created", "admin.login_failed", "admin.login_failed", "admin.login", "admin.login_failed"}
	if got := h.store.actions(); !slices.Equal(got, want) {
		t.Fatalf("audit %v, want %v", got, want)
	}
}

// onFlags is a feature flag evaluator with some flags on.
type onFlags map[string]bool

func (f onFlags) Enabled(key string, _ flags.Subject) bool { return f[key] }

func TestLoginWithoutTheCodeWhenSwitchedOff(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "ann@example.com", domain.RoleAdmin)
	if !h.svc.TOTPRequired() {
		t.Fatal("the code is required unless the flag is on")
	}
	h.svc.Features = onFlags{}
	if _, _, _, err := h.svc.Login(ctx, "ann@example.com", testPassword, "", "ip", "ua"); code(err) != "ADMIN_LOGIN_FAILED" {
		t.Fatalf("no code with the flag off: %v", err)
	}

	h.svc.Features = onFlags{flags.KeyAdminNoTOTP: true}
	if h.svc.TOTPRequired() {
		t.Fatal("the flag switches the code off")
	}
	if _, _, _, err := h.svc.Login(ctx, "ann@example.com", "wrong password!", "", "ip", "ua"); code(err) != "ADMIN_LOGIN_FAILED" {
		t.Fatalf("the password is still checked: %v", err)
	}
	for _, c := range []string{"", "000000"} {
		if _, _, _, err := h.svc.Login(ctx, "ann@example.com", testPassword, c, "ip", "ua"); err != nil {
			t.Fatalf("password alone, code %q: %v", c, err)
		}
	}
	last := h.store.audits[len(h.store.audits)-1]
	if last.GetAction() != "admin.login" || !strings.Contains(last.GetDetails(), `"totp_checked":false`) {
		t.Fatalf("audit %v", last)
	}

	// Switched back on, the code is checked again and an unused one still works.
	h.svc.Features = onFlags{}
	h.login(t, "ann@example.com")
}

func TestLoginLocksAfterFiveFailures(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "ann@example.com", domain.RoleAdmin)
	for range domain.MaxFailures {
		if _, _, _, err := h.svc.Login(ctx, "ann@example.com", "wrong password!", "000000", "ip", "ua"); code(err) != "ADMIN_LOGIN_FAILED" {
			t.Fatal(err)
		}
	}
	if _, _, _, err := h.svc.Login(ctx, "ann@example.com", testPassword, h.code("ann@example.com"), "ip", "ua"); code(err) != "ADMIN_LOCKED" {
		t.Fatalf("locked: %v", err)
	}
	h.now = h.now.Add(domain.LockDuration + time.Second)
	h.login(t, "ann@example.com")
}

func TestSessions(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "ann@example.com", domain.RoleAdmin)
	token, _, _, err := h.svc.Login(ctx, "ann@example.com", testPassword, h.code("ann@example.com"), "ip", "ua")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Authenticate(ctx, "not a token"); code(err) != "ADMIN_UNAUTHORIZED" {
		t.Fatalf("bad token: %v", err)
	}
	// Requests keep a session alive; an hour without one ends it.
	for range 3 {
		h.now = h.now.Add(50 * time.Minute)
		if _, err := h.svc.Authenticate(ctx, token); err != nil {
			t.Fatal(err)
		}
	}
	h.now = h.now.Add(domain.SessionIdle + time.Minute)
	if _, err := h.svc.Authenticate(ctx, token); code(err) != "ADMIN_UNAUTHORIZED" {
		t.Fatalf("idle: %v", err)
	}
	p := h.login(t, "ann@example.com")
	if err := h.svc.Logout(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Authenticate(ctx, ""); code(err) != "ADMIN_UNAUTHORIZED" {
		t.Fatal(err)
	}
	q := h.login(t, "ann@example.com")
	if err := DisableAdmin(ctx, h.store, "ann@example.com", "cli:test", "left the team", h.now); err != nil {
		t.Fatal(err)
	}
	if !h.store.revoked[string(q.Session)] || len(h.store.revoked) != len(h.store.sessions) {
		t.Fatalf("revoked %d of %d sessions", len(h.store.revoked), len(h.store.sessions))
	}
	if _, _, _, err := h.svc.Login(ctx, "ann@example.com", testPassword, h.code("ann@example.com"), "ip", "ua"); code(err) != "ADMIN_LOGIN_FAILED" {
		t.Fatalf("disabled: %v", err)
	}
}

func TestRoles(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	h.admin(t, "ops@example.com", domain.RoleOperator)
	h.admin(t, "fin@example.com", domain.RoleFinance)
	auditor, ops, fin := h.login(t, "audit@example.com"), h.login(t, "ops@example.com"), h.login(t, "fin@example.com")
	if _, err := h.svc.SwitchFlag(ctx, auditor, "wallet.withdraw", false, "drill"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("auditor switches a flag: %v", err)
	}
	if _, err := h.svc.FlagList(ctx, auditor); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ReviewWithdrawal(ctx, ops, "", "w1", true, "looks fine"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("operator reviews a withdrawal: %v", err)
	}
	if _, err := h.svc.ReviewWithdrawal(ctx, fin, "", "w1", true, "looks fine"); err != nil || h.wallet.reviewer != "fin@example.com" {
		t.Fatalf("finance reviews: %v, reviewer %q", err, h.wallet.reviewer)
	}
	if _, err := h.svc.SwitchFlag(ctx, ops, "wallet.withdraw", false, "no"); err == nil {
		t.Fatal("a two-character reason passed")
	}
	if _, err := h.svc.SwitchFlag(ctx, ops, "wallet.withdraw", false, "incident 42"); err != nil || len(h.flags.switched) != 1 {
		t.Fatalf("operator switches a flag: %v", err)
	}
	if err := h.svc.CancelOrders(ctx, fin, "01929c3e-7f3a-7d7e-8a1b-2c3d4e5f6a7b", "fraud"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("finance cancels orders: %v", err)
	}
	if err := h.svc.CancelOrders(ctx, ops, "01929c3e-7f3a-7d7e-8a1b-2c3d4e5f6a7b", "fraud"); err != nil || len(h.orders.canceled) != 1 {
		t.Fatalf("operator cancels orders: %v", err)
	}
	if got := h.store.audits[len(h.store.audits)-1]; got.GetAction() != "admin.orders.cancel_all" || got.GetActor() != "ops@example.com" {
		t.Fatalf("audit %v", got)
	}
	if _, err := h.svc.RequestAdjustment(ctx, ops, Adjustment{}); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("operator requests an adjustment: %v", err)
	}
}

func TestAdjustmentsNeedASecondAdministrator(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "fin@example.com", domain.RoleFinance)
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	fin, boss := h.login(t, "fin@example.com"), h.login(t, "boss@example.com")
	user := "01929c3e-7f3a-7d7e-8a1b-2c3d4e5f6a7b"
	a, err := h.svc.RequestAdjustment(ctx, fin, Adjustment{UserID: user, Asset: "usdt", Amount: decimal.RequireFromString("12.5"), Reason: "refund"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != domain.ApprovalPending || a.Payload["asset"] != "USDT" {
		t.Fatalf("request %+v", a)
	}
	if _, err := h.svc.DecideApproval(ctx, fin, a.ID, true, "my own"); code(err) != "ADMIN_SELF_APPROVAL" {
		t.Fatalf("self approval: %v", err)
	}
	// The ledger does not answer: the request stays pending.
	h.ledger.err = apperr.New(apperr.KindUnavailable, "UNAVAILABLE", "down")
	if _, err := h.svc.DecideApproval(ctx, boss, a.ID, true, "checked"); err == nil {
		t.Fatal("approved while the ledger was down")
	}
	if got := h.store.approvals[a.ID]; got.Status != domain.ApprovalPending {
		t.Fatalf("after an unknown outcome: %+v", got)
	}
	h.ledger.err = nil
	done, err := h.svc.DecideApproval(ctx, boss, a.ID, true, "checked")
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != domain.ApprovalExecuted || done.DecidedBy != boss.Admin.ID || done.Result != "journal journal-1" {
		t.Fatalf("decided %+v", done)
	}
	for _, c := range h.ledger.calls {
		if c.key != "approval:"+a.ID || c.userID != user || c.asset != "USDT" || !c.amount.Equal(decimal.RequireFromString("12.5")) ||
			c.actor != "boss@example.com" {
			t.Fatalf("ledger call %+v", c)
		}
	}
	if _, err := h.svc.DecideApproval(ctx, boss, a.ID, false, "again"); code(err) != "ADMIN_APPROVAL_DECIDED" {
		t.Fatalf("second decision: %v", err)
	}
	// A refusal by the ledger fails the request for good.
	b, err := h.svc.RequestAdjustment(ctx, boss, Adjustment{UserID: user, Asset: "USDT", Amount: decimal.RequireFromString("-1"), Reason: "fee"})
	if err != nil {
		t.Fatal(err)
	}
	h.ledger.err = apperr.New(apperr.KindUnprocessable, "LEDGER_ADJUSTMENT_DISABLED", "off")
	failed, err := h.svc.DecideApproval(ctx, fin, b.ID, true, "checked")
	if err != nil || failed.Status != domain.ApprovalFailed || !bytes.Contains([]byte(failed.Result), []byte("LEDGER_ADJUSTMENT_DISABLED")) {
		t.Fatalf("refused: %+v, %v", failed, err)
	}
	c, err := h.svc.RequestAdjustment(ctx, fin, Adjustment{UserID: user, Asset: "USDT", Amount: decimal.RequireFromString("3"), Reason: "typo"})
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := h.svc.DecideApproval(ctx, boss, c.ID, false, "wrong amount")
	if err != nil || rejected.Status != domain.ApprovalRejected || len(h.ledger.calls) != 3 {
		t.Fatalf("rejected: %+v, %v (%d ledger calls)", rejected, err, len(h.ledger.calls))
	}
}

func TestInsuranceFundContributionsNeedASecondAdministrator(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "fin@example.com", domain.RoleFinance)
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "ops@example.com", domain.RoleOperator)
	fin, boss, ops := h.login(t, "fin@example.com"), h.login(t, "boss@example.com"), h.login(t, "ops@example.com")
	fund, err := h.svc.InsuranceFund(ctx, ops, "")
	if err != nil || fund.Asset != "USDT" || fund.Balance != "1000000" || fund.PnLClearing != "-12.5" {
		t.Fatalf("fund %+v %v", fund, err)
	}
	if _, err := h.svc.RequestInsuranceFunding(ctx, ops, "USDT", decimal.NewFromInt(100), "top up"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an operator requested: %v", err)
	}
	if _, err := h.svc.RequestInsuranceFunding(ctx, fin, "USDT", decimal.NewFromInt(-5), "take out"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("a negative contribution: %v", err)
	}
	a, err := h.svc.RequestInsuranceFunding(ctx, fin, "usdt", decimal.NewFromInt(50000), "after the drill")
	if err != nil || a.Kind != domain.KindInsuranceFund || a.Payload["asset"] != "USDT" || a.Payload["amount"] != "50000" {
		t.Fatalf("request %+v %v", a, err)
	}
	if _, err := h.svc.DecideApproval(ctx, fin, a.ID, true, "my own"); code(err) != "ADMIN_SELF_APPROVAL" {
		t.Fatalf("self approval: %v", err)
	}
	done, err := h.svc.DecideApproval(ctx, boss, a.ID, true, "checked")
	if err != nil || done.Status != domain.ApprovalExecuted || done.Result != "journal journal-2" {
		t.Fatalf("decided %+v %v", done, err)
	}
	if len(h.ledger.calls) != 1 || h.ledger.calls[0].key != "approval:"+a.ID || h.ledger.calls[0].asset != "USDT" ||
		!h.ledger.calls[0].amount.Equal(decimal.NewFromInt(50000)) || h.ledger.calls[0].actor != "boss@example.com" {
		t.Fatalf("ledger calls %+v", h.ledger.calls)
	}
	if a := h.actions(); !slices.Contains(a, "admin.derivatives.insurance_requested") || !slices.Contains(a, "admin.derivatives.insurance_approved") {
		t.Fatalf("audit %v", a)
	}
}

// actions lists the audited actions in order.
func (h *harness) actions() []string {
	var out []string
	for _, e := range h.store.audits {
		out = append(out, e.GetAction())
	}
	return out
}

func TestLiftingReduceOnly(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "ops@example.com", domain.RoleOperator)
	h.admin(t, "fin@example.com", domain.RoleFinance)
	ops, fin := h.login(t, "ops@example.com"), h.login(t, "fin@example.com")
	if _, err := h.svc.LiftReduceOnly(ctx, fin, "BTC-USDT-PERP", "index is back"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("finance lifted: %v", err)
	}
	if _, err := h.svc.LiftReduceOnly(ctx, ops, "BTC-USDT-PERP", ""); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("no reason: %v", err)
	}
	raw, err := h.svc.LiftReduceOnly(ctx, ops, "btc-usdt-perp", "index is back")
	if err != nil || !bytes.Contains(raw, []byte(`"lifted":true`)) || len(h.derivatives.lifted) != 1 ||
		h.derivatives.lifted[0] != "btc-usdt-perp by ops@example.com" {
		t.Fatalf("lift %s %v %v", raw, err, h.derivatives.lifted)
	}
	if !slices.Contains(h.actions(), "admin.derivatives.reduce_only_lifted") {
		t.Fatalf("audit %v", h.actions())
	}
	if _, _, err := h.svc.Liquidations(ctx, fin, ports.LiquidationQuery{Days: 7, Kind: "sideways", Limit: 10}); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("unknown kind: %v", err)
	}
	if _, _, err := h.svc.Liquidations(ctx, fin, ports.LiquidationQuery{UserID: "bob"}); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("a user that is no UUID: %v", err)
	}
}

func TestEveryUsersPositions(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.svc.HouseBook.User = "0192a000-0000-7000-8000-0000000000ff"
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	auditor := h.login(t, "audit@example.com")
	if _, err := h.svc.OpenPositions(ctx, auditor, ports.PositionQuery{UserID: "bob"}); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("a user that is no UUID: %v", err)
	}
	page, err := h.svc.OpenPositions(ctx, auditor, ports.PositionQuery{Symbol: " btc-usdt-perp ", Watch: true, Limit: 10_000})
	if err != nil || page.Truncated || page.HouseUserID == nil || *page.HouseUserID != h.svc.HouseBook.User ||
		!bytes.Contains(page.Positions, []byte(`"BTC-USDT-PERP"`)) {
		t.Fatalf("positions %+v %v", page, err)
	}
	if q := h.derivatives.queries[0]; q.Symbol != "BTC-USDT-PERP" || !q.Watch || q.Limit != positionsLimit {
		t.Fatalf("asked %+v", q)
	}
}
