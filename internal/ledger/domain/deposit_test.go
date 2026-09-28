package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestDepositPosting(t *testing.T) {
	d := Deposit{ID: "d1", UserID: "u1", Asset: "ETH", Amount: decimal.RequireFromString("0.002"), Network: "ETH-SEPOLIA", TxHash: "0xab"}
	p, err := DepositPosting(d)
	if err != nil || p.Validate() != nil || p.IdemKey != "deposit:d1" || p.EntryType != EntryDepositCredit {
		t.Fatalf("posting %+v %v", p, err)
	}
	if p.Lines[0].Account != SystemAccount(AccountDepositPending, "ETH") || !p.Lines[0].Amount.Equal(decimal.RequireFromString("-0.002")) {
		t.Fatalf("DEPOSIT_PENDING pays: %+v", p.Lines[0])
	}
	if p.Lines[1].Account != UserAccount("u1", AccountSpot, "ETH") {
		t.Fatalf("the user's SPOT account receives: %+v", p.Lines[1])
	}

	d.Unclaimed, d.Reason = true, "BELOW_MINIMUM"
	p, err = DepositPosting(d)
	if err != nil || p.Lines[1].Account != SystemAccount(AccountUnclaimedDeposit, "ETH") || p.Validate() != nil {
		t.Fatalf("an unclaimed deposit goes to UNCLAIMED_DEPOSIT: %+v %v", p, err)
	}
	for _, bad := range []Deposit{{UserID: "u", Asset: "ETH", Amount: decimal.NewFromInt(1)}, {ID: "x", UserID: "u", Asset: "ETH"}} {
		if _, err := DepositPosting(bad); err == nil {
			t.Fatalf("refused: %+v", bad)
		}
	}
}
