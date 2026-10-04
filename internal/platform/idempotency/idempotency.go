// Package idempotency stores the outcome of write requests that carry an
// Idempotency-Key (requirements §7.1). The key is claimed in the same
// transaction as the business write, so a repeated request either replays
// the stored response or, if its body differs, fails with
// COMMON_IDEMPOTENCY_CONFLICT. The gateway's Redis cache sits in front of
// this and is only an optimization.
package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/pg"
)

// TTL is how long keys are kept.
const TTL = 24 * time.Hour

// MaxKeyLength bounds client-supplied keys.
const MaxKeyLength = 128

// Response is a stored outcome.
type Response struct {
	StatusCode int
	Body       []byte
}

// ErrConflict reports a key reused with a different request.
var ErrConflict = apperr.New(apperr.KindConflict, apperr.CodeIdempotencyConflict,
	"Idempotency-Key was already used with a different request")

// ErrInvalidKey reports a missing or oversized key.
var ErrInvalidKey = apperr.Invalid("Idempotency-Key must be 1 to 128 characters")

// Hash fingerprints a request: method, path and body.
func Hash(method, path string, body []byte) []byte {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte{0})
	h.Write([]byte(path))
	h.Write([]byte{0})
	h.Write(body)
	return h.Sum(nil)
}

// Claim reserves key within scope for a request with the given hash. It
// returns nil, nil when the caller should perform the write and then call
// Complete in the same transaction; the stored response when the same
// request was already completed; and ErrConflict when the body differs.
// A concurrent claim of the same key blocks until the first transaction
// ends.
func Claim(ctx context.Context, q pg.Querier, scope, key string, hash []byte) (*Response, error) {
	if key == "" || len(key) > MaxKeyLength {
		return nil, ErrInvalidKey
	}
	tag, err := q.Exec(ctx, `INSERT INTO idempotency_keys (scope, key, request_hash) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, scope, key, hash)
	if err != nil {
		return nil, fmt.Errorf("idempotency: claim: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil, nil
	}
	var stored []byte
	var status *int
	var body []byte
	err = q.QueryRow(ctx, `SELECT request_hash, status_code, response FROM idempotency_keys
		WHERE scope = $1 AND key = $2`, scope, key).Scan(&stored, &status, &body)
	if err != nil {
		return nil, fmt.Errorf("idempotency: load: %w", err)
	}
	if !bytes.Equal(stored, hash) || status == nil {
		return nil, ErrConflict
	}
	return &Response{StatusCode: *status, Body: body}, nil
}

// Complete records the response for a claimed key.
func Complete(ctx context.Context, q pg.Querier, scope, key string, resp Response) error {
	tag, err := q.Exec(ctx, `UPDATE idempotency_keys SET status_code = $3, response = $4
		WHERE scope = $1 AND key = $2`, scope, key, resp.StatusCode, resp.Body)
	if err != nil {
		return fmt.Errorf("idempotency: complete: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("idempotency: complete: key %q was not claimed", key)
	}
	return nil
}

// Purge deletes keys older than cutoff.
func Purge(ctx context.Context, q pg.Querier, cutoff time.Time) (int64, error) {
	tag, err := q.Exec(ctx, `DELETE FROM idempotency_keys WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("idempotency: purge: %w", err)
	}
	return tag.RowsAffected(), nil
}
