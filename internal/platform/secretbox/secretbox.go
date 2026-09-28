// Package secretbox encrypts small secrets at rest (TOTP seeds) with
// AES-256-GCM. The key comes from configuration (a base64-encoded 32-byte
// value in the environment, never in the repository); a sealed value is
// a version byte, the random nonce and the ciphertext.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

const version = 1

// Box seals and opens secrets with one key.
type Box struct{ aead cipher.AEAD }

// New reads a base64-encoded 32-byte key.
func New(keyB64 string) (*Box, error) {
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil || len(key) != 32 {
		return nil, errors.New("secretbox: the key must be 32 bytes in base64 (openssl rand -base64 32)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secretbox: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secretbox: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext; aad binds it to its owner (a user ID), so a
// sealed value moved to another row does not open.
func (b *Box) Seal(plaintext, aad []byte) []byte {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	out := append([]byte{version}, nonce...)
	return b.aead.Seal(out, nonce, plaintext, aad)
}

// Open decrypts a sealed value.
func (b *Box) Open(sealed, aad []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < 1+n || sealed[0] != version {
		return nil, errors.New("secretbox: not a sealed value")
	}
	plain, err := b.aead.Open(nil, sealed[1:1+n], sealed[1+n:], aad)
	if err != nil {
		return nil, errors.New("secretbox: the value does not open with this key")
	}
	return plain, nil
}
