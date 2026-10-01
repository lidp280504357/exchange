package domain

import (
	"slices"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestAssess(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	old := now.Add(-30 * 24 * time.Hour)
	d := decimal.RequireFromString
	calm := RiskInput{
		Now: now, AccountCreated: old, DeviceFirstSeen: old, IdentityChanged: old, PasswordChanged: old, AddressAdded: old,
		ValueUSDT: d("100"), DailyUSDT: d("100"), DailyLimit: d("2000"),
	}
	if r := Assess(calm); r.Approvals != 0 || len(r.Reasons) != 0 || r.Score != 0 {
		t.Fatalf("an old account on a known device: %+v", r)
	}
	for reason, change := range map[string]func(*RiskInput){
		RiskNewAccount:     func(in *RiskInput) { in.AccountCreated = now.Add(-71 * time.Hour) },
		RiskNewDevice:      func(in *RiskInput) { in.DeviceFirstSeen = now.Add(-23 * time.Hour) },
		RiskSecurityChange: func(in *RiskInput) { in.PasswordChanged = now.Add(-time.Hour) },
		RiskNewAddress:     func(in *RiskInput) { in.AddressAdded = now.Add(-48 * time.Hour) },
		RiskLargeAmount:    func(in *RiskInput) { in.ValueUSDT, in.DailyUSDT = d("1000.01"), d("900") },
		RiskDailyShare:     func(in *RiskInput) { in.DailyUSDT = d("1000.01") },
	} {
		in := calm
		change(&in)
		r := Assess(in)
		if !slices.Equal(r.Reasons, []string{reason}) || r.Approvals != 1 || r.Score == 0 {
			t.Errorf("%s: %+v", reason, r)
		}
	}
	unknown := calm
	unknown.DeviceFirstSeen = time.Time{}
	if r := Assess(unknown); !slices.Contains(r.Reasons, RiskNewDevice) {
		t.Fatalf("an unknown device counts as new: %+v", r)
	}
	huge := calm
	huge.ValueUSDT, huge.DailyUSDT, huge.DailyLimit = d("25000"), d("25000"), d("100000")
	if r := Assess(huge); r.Approvals != 2 {
		t.Fatalf("above 20,000 two reviewers approve: %+v", r)
	}
	fresh := calm
	fresh.AccountCreated, fresh.DeviceFirstSeen, fresh.IdentityChanged, fresh.AddressAdded = now, now, now, now
	fresh.ValueUSDT, fresh.DailyUSDT = d("1500"), d("1500")
	if r := Assess(fresh); r.Score != 100 {
		t.Fatalf("the score stops at 100: %+v", r)
	}
}

func TestLimits(t *testing.T) {
	if l := LimitsFor(2, true); l.Daily.String() != "2000" || l.Monthly.String() != "20000" {
		t.Fatalf("full limits %+v", l)
	}
	for _, l := range []Limits{LimitsFor(1, true), LimitsFor(2, false)} {
		if l.Daily.String() != "400" || l.Monthly.String() != "4000" {
			t.Fatalf("20%% limits %+v", l)
		}
	}
}

func TestWithdrawalLifecycle(t *testing.T) {
	now := time.Now()
	w := &Withdrawal{ID: "w1", Status: WithdrawalRequested, Required: 12, Nonce: -1, FreezeJournal: "j1"}
	w.Scored(RiskResult{Score: 60, Reasons: []string{RiskNewAccount}, Approvals: 2}, now)
	if w.Status != WithdrawalReview {
		t.Fatalf("reviewed: %+v", w)
	}
	if done, err := w.Approve("alice", now); err != nil || done {
		t.Fatalf("one of two approvals: %v %v", done, err)
	}
	if _, err := w.Approve("alice", now); err == nil {
		t.Fatal("the same reviewer twice")
	}
	if done, err := w.Approve("bob", now); err != nil || !done || w.Status != WithdrawalApproved {
		t.Fatalf("two approvals: %v %v %+v", done, err, w)
	}
	w.Broadcasted(7, "0xa", now)
	if w.Cancelable() || w.Cancel(now) == nil || w.Reject("x", now) == nil {
		t.Fatal("a broadcast withdrawal cannot be withdrawn")
	}
	if w.Mined("0xb", 100, 105, now) || w.Status != WithdrawalConfirming || w.Confirmations != 6 || w.TxHash != "0xb" {
		t.Fatalf("mined with 6 confirmations: %+v", w)
	}
	if !w.Mined("0xb", 100, 111, now) || w.Status != WithdrawalConfirmed {
		t.Fatalf("confirmed: %+v", w)
	}

	c := &Withdrawal{ID: "w2", Status: WithdrawalRequested, FreezeJournal: "j2"}
	c.Scored(RiskResult{}, now)
	if c.Status != WithdrawalApproved || c.ApprovedAt.IsZero() {
		t.Fatalf("no reasons, approved at once: %+v", c)
	}
	if err := c.Cancel(now); err != nil || !c.NeedsRelease() {
		t.Fatalf("canceled with funds to release: %+v %v", c, err)
	}
	c.UnfreezeJournal = "j3"
	if c.NeedsRelease() {
		t.Fatal("released once")
	}
}

func TestCustodianReports(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	approved := Withdrawal{ID: "w1", Status: WithdrawalApproved, Provider: ProviderUdun, Required: 20, FreezeJournal: "j1"}
	own := approved
	own.Provider = ""
	if own.Submit(now) {
		t.Fatal("a withdrawal of the platform's wallets went to a custodian")
	}
	w := approved
	if !w.Submit(now) || w.Status != WithdrawalSubmitted || w.ProviderStatus != CustodySubmitted || !w.SubmittedAt.Equal(now) {
		t.Fatalf("Submit: %+v", w)
	}
	if w.Cancelable() {
		t.Fatal("a withdrawal with the custodian can be canceled")
	}
	if w.Submit(now) {
		t.Fatal("submitted twice")
	}
	if !w.Custodian(CustodyAccepted, "", now) || w.ProviderStatus != CustodyAccepted {
		t.Fatalf("accepted: %+v", w)
	}
	if !w.Custodian(CustodyApproved, "", now) || w.Custodian(CustodyApproved, "", now) || w.Custodian(CustodyAccepted, "", now) {
		t.Fatalf("the review changes once, and a late acknowledgment not at all: %+v", w)
	}
	sent := w
	if !sent.Custodian(CustodySuccess, "0xabc", now) || sent.Status != WithdrawalConfirmed || sent.TxHash != "0xabc" || sent.Confirmations != 20 {
		t.Fatalf("success: %+v", sent)
	}
	if sent.Custodian(CustodyFailed, "", now) || sent.Status != WithdrawalConfirmed {
		t.Fatal("a failure after the success changed it")
	}
	if sent.NeedsRelease() {
		t.Fatal("a sent withdrawal releases its funds")
	}
	for word, reason := range map[string]string{CustodyRejected: "CUSTODY_REJECTED", CustodyFailed: "CUSTODY_FAILED: 0xdead"} {
		failed := w
		tx := ""
		if word == CustodyFailed {
			tx = "0xdead"
		}
		if !failed.Custodian(word, tx, now) || failed.Status != WithdrawalFailed || failed.RejectReason != reason || failed.TxHash != "" {
			t.Fatalf("%s: %+v", word, failed)
		}
		if !failed.NeedsRelease() {
			t.Fatalf("%s: the frozen funds stay frozen", word)
		}
	}
}
