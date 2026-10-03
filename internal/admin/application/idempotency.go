package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"strings"

	"github.com/google/uuid"

	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/idempotency"
)

// Idempotency-Keys of the requests that move money (C5.5 ⑥): the console
// sends a key with each. The first request with a key claims it with its
// fingerprint and the ID of what it makes (a fund operation, a hold, a
// closing order, a message); the same request again gets that ID, so what
// it made is looked up or finished instead of made twice; the key with
// another request fails with COMMON_IDEMPOTENCY_CONFLICT. A key is the
// administrator's own and is kept a day.

// Scopes of the keys: what a request does.
const (
	scopeFunds     = "funds"
	scopeHold      = "hold"
	scopeRelease   = "hold.release"
	scopeClose     = "position.close"
	scopeReview    = "withdrawal.review"
	scopeBatch     = "withdrawal.review_batch"
	scopeCredit    = "deposit.credit"
	scopeBroadcast = "broadcast"
)

// claim is a claimed key: the ID of what its request makes, and whether
// this request is the first with the key.
type claim struct {
	Ref   string
	Fresh bool
}

// needKey checks an Idempotency-Key.
func needKey(key string) error {
	if k := strings.TrimSpace(key); k == "" || len(k) > idempotency.MaxKeyLength {
		return idempotency.ErrInvalidKey
	}
	return nil
}

// fingerprint hashes a request's fields.
func fingerprint(fields ...string) []byte {
	h := sha256.New()
	for _, f := range fields {
		h.Write([]byte(f))
		h.Write([]byte{0})
	}
	return h.Sum(nil)
}

// claimIn claims an administrator's key for a request of a scope in r,
// with a new UUIDv7 as the ID of what it makes. Without a key (the HTTP
// API requires one; tests and the service's own calls go without) the
// request is the first.
func (s *Service) claimIn(ctx context.Context, r ports.Repos, p Principal, key, scope string, hash []byte) (claim, error) {
	ref := uuid.Must(uuid.NewV7()).String()
	if key == "" {
		return claim{Ref: ref, Fresh: true}, nil
	}
	if err := needKey(key); err != nil {
		return claim{}, err
	}
	stored, first, fresh, err := r.Keys().Claim(ctx, p.Admin.ID+" "+scope, strings.TrimSpace(key), hash, ref, s.Now())
	if err != nil {
		return claim{}, err
	}
	if !bytes.Equal(stored, hash) {
		return claim{}, idempotency.ErrConflict
	}
	return claim{Ref: first, Fresh: fresh}, nil
}

// claimKey is claimIn in a transaction of its own.
func (s *Service) claimKey(ctx context.Context, p Principal, key, scope string, hash []byte) (claim, error) {
	if key == "" {
		return claim{Ref: uuid.Must(uuid.NewV7()).String(), Fresh: true}, nil
	}
	var c claim
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		var err error
		c, err = s.claimIn(ctx, r, p, key, scope, hash)
		return err
	})
	return c, err
}

// PurgeKeys deletes the keys older than a day.
func (s *Service) PurgeKeys(ctx context.Context) (int64, error) {
	return s.Store.Read().Keys().Purge(ctx, s.Now().Add(-idempotency.TTL))
}
