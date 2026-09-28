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

func TestWalletPostings(t *testing.T) {
	d := decimal.RequireFromString
	p, err := WithdrawSettlePosting("k1", "u1", "ETH", d("0.01"), d("0.001"), 18, "w1")
	if err != nil || p.Validate() != nil || len(p.Lines) != 3 || !p.Lines[0].Amount.Equal(d("-0.011")) || p.Lines[0].Kind != Frozen {
		t.Fatalf("settle %+v %v", p, err)
	}
	if p, _ := WithdrawSettlePosting("k2", "u1", "ETH", d("0.01"), decimal.Zero, 18, "w2"); len(p.Lines) != 2 || p.Validate() != nil {
		t.Fatalf("no fee, no fee line: %+v", p)
	}
	if _, err := WithdrawSettlePosting("k3", "u1", "USDT", d("1"), d("0.0000001"), 6, "w3"); err == nil {
		t.Fatal("a fee beyond the asset's decimals")
	}
	p, err = InternalTransferPosting("k4", "u1", "u2", "ETH", d("0.5"), 18, "w4")
	if err != nil || p.Validate() != nil || p.EntryType != EntryInternalTransfer || p.Lines[1].Account != UserAccount("u2", AccountSpot, "ETH") {
		t.Fatalf("internal %+v %v", p, err)
	}
	if _, err := InternalTransferPosting("k5", "u1", "u1", "ETH", d("1"), 18, "w5"); err == nil {
		t.Fatal("an internal transfer to oneself")
	}
	p, err = ChainFeePosting("k6", "ETH", d("0.000021"), 18, "0xab")
	if err != nil || p.Validate() != nil || p.Lines[0].Account.Type != AccountGasSupply || p.Lines[1].Account.Type != AccountWithdrawalPending {
		t.Fatalf("chain fee %+v %v", p, err)
	}
	p, err = FundingPosting("k7", AccountGasSupply, "ETH", d("0.01"), 18, "0xcd")
	if err != nil || p.Validate() != nil || p.EntryType != EntryDepositCredit || p.Lines[0].Account.Type != AccountDepositPending {
		t.Fatalf("funding %+v %v", p, err)
	}
	if _, err := FundingPosting("k8", AccountFeeRevenue, "ETH", d("1"), 18, "0xef"); err == nil {
		t.Fatal("FEE_REVENUE is not funded from outside")
	}
}
