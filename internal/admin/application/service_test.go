package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	audits    []*auditv1.AdminActionPerformed
}

func newMemStore() *memStore {
	return &memStore{
		admins: map[string]domain.Admin{}, sessions: map[string]domain.Session{}, revoked: map[string]bool{},
		approvals: map[string]domain.Approval{},
	}
}

func (m *memStore) Tx(_ context.Context, fn func(ports.Repos) error) error { return fn(m) }

func (m *memStore) Read() ports.Repos { return m }

func (m *memStore) Admins() ports.AdminRepo { return memAdmins{m} }

func (m *memStore) Sessions() ports.SessionRepo { return memSessions{m} }

func (m *memStore) Approvals() ports.ApprovalRepo { return memApprovals{m} }

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
	key, userID, asset, actor string
	amount                    decimal.Decimal
}

type fakeLedger struct {
	calls []adjustment
	err   error
}

func (l *fakeLedger) Adjust(_ context.Context, key, userID, asset string, amount decimal.Decimal, actor, _ string) (string, error) {
	l.calls = append(l.calls, adjustment{key: key, userID: userID, asset: asset, actor: actor, amount: amount})
	if l.err != nil {
		return "", l.err
	}
	return "journal-1", nil
}

func (l *fakeLedger) FundInsurance(_ context.Context, key, asset string, amount decimal.Decimal, actor, _ string) (string, error) {
	l.calls = append(l.calls, adjustment{key: key, asset: asset, actor: actor, amount: amount})
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

type fakeDerivatives struct{ lifted []string }

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

type fakeWallet struct{ reviewer string }

func (w *fakeWallet) List(context.Context, ports.WithdrawalQuery) (json.RawMessage, error) {
	return json.RawMessage(`{"items":[]}`), nil
}

func (w *fakeWallet) Review(_ context.Context, _ string, _ bool, reviewer, _ string) (json.RawMessage, error) {
	w.reviewer = reviewer
	return json.RawMessage(`{}`), nil
}

type harness struct {
	svc         *Service
	store       *memStore
	ledger      *fakeLedger
	flags       *fakeFlags
	orders      *fakeOrders
	wallet      *fakeWallet
	derivatives *fakeDerivatives
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
	}
	h.svc = &Service{
		Store: h.store, Hasher: password.NewHasher(1, testCost), Box: box, Orders: h.orders, Wallet: h.wallet, Flags: h.flags,
		Ledger: h.ledger, Derivatives: h.derivatives, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return h.now },
	}
	return h
}

const testPassword = "correct horse battery"

func (h *harness) admin(t *testing.T, email, role string) {
	t.Helper()
	secret := totp.NewSecret()
	a, err := NewAdmin(context.Background(), h.store, h.svc.Hasher, h.svc.Box, email, "Test", role, testPassword, secret, "cli:test", h.now)
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
	if _, err := h.svc.ReviewWithdrawal(ctx, ops, "w1", true, "looks fine"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("operator reviews a withdrawal: %v", err)
	}
	if _, err := h.svc.ReviewWithdrawal(ctx, fin, "w1", true, "looks fine"); err != nil || h.wallet.reviewer != "fin@example.com" {
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
	if _, _, err := h.svc.Liquidations(ctx, fin, 7, "sideways", "", 10); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("unknown kind: %v", err)
	}
}
