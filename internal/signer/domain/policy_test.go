package domain

import (
	"errors"
	"math/big"
	"testing"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

const (
	hot   = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
	payee = "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359"
)

func gwei(n int64) *big.Int { return new(big.Int).Mul(big.NewInt(n), big.NewInt(1_000_000_000)) }
func eth(milli int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(milli), big.NewInt(1_000_000_000_000_000))
}

var policy = Policy{ChainID: 11155111, MaxValue: eth(100), DailyValue: eth(150), MaxFee: gwei(200), MaxGas: 100_000}

func withdrawal() Request {
	return Request{
		ID: "r1", Purpose: PurposeWithdrawal, Reference: "w1", ApprovedBy: "risk", ChainID: 11155111, Nonce: 3, To: payee,
		Value: eth(10), GasLimit: 21000, MaxFee: gwei(30), MaxTip: gwei(2),
	}
}

func refused(t *testing.T, err error, why string) {
	t.Helper()
	var e *apperr.Error
	if !errors.As(err, &e) || e.Code != "SIGNER_REFUSED" {
		t.Errorf("%s: got %v", why, err)
	}
}

func TestPolicy(t *testing.T) {
	if err := policy.Check(withdrawal(), hot); err != nil {
		t.Fatal(err)
	}
	for why, change := range map[string]func(*Request){
		"another chain":           func(r *Request) { r.ChainID = 1 },
		"no approval":             func(r *Request) { r.ApprovedBy = "" },
		"above the per-tx limit":  func(r *Request) { r.Value = eth(101) },
		"zero value":              func(r *Request) { r.Value = big.NewInt(0) },
		"fee above the cap":       func(r *Request) { r.MaxFee = gwei(201) },
		"tip above the fee":       func(r *Request) { r.MaxTip = gwei(31) },
		"gas limit above the cap": func(r *Request) { r.GasLimit = 100_001 },
		"calldata":                func(r *Request) { r.Data = []byte{1} },
		"paying the hot wallet":   func(r *Request) { r.To = hot },
		"a bad address":           func(r *Request) { r.To = "0x1234" },
		"an unknown purpose":      func(r *Request) { r.Purpose = "GIFT" },
	} {
		r := withdrawal()
		change(&r)
		refused(t, policy.Check(r, hot), why)
	}
	sweep := Request{
		ID: "s1", Purpose: PurposeSweep, Reference: "sw1", ChainID: 11155111, Index: 4, To: hot, Value: eth(1),
		GasLimit: 21000, MaxFee: gwei(30), MaxTip: gwei(1),
	}
	if err := policy.Check(sweep, hot); err != nil {
		t.Fatalf("a sweep to the hot wallet: %v", err)
	}
	sweep.To = payee
	refused(t, policy.Check(sweep, hot), "a sweep elsewhere")
}

func TestPolicyHistory(t *testing.T) {
	first := withdrawal()
	if err := policy.CheckHistory(first, nil, eth(100)); err != nil {
		t.Fatalf("within the daily limit: %v", err)
	}
	refused(t, policy.CheckHistory(first, nil, eth(141)), "over the daily limit")

	signed := []Signature{{Request: first}}
	bump := first
	bump.ID, bump.MaxFee, bump.MaxTip = "r2", gwei(40), gwei(3)
	if err := policy.CheckHistory(bump, signed, eth(149)); err != nil {
		t.Fatalf("a replacement does not count against the limit again: %v", err)
	}
	same := bump
	same.MaxFee = first.MaxFee
	refused(t, policy.CheckHistory(same, signed, eth(0)), "a replacement that does not raise the fee")
	for why, change := range map[string]func(*Request){
		"another nonce":     func(r *Request) { r.Nonce = 4 },
		"another recipient": func(r *Request) { r.To = hot },
		"another value":     func(r *Request) { r.Value = eth(11) },
	} {
		r := bump
		change(&r)
		refused(t, policy.CheckHistory(r, signed, eth(0)), why)
	}
	if string(first.Hash()) == string(bump.Hash()) {
		t.Fatal("different requests hash differently")
	}
}
