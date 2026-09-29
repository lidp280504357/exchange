// Package password hashes passwords with Argon2id (requirements §5.2) into
// PHC strings, capping concurrent hashes to bound memory.
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Cost is the Argon2id cost. Production uses the §5.2 cost; tests pass a
// lower one to stay fast. Verify honors the cost stored in a hash, so
// raising it later needs no migration.
type Cost struct {
	MemoryKiB  uint32
	Iterations uint32
}

// DefaultCost is §5.2: 64 MiB, 3 passes (1 lane).
var DefaultCost = Cost{MemoryKiB: 64 * 1024, Iterations: 3}

const (
	threads = 1
	keyLen  = 32
	saltLen = 16
)

// Hasher hashes with Argon2id. Each hash takes MemoryKiB, so the number
// of concurrent hashes is capped to keep the process within its memory
// limit.
type Hasher struct {
	slots chan struct{}
	cost  Cost
	dummy func() string
}

// NewHasher allows concurrency hashes at a time.
func NewHasher(concurrency int, cost Cost) *Hasher {
	h := &Hasher{slots: make(chan struct{}, max(concurrency, 1)), cost: cost}
	// The dummy hash lets a login for an unknown account spend the same
	// time as a real one; it is computed on first use.
	h.dummy = sync.OnceValue(func() string { return h.Hash("dummy password for timing") })
	return h
}

// Hash returns a PHC string: $argon2id$v=19$m=65536,t=3,p=1$salt$hash.
func (h *Hasher) Hash(pw string) string {
	h.slots <- struct{}{}
	defer func() { <-h.slots }()
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	key := argon2.IDKey([]byte(pw), salt, h.cost.Iterations, h.cost.MemoryKiB, threads, keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, h.cost.MemoryKiB, h.cost.Iterations, threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

// Verify checks pw against a PHC string produced by Hash, honoring the
// parameters stored in it.
func (h *Hasher) Verify(encoded, pw string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("unsupported password hash")
	}
	var memory, iterations uint32
	var lanes uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &lanes); err != nil {
		return false, fmt.Errorf("password hash parameters: %w", err)
	}
	if memory == 0 || memory > 1<<20 || iterations == 0 || iterations > 16 || lanes == 0 {
		return false, errors.New("password hash parameters out of range")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("password hash salt: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) != keyLen {
		return false, errors.New("password hash is malformed")
	}
	h.slots <- struct{}{}
	defer func() { <-h.slots }()
	got := argon2.IDKey([]byte(pw), salt, iterations, memory, lanes, keyLen)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// VerifyDummy burns one hash to equalize timing.
func (h *Hasher) VerifyDummy(pw string) {
	_, _ = h.Verify(h.dummy(), pw)
}
