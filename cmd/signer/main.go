// Command signer holds the platform wallet's keys (requirements §5.10,
// ADR-0003). This first part manages the encrypted keystore; signing
// withdrawals comes with plan §6.3 task 10.
//
//	SIGNER_PASSPHRASE=... signer init --keystore /keystore/keystore.json
//	SIGNER_PASSPHRASE=... signer xpub --keystore /keystore/keystore.json
//
// Both print only the deposit account's xpub, which wallet-service takes
// as WALLET_XPUB to derive deposit addresses; the mnemonic never leaves the
// keystore.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/lidp280504357/exchange/internal/signer/keystore"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "signer:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: signer init|xpub --keystore PATH (passphrase in SIGNER_PASSPHRASE)")
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	path := fs.String("keystore", "/keystore/keystore.json", "keystore file")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	pass := os.Getenv("SIGNER_PASSPHRASE")
	switch args[0] {
	case "init":
		xpub, err := keystore.Create(*path, pass)
		if err != nil {
			return err
		}
		fmt.Println(xpub)
	case "xpub":
		ks, err := keystore.Open(*path, pass)
		if err != nil {
			return err
		}
		xpub, err := ks.AccountXPub(keystore.DepositAccount)
		if err != nil {
			return err
		}
		fmt.Println(xpub)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
	return nil
}
