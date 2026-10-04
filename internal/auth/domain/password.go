package domain

import (
	_ "embed" // for the weak-password list
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/password"
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

// PasswordCost is the Argon2id cost; hashing lives in
// internal/platform/password, shared with the admin console's logins.
type PasswordCost = password.Cost

// PasswordHasher hashes passwords with Argon2id.
type PasswordHasher = password.Hasher

// DefaultPasswordCost is §5.2: 64 MiB, 3 passes (1 lane).
var DefaultPasswordCost = password.DefaultCost

// NewPasswordHasher allows concurrency hashes at a time.
func NewPasswordHasher(concurrency int, cost PasswordCost) *PasswordHasher {
	return password.NewHasher(concurrency, cost)
}
