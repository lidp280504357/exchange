package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/skill/exchange/internal/platform/apperr"
)

// RFC 6238 appendix B, SHA-1: the 8-digit codes cut to our 6 digits.
func TestTOTPCodesMatchRFC6238(t *testing.T) {
	secret := []byte("12345678901234567890")
	for _, c := range []struct {
		unix int64
		want string
	}{
		{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"},
	} {
		if got := TOTPCode(secret, TOTPStep(time.Unix(c.unix, 0))); got != c.want {
			t.Errorf("T=%d: %s, want %s", c.unix, got, c.want)
		}
	}
}

func TestVerifyTOTP(t *testing.T) {
	secret := NewTOTPSecret()
	now := time.Unix(1_790_000_000, 0)
	step := TOTPStep(now)
	code := TOTPCode(secret, step)
	got, err := VerifyTOTP(secret, code, now, 0)
	if err != nil || got != step {
		t.Fatalf("current code: step %d, %v", got, err)
	}
	// One step of skew either way.
	if _, err := VerifyTOTP(secret, TOTPCode(secret, step-1), now, 0); err != nil {
		t.Fatalf("previous step: %v", err)
	}
	if _, err := VerifyTOTP(secret, TOTPCode(secret, step+1), now, 0); err != nil {
		t.Fatalf("next step: %v", err)
	}
	if _, err := VerifyTOTP(secret, TOTPCode(secret, step-2), now, 0); !apperr.Is(err, "AUTH_TOTP_INVALID") {
		t.Fatalf("two steps old: %v", err)
	}
	// A code used once is refused, and so is any older one.
	if _, err := VerifyTOTP(secret, code, now, step); !apperr.Is(err, "AUTH_TOTP_INVALID") {
		t.Fatalf("replay: %v", err)
	}
	if _, err := VerifyTOTP(secret, "12345", now, 0); err == nil {
		t.Fatal("five digits")
	}
}

func TestTOTPURI(t *testing.T) {
	secret := []byte("12345678901234567890")
	uri := TOTPURI("Exchange", "a***@x.com", secret)
	if !strings.HasPrefix(uri, "otpauth://totp/Exchange:a%2A%2A%2A@x.com?") || !strings.Contains(uri, "secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ") ||
		!strings.Contains(uri, "issuer=Exchange") || !strings.Contains(uri, "period=30") {
		t.Fatalf("uri %s", uri)
	}
	if EncodeTOTPSecret(secret) != "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" {
		t.Fatal("base32 without padding")
	}
}
