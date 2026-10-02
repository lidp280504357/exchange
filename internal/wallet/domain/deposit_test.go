package domain

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestDepositLifecycle(t *testing.T) {
	now := time.Now()
	d := &Deposit{ID: "d1", BlockNumber: 100, Required: 12, Status: StatusDetected}
	if d.Observe(99, now) || d.Status != StatusDetected {
		t.Fatal("a head behind the block changes nothing")
	}
	if d.Observe(105, now) || d.Status != StatusConfirming || d.Confirmations != 6 {
		t.Fatalf("6 confirmations: %+v", d)
	}
	if d.RequestCredit("", now) {
		t.Fatal("only a confirmed deposit is sent to the ledger")
	}
	if !d.Observe(111, now) || d.Status != StatusConfirmed || d.Confirmations != 12 {
		t.Fatalf("12 confirmations: %+v", d)
	}
	if d.Observe(200, now) {
		t.Fatal("confirmed once")
	}
	if err := d.Orphan(); err == nil {
		t.Fatal("a confirmed deposit is past the reorganization depth")
	}
	if !d.RequestCredit("", now) || d.RequestCredit("", now) || d.Unclaimed {
		t.Fatalf("credit requested once: %+v", d)
	}
	if !d.Credit("j1", now) || d.Status != StatusCredited || d.Credit("j2", now) {
		t.Fatalf("credit: %+v", d)
	}

	u := &Deposit{ID: "d2", BlockNumber: 1, Required: 1, Status: StatusDetected}
	u.Observe(1, now)
	if !u.RequestCredit(ReasonAccountClosed, now) || !u.Unclaimed || u.Reason != ReasonAccountClosed {
		t.Fatalf("unclaimed on request: %+v", u)
	}
	if !u.Credit("j3", now) || u.Status != StatusRejected {
		t.Fatalf("an unclaimed deposit ends REJECTED: %+v", u)
	}
}

func TestDepositDecisions(t *testing.T) {
	now := time.Now()
	unclaimed := func() *Deposit {
		return &Deposit{ID: "d1", Status: StatusRejected, Unclaimed: true, Reason: ReasonBelowMinimum, JournalID: "j1", Asset: "USDT"}
	}
	d := unclaimed()
	if !d.Attention() {
		t.Fatal("an unclaimed deposit waits for a decision")
	}
	if err := d.Release("j2", "ops", "minimum waived", now); err != nil || d.Status != StatusCredited || d.Attention() {
		t.Fatalf("released: %+v %v", d, err)
	}
	if err := d.Release("j3", "ops", "again", now); err == nil {
		t.Fatal("released once")
	}
	if err := d.Dismiss("ops", "again", now); err == nil {
		t.Fatal("a decided deposit needs no other")
	}

	odd := unclaimed()
	odd.Source = SourceManual
	if odd.MatchCallback(odd.ProviderTxID, "addr", "USDT", decimal.NewFromInt(1), now) || !odd.Attention() {
		t.Fatalf("a disagreeing callback needs a person: %+v", odd)
	}
	if err := odd.Release("j2", "ops", "credit it", now); err == nil {
		t.Fatal("a backfill in doubt is not released")
	}
	if err := odd.Dismiss("ops", "entered wrongly", now); err != nil || odd.Resolution != ResolutionDismissed || odd.Attention() {
		t.Fatalf("dismissed: %+v %v", odd, err)
	}

	token := &Deposit{ID: "d3", Status: StatusRejected, Reason: ReasonUnsupportedToken}
	if !token.Attention() || token.Release("j1", "ops", "credit it", now) == nil {
		t.Fatal("an unsupported token waits, but only to be dismissed")
	}
}

func TestReorganization(t *testing.T) {
	now := time.Now()
	o := &Deposit{ID: "d3", BlockNumber: 5, BlockHash: "0xa", Required: 12, Status: StatusConfirming, Confirmations: 3}
	if err := o.Orphan(); err != nil || o.Status != StatusOrphaned || o.Confirmations != 0 || o.Observe(100, now) {
		t.Fatalf("orphan: %+v %v", o, err)
	}
	if !o.Seen(6, "0xb") || o.Status != StatusDetected || o.BlockNumber != 6 {
		t.Fatalf("an orphan seen again is detected anew: %+v", o)
	}
	if o.Seen(6, "0xb") {
		t.Fatal("the same block again changes nothing")
	}
	c := &Deposit{ID: "d4", BlockNumber: 7, BlockHash: "0xc", Status: StatusCredited}
	if !c.Seen(8, "0xd") || c.Status != StatusCredited || c.BlockNumber != 8 {
		t.Fatalf("a credited deposit follows its block: %+v", c)
	}
}
