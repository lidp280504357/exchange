package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestRoles(t *testing.T) {
	for _, c := range []struct {
		role, perm string
		want       bool
	}{
		{RoleAdmin, PermAdjustApprove, true},
		{RoleFinance, PermWithdrawalsEdit, true},
		{RoleFinance, PermFlagsEdit, false},
		{RoleOperator, PermFlagsEdit, true},
		{RoleOperator, PermWithdrawalsEdit, false},
		{RoleAuditor, PermAuditRead, true},
		{RoleAuditor, PermUsersStatus, false},
		{RoleAdmin, PermSettingsEdit, true},
		{RoleFinance, PermSettingsEdit, false},
		{RoleOperator, PermSettingsEdit, false},
		{"ROOT", PermUsersRead, false},
	} {
		if Allows(c.role, c.perm) != c.want {
			t.Errorf("%s %s: want %v", c.role, c.perm, c.want)
		}
	}
}

func TestAdminLifecycle(t *testing.T) {
	now := time.Now()
	if _, err := NewAdmin("a1", "not an email", "Ann", RoleAdmin, now); err == nil {
		t.Fatal("a bad email")
	}
	if _, err := NewAdmin("a1", "ann@example.com", "Ann", "ROOT", now); err == nil {
		t.Fatal("an unknown role")
	}
	a, err := NewAdmin("a1", " Ann@Example.com ", "Ann", RoleFinance, now)
	if err != nil || a.Email != "ann@example.com" || a.Status != StatusActive {
		t.Fatalf("admin %+v %v", a, err)
	}
	for range MaxFailures - 1 {
		a.Failed(now)
	}
	if a.Locked(now) {
		t.Fatal("not yet locked")
	}
	a.Failed(now)
	if !a.Locked(now) || a.Locked(now.Add(LockDuration)) {
		t.Fatalf("locked for 15 minutes: %+v", a)
	}
	a.Succeeded(42, now)
	if a.Locked(now) || a.TOTPLastStep != 42 || a.FailedAttempts != 0 {
		t.Fatalf("after a login %+v", a)
	}
}

func TestSessionsAndApprovals(t *testing.T) {
	now := time.Now()
	token, hash := NewToken()
	if string(HashToken(token)) != string(hash) || len(token) < 40 {
		t.Fatal("token hashing")
	}
	s := Session{CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(SessionTTL)}
	if !s.Live(now.Add(time.Minute)) || s.Live(now.Add(SessionIdle)) || s.Live(now.Add(SessionTTL)) {
		t.Fatal("session lifetime")
	}
	ap := Approval{Status: ApprovalPending, RequestedBy: "a1", Mode: ModeTwoPerson}
	if err := ap.Decide("a1", true); !errors.Is(err, ErrSelfApproval) {
		t.Fatal("no self-approval")
	}
	if err := ap.Decide("a1", false); err != nil {
		t.Fatalf("withdrawing one's own request: %v", err)
	}
	if err := ap.Decide("a2", true); err != nil {
		t.Fatal(err)
	}
	// A single-person operation whose outcome was unknown is finished by its requester.
	single := Approval{Status: ApprovalPending, RequestedBy: "a1", Mode: ModeSingle}
	if err := single.Decide("a1", true); err != nil {
		t.Fatal(err)
	}
	ap.Status = ApprovalExecuted
	if err := ap.Decide("a2", true); !errors.Is(err, ErrNotPending) {
		t.Fatal("decided once")
	}
}

func TestSettings(t *testing.T) {
	s := DefaultSettings()
	if err := s.Validate(); err != nil || s.SingleMax.String() != "100000" || s.DailyMax.String() != "500000" {
		t.Fatalf("defaults %+v %v", s, err)
	}
	s.DailyMax = s.SingleMax.Sub(decimal.NewFromInt(1))
	if s.Validate() == nil {
		t.Fatal("a day's limit below one operation's")
	}
	s = DefaultSettings()
	s.WithdrawalMax = decimal.Zero
	if s.Validate() == nil {
		t.Fatal("a zero limit")
	}
}
