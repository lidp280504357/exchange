package domain

import (
	"crypto/subtle"
	_ "embed" // for the weak-password list
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Password policy (requirements §5.2): 10 to 128 characters, no
// composition rules, no common or trivially guessable passwords.
const (
	MinPasswordLength = 10
	MaxPasswordLength = 128
)

// Password errors.
var (
	ErrPasswordWeak    = apperr.New(apperr.KindInvalid, "AUTH_PASSWORD_WEAK", "choose a longer or less common password")
	ErrPasswordInvalid = apperr.New(apperr.KindUnauthenticated, "AUTH_PASSWORD_INVALID", "the account or password is incorrect")
)

//go:embed weak_passwords.txt
var weakList string

var weakPasswords = func() map[string]bool {
	m := map[string]bool{}
	for _, line := range strings.Split(weakList, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			m[strings.ToLower(line)] = true
		}
	}
	return m
}()

// CheckPassword applies the policy. identifiers are the user's email and
// phone, which must not be (part of) the password.
func CheckPassword(pw string, identifiers ...string) error {
	n := utf8.RuneCountInString(pw)
	if n < MinPasswordLength || n > MaxPasswordLength {
		return ErrPasswordWeak.WithDetail("min_length", MinPasswordLength).WithDetail("max_length", MaxPasswordLength)
	}
	lower := strings.ToLower(pw)
	if weakPasswords[lower] || weakPasswords[stripSuffix(lower)] || repetitive(lower) || sequential(lower) {
		return ErrPasswordWeak
	}
	for _, id := range identifiers {
		id = strings.ToLower(id)
		local, _, _ := strings.Cut(id, "@")
		if id != "" && (strings.Contains(lower, id) || (len(local) >= 4 && strings.Contains(lower, local))) {
			return ErrPasswordWeak
		}
	}
	return nil
}

// repetitive catches passwords made of one short unit, e.g. "aaaaaaaaaa"
// or "abcabcabcabc".
func repetitive(s string) bool {
	for unit := 1; unit <= 3 && unit < len(s); unit++ {
		if strings.Repeat(s[:unit], len(s)/unit+1)[:len(s)] == s {
			return true
		}
	}
	return false
}

// stripSuffix drops the digits and symbols people append to a common word
// to pass a length rule, e.g. "password2026!".
func stripSuffix(s string) string {
	return strings.TrimRightFunc(s, func(r rune) bool {
		return unicode.IsDigit(r) || strings.ContainsRune("!@#$%^&*()_+-=.,?~", r)
	})
}

// sequential catches runs such as "0123456789", "123456789012" (digits
// wrap around) or "abcdefghijk".
func sequential(s string) bool {
	step := func(a, b byte, d int) bool {
		if isDigit(a) && isDigit(b) {
			return int(b-'0') == (int(a-'0')+d+10)%10
		}
		return int(b) == int(a)+d
	}
	up, down := true, true
	for i := 1; i < len(s); i++ {
		up = up && step(s[i-1], s[i], 1)
		down = down && step(s[i-1], s[i], -1)
	}
	return up || down
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// PasswordCost is the Argon2id cost. Production uses the §5.2 cost; tests
// pass a lower one to stay fast. Verify honors the cost stored in a hash,
// so raising it later needs no migration.
type PasswordCost struct {
	MemoryKiB  uint32
	Iterations uint32
}

// DefaultPasswordCost is §5.2: 64 MiB, 3 passes (1 lane).
var DefaultPasswordCost = PasswordCost{MemoryKiB: 64 * 1024, Iterations: 3}

const (
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

// PasswordHasher hashes with Argon2id. Each hash takes MemoryKiB, so the
// number of concurrent hashes is capped to keep the process within its
// memory limit.
type PasswordHasher struct {
	slots chan struct{}
	cost  PasswordCost
	dummy func() string
}

// NewPasswordHasher allows concurrency hashes at a time.
func NewPasswordHasher(concurrency int, cost PasswordCost) *PasswordHasher {
	h := &PasswordHasher{slots: make(chan struct{}, max(concurrency, 1)), cost: cost}
	// The dummy hash lets a login for an unknown account spend the same
	// time as a real one; it is computed on first use.
	h.dummy = sync.OnceValue(func() string { return h.Hash("dummy password for timing") })
	return h
}

// Hash returns a PHC string: $argon2id$v=19$m=65536,t=3,p=1$salt$hash.
func (h *PasswordHasher) Hash(pw string) string {
	h.slots <- struct{}{}
	defer func() { <-h.slots }()
	salt := RandomBytes(argonSaltLen)
	key := argon2.IDKey([]byte(pw), salt, h.cost.Iterations, h.cost.MemoryKiB, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, h.cost.MemoryKiB, h.cost.Iterations, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

// Verify checks pw against a PHC string produced by Hash, honoring the
// parameters stored in it.
func (h *PasswordHasher) Verify(encoded, pw string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("unsupported password hash")
	}
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false, fmt.Errorf("password hash parameters: %w", err)
	}
	if memory == 0 || memory > 1<<20 || iterations == 0 || iterations > 16 || threads == 0 {
		return false, errors.New("password hash parameters out of range")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("password hash salt: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) != argonKeyLen {
		return false, errors.New("password hash is malformed")
	}
	h.slots <- struct{}{}
	defer func() { <-h.slots }()
	got := argon2.IDKey([]byte(pw), salt, iterations, memory, threads, argonKeyLen)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// VerifyDummy burns one hash to equalize timing.
func (h *PasswordHasher) VerifyDummy(pw string) {
	_, _ = h.Verify(h.dummy(), pw)
}
