package evm

import (
	"math/big"
	"testing"

	"github.com/shopspring/decimal"
)

// BIP32 test vector 1's m/0H chain: a known public key derivation.
const vector1 = "xpub68Gmy5EdvgibQVfPdqkBBCHxA5htiqg55crXYuXoQRKfDBFA1WEjWgP6LHhwBZeNK1VTsfTFUHCdrfp1bgwQ9xv5ski8PX9rL2dZXvgGDnw"

func TestDeriverAddresses(t *testing.T) {
	d, err := NewDeriver(vector1)
	if err != nil {
		t.Fatal(err)
	}
	a0, err := d.Address(0)
	if err != nil {
		t.Fatal(err)
	}
	a1, _ := d.Address(1)
	again, _ := d.Address(0)
	if a0 == a1 || a0 != again || !ValidAddress(a0) || Checksum(a0) != a0 {
		t.Fatalf("addresses %s %s", a0, a1)
	}
	if _, err := d.Address(1 << 31); err == nil {
		t.Fatal("hardened indexes are private derivations")
	}
	if _, err := NewDeriver("not an xpub"); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestValidAddress(t *testing.T) {
	for s, want := range map[string]bool{
		"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed": true,  // EIP-55
		"0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed": true,  // all lower
		"0x5AAEB6053F3E94C9B9A09F33669435E7EF1BEAED": true,  // all upper
		"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeD": false, // bad checksum
		"5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed":   false,
		"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeA":   false,
	} {
		if ValidAddress(s) != want {
			t.Errorf("%s: want %v", s, want)
		}
	}
}

func TestWei(t *testing.T) {
	wei, _ := new(big.Int).SetString("1234500000000000000", 10)
	if got := FromWei(wei, 18); got.String() != "1.2345" {
		t.Fatalf("from wei %s", got)
	}
	back, err := ToWei(decimal.RequireFromString("1.2345"), 18)
	if err != nil || back.Cmp(wei) != 0 {
		t.Fatalf("to wei %s %v", back, err)
	}
	if _, err := ToWei(decimal.RequireFromString("0.0000001"), 6); err == nil {
		t.Fatal("too many decimals")
	}
}
