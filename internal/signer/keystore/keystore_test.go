package keystore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/skill/exchange/internal/platform/evm"
)

func TestCreateOpenAndDerive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keystore.json")
	pass := "a long enough passphrase"
	xpub, err := Create(path, pass)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(xpub, "xpub") {
		t.Fatalf("xpub %s", xpub)
	}
	raw, _ := os.ReadFile(path)
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 || strings.Contains(string(raw), "abandon") {
		t.Fatalf("mode %v", info.Mode())
	}
	if _, err := Create(path, pass); err == nil {
		t.Fatal("an existing keystore must not be overwritten")
	}
	if _, err := Open(path, "the wrong passphrase!!"); err == nil {
		t.Fatal("wrong passphrase opened it")
	}
	if _, err := Create(filepath.Join(t.TempDir(), "k.json"), "short"); err == nil {
		t.Fatal("a short passphrase must be refused")
	}
	ks, err := Open(path, pass)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := ks.AccountXPub(DepositAccount); again != xpub {
		t.Fatal("the reopened keystore has another xpub")
	}
	// The signer's private derivation and the wallet's public one agree.
	acct, err := ks.Account(DepositAccount)
	if err != nil {
		t.Fatal(err)
	}
	d, err := evm.NewDeriver(xpub)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []uint32{0, 1, 7} {
		ext, _ := acct.Derive(0)
		child, _ := ext.Derive(i)
		priv, err := child.ECPrivKey()
		if err != nil {
			t.Fatal(err)
		}
		want := crypto.PubkeyToAddress(priv.ToECDSA().PublicKey).Hex()
		if got, _ := d.Address(i); got != want {
			t.Fatalf("index %d: public %s, private %s", i, got, want)
		}
	}
	key, addr, err := ks.Key(DepositAccount, 7)
	if want, _ := d.Address(7); err != nil || addr != want {
		t.Fatalf("Key agrees with the public derivation: %s %v", addr, err)
	}
	if key.Curve != crypto.S256() {
		t.Fatal("keys must be on go-ethereum's curve value, or the pure-Go signer refuses them")
	}
	if _, err := crypto.Sign(crypto.Keccak256([]byte("x")), key); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if hot, _ := ks.AccountXPub(HotAccount); hot == xpub {
		t.Fatal("the hot wallet is another account")
	}
	if _, err := evm.NewDeriver(acct.String()); err == nil {
		t.Fatal("an xprv must be refused")
	}
}
