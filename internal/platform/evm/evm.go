// Package evm holds chain-neutral helpers for EVM networks (requirements
// §5.10): deposit addresses derived from an account xpub (BIP32 public
// derivation, no private key), EIP-55 addresses and wei amounts.
package evm

import (
	"errors"
	"fmt"
	"math/big"
	"sync"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/shopspring/decimal"
)

// Deriver derives the addresses of an account's external chain
// (m/44'/60'/account'/0/i) from its xpub.
type Deriver struct {
	mu       sync.Mutex
	external *hdkeychain.ExtendedKey
}

// NewDeriver parses an account xpub; a private key is refused, since the
// business services must never hold one (ADR-0003).
func NewDeriver(xpub string) (*Deriver, error) {
	key, err := hdkeychain.NewKeyFromString(xpub)
	if err != nil {
		return nil, fmt.Errorf("evm: bad xpub: %w", err)
	}
	if key.IsPrivate() {
		return nil, errors.New("evm: an xprv was given where an xpub belongs")
	}
	external, err := key.Derive(0)
	if err != nil {
		return nil, err
	}
	return &Deriver{external: external}, nil
}

// Address returns the checksummed address at index.
func (d *Deriver) Address(index uint32) (string, error) {
	if index >= hdkeychain.HardenedKeyStart {
		return "", errors.New("evm: index out of range")
	}
	d.mu.Lock()
	child, err := d.external.Derive(index)
	d.mu.Unlock()
	if err != nil {
		return "", err
	}
	pub, err := child.ECPubKey()
	if err != nil {
		return "", err
	}
	return crypto.PubkeyToAddress(*pub.ToECDSA()).Hex(), nil
}

// ValidAddress reports whether s is a 0x address whose mixed case, if any,
// matches its EIP-55 checksum.
func ValidAddress(s string) bool {
	if !common.IsHexAddress(s) || len(s) != 42 || s[:2] != "0x" {
		return false
	}
	lower, upper := true, true
	for _, c := range s[2:] {
		if c >= 'a' && c <= 'f' {
			upper = false
		}
		if c >= 'A' && c <= 'F' {
			lower = false
		}
	}
	return lower || upper || common.HexToAddress(s).Hex() == s
}

// Checksum returns the EIP-55 form of a valid address.
func Checksum(s string) string { return common.HexToAddress(s).Hex() }

// FromWei turns an integer amount of the smallest unit into a decimal of
// the asset's decimals (exact).
func FromWei(v *big.Int, decimals int32) decimal.Decimal {
	return decimal.NewFromBigInt(v, -decimals)
}

// ToWei turns a decimal amount into the smallest unit; more decimals than
// the asset has are an error.
func ToWei(d decimal.Decimal, decimals int32) (*big.Int, error) {
	scaled := d.Shift(decimals)
	if !scaled.Equal(scaled.Truncate(0)) {
		return nil, fmt.Errorf("evm: %s has more than %d decimals", d, decimals)
	}
	return scaled.BigInt(), nil
}
