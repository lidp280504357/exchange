package domain

import (
	"strings"
	"testing"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

var testCost = PasswordCost{MemoryKiB: 64, Iterations: 1}

// passwordCases are test data, not credentials.
//
//nolint:gosec // G101 mistakes them for hardcoded passwords
var passwordCases = []struct {
	pw   string
	ids  []string
	weak bool
}{
	{pw: "correct horse battery", weak: false},
	{pw: "Tr0ub4dor&3x", weak: false},
	{pw: "短短的中文密码也可以很长", weak: false},
	{pw: "short1!", weak: true},
	{pw: strings.Repeat("x", MaxPasswordLength+1), weak: true},
	{pw: "password123", weak: true},
	{pw: "PASSWORD123", weak: true},
	{pw: "qwertyuiop", weak: true},
	{pw: "aaaaaaaaaaaa", weak: true},
	{pw: "abcabcabcabc", weak: true},
	{pw: "0123456789", weak: true},
	{pw: "123456789012", weak: true},
	{pw: "9876543210987", weak: true},
	{pw: "Password2026!", weak: true},
	{pw: "qwerty!!!!!!", weak: true},
	{pw: "bitcoin123456", weak: true},
	{pw: "4829107365", weak: false},
	{pw: "passage of time", weak: false},
	{pw: "zyxwvutsrqpo", weak: true},
	{pw: "alice.smith2026", ids: []string{"alice.smith@example.com"}, weak: true},
	{pw: "my+8613800138000pw", ids: []string{"+8613800138000"}, weak: true},
	{pw: "bobby tables forever", ids: []string{"bob@example.com"}, weak: false},
}

func TestCheckPassword(t *testing.T) {
	for _, tc := range passwordCases {
		err := CheckPassword(tc.pw, tc.ids...)
		if weak := apperr.Is(err, "AUTH_PASSWORD_WEAK"); weak != tc.weak {
			t.Errorf("CheckPassword(%q, %v) = %v, want weak=%v", tc.pw, tc.ids, err, tc.weak)
		}
	}
}

func TestPasswordHasher(t *testing.T) {
	h := NewPasswordHasher(2, testCost)
	enc := h.Hash("correct horse battery")
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatalf("hash format: %s", enc)
	}
	if enc == h.Hash("correct horse battery") {
		t.Fatal("hashes must be salted")
	}
	if ok, err := h.Verify(enc, "correct horse battery"); err != nil || !ok {
		t.Fatalf("verify right password: %v %v", ok, err)
	}
	if ok, err := h.Verify(enc, "correct horse batterY"); err != nil || ok {
		t.Fatalf("verify wrong password: %v %v", ok, err)
	}
	// A hash made with another cost still verifies.
	strong := NewPasswordHasher(1, PasswordCost{MemoryKiB: 128, Iterations: 2}).Hash("pw of another cost")
	if ok, err := h.Verify(strong, "pw of another cost"); err != nil || !ok {
		t.Fatalf("verify other cost: %v %v", ok, err)
	}
	for _, bad := range []string{"", "$2a$10$abc", "$argon2id$v=19$m=0,t=1,p=1$AAAA$AAAA", "$argon2id$v=19$m=64,t=1,p=1$!!$AAAA", "$argon2id$v=19$m=64,t=1,p=1$AAAA$AAAA"} {
		if _, err := h.Verify(bad, "x"); err == nil {
			t.Errorf("Verify(%q) accepted a malformed hash", bad)
		}
	}
	h.VerifyDummy("anything") // must not panic
}
