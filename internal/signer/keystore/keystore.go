// Package keystore keeps the platform's wallet seed (a BIP39 mnemonic)
// encrypted on the signer's disk (requirements §5.10, ADR-0003): scrypt
// derives a key from the passphrase, AES-256-GCM seals the mnemonic. Only
// the signer opens it; the business services get the account xpub, from
// which they derive deposit addresses without any private key.
package keystore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/tyler-smith/go-bip39"
	"golang.org/x/crypto/scrypt"
)

// Accounts of the platform wallet (BIP44 m/44'/60'/account'): deposit
// addresses are m/44'/60'/0'/0/i; the hot wallet is account 1.
const (
	DepositAccount = 0
	HotAccount     = 1
)

// scrypt parameters (N=2^18, as Ethereum keystores use).
const (
	scryptN = 1 << 18
	scryptR = 8
	scryptP = 1
)

type file struct {
	Version    int       `json:"version"`
	Salt       []byte    `json:"salt"`
	N          int       `json:"n"`
	R          int       `json:"r"`
	P          int       `json:"p"`
	Nonce      []byte    `json:"nonce"`
	Ciphertext []byte    `json:"ciphertext"`
	DepositPub string    `json:"deposit_xpub"`
	CreatedAt  time.Time `json:"created_at"`
}

// Keystore is an opened keystore.
type Keystore struct {
	master *hdkeychain.ExtendedKey
}

func aead(passphrase string, salt []byte, n, r, p int) (cipher.AEAD, error) {
	if len(passphrase) < 16 {
		return nil, errors.New("keystore: the passphrase must have at least 16 characters")
	}
	key, err := scrypt.Key([]byte(passphrase), salt, n, r, p, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Create writes a new keystore with a fresh 24-word mnemonic to path,
// which must not exist, and returns the deposit account's xpub.
func Create(path, passphrase string) (string, error) {
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("keystore: %s exists", path)
	}
	entropy, err := bip39.NewEntropy(256)
	if err != nil {
		return "", err
	}
	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return "", err
	}
	ks, err := fromMnemonic(mnemonic)
	if err != nil {
		return "", err
	}
	xpub, err := ks.AccountXPub(DepositAccount)
	if err != nil {
		return "", err
	}
	f := file{Version: 1, N: scryptN, R: scryptR, P: scryptP, Salt: make([]byte, 32), Nonce: make([]byte, 12), DepositPub: xpub, CreatedAt: time.Now().UTC()}
	if _, err := rand.Read(f.Salt); err != nil {
		return "", err
	}
	if _, err := rand.Read(f.Nonce); err != nil {
		return "", err
	}
	a, err := aead(passphrase, f.Salt, f.N, f.R, f.P)
	if err != nil {
		return "", err
	}
	f.Ciphertext = a.Seal(nil, f.Nonce, []byte(mnemonic), nil)
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", err
	}
	return xpub, nil
}

// Open decrypts the keystore at path.
func Open(path, passphrase string) (*Keystore, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil || f.Version != 1 {
		return nil, errors.New("keystore: not a keystore file")
	}
	a, err := aead(passphrase, f.Salt, f.N, f.R, f.P)
	if err != nil {
		return nil, err
	}
	mnemonic, err := a.Open(nil, f.Nonce, f.Ciphertext, nil)
	if err != nil {
		return nil, errors.New("keystore: wrong passphrase")
	}
	return fromMnemonic(string(mnemonic))
}

func fromMnemonic(mnemonic string) (*Keystore, error) {
	seed, err := bip39.NewSeedWithErrorChecking(mnemonic, "")
	if err != nil {
		return nil, err
	}
	master, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	if err != nil {
		return nil, err
	}
	return &Keystore{master: master}, nil
}

// Account returns the private key of m/44'/60'/account'.
func (k *Keystore) Account(account uint32) (*hdkeychain.ExtendedKey, error) {
	key := k.master
	for _, idx := range []uint32{44, 60, account} {
		var err error
		if key, err = key.Derive(hdkeychain.HardenedKeyStart + idx); err != nil {
			return nil, err
		}
	}
	return key, nil
}

// AccountXPub returns the extended public key of m/44'/60'/account'.
func (k *Keystore) AccountXPub(account uint32) (string, error) {
	key, err := k.Account(account)
	if err != nil {
		return "", err
	}
	pub, err := key.Neuter()
	if err != nil {
		return "", err
	}
	return pub.String(), nil
}

// Key returns the private key of m/44'/60'/account'/0/index with its
// checksummed address.
func (k *Keystore) Key(account, index uint32) (*ecdsa.PrivateKey, string, error) {
	if index >= hdkeychain.HardenedKeyStart {
		return nil, "", errors.New("keystore: index out of range")
	}
	acct, err := k.Account(account)
	if err != nil {
		return nil, "", err
	}
	external, err := acct.Derive(0)
	if err != nil {
		return nil, "", err
	}
	child, err := external.Derive(index)
	if err != nil {
		return nil, "", err
	}
	priv, err := child.ECPrivKey()
	if err != nil {
		return nil, "", err
	}
	key := priv.ToECDSA()
	return key, crypto.PubkeyToAddress(key.PublicKey).Hex(), nil
}
