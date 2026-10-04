// Package ports declares what the signer's application layer needs.
package ports

import (
	"context"
	"crypto/ecdsa"
	"math/big"
	"time"

	"github.com/skill/exchange/internal/signer/domain"
)

// Keys derives the platform wallet's keys (the opened keystore).
type Keys interface {
	// Key returns the key of m/44'/60'/account'/0/index and its address.
	Key(account, index uint32) (*ecdsa.PrivateKey, string, error)
}

// Store keeps the signer's audit.
type Store interface {
	// Tx runs fn in a transaction that holds the signer's lock, so the
	// limits see every earlier signature.
	Tx(ctx context.Context, fn func(Repo) error) error
	// Refuse records a refused request.
	Refuse(ctx context.Context, r domain.Request, reason string) error
}

// Repo reads and appends signatures.
type Repo interface {
	// Get returns the signature of a request ID with its request hash, or
	// nil.
	Get(ctx context.Context, requestID string) (*domain.Signature, []byte, error)
	// ByReference lists the signatures of a withdrawal or sweep.
	ByReference(ctx context.Context, reference string) ([]domain.Signature, error)
	// Withdrawn sums the value of the withdrawals first signed since t.
	Withdrawn(ctx context.Context, since time.Time) (*big.Int, error)
	Insert(ctx context.Context, s domain.Signature, hash []byte) error
}
