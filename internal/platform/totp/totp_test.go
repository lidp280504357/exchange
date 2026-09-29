package totp

import (
	"strings"
	"testing"
	"time"
)

func TestRFC6238Vectors(t *testing.T) {
	// RFC 6238 appendix B (SHA-1 seed), 6-digit truncation.
	secret := []byte("12345678901234567890")
	for unix, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"} {
		if got := Code(secret, Step(time.Unix(unix, 0))); got != want {
			t.Errorf("%d: %s, want %s", unix, got, want)
		}
	}
}

func TestVerify(t *testing.T) {
	secret := NewSecret()
	now := time.Unix(1_790_000_000, 0)
	step := Step(now)
	if got, ok := Verify(secret, Code(secret, step), now, 0); !ok || got != step {
		t.Fatal("the current code")
	}
	if _, ok := Verify(secret, Code(secret, step+1), now, 0); !ok {
		t.Fatal("one step of skew")
	}
	if _, ok := Verify(secret, Code(secret, step+2), now, 0); ok {
		t.Fatal("two steps ahead")
	}
	if _, ok := Verify(secret, Code(secret, step), now, step); ok {
		t.Fatal("a used step")
	}
	if _, ok := Verify(secret, "12345", now, 0); ok {
		t.Fatal("five digits")
	}
	enc := Encode(secret)
	if back, err := Decode(strings.ToLower(enc) + "=="); err != nil || string(back) != string(secret) {
		t.Fatalf("decode %v", err)
	}
	if u := URI("Exchange", "a@example.com", secret); !strings.HasPrefix(u, "otpauth://totp/Exchange:a@example.com?") {
		t.Fatal(u)
	}
}
