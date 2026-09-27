package domain

import (
	"errors"
	"regexp"
	"testing"
	"time"
)

func TestParseIdentifier(t *testing.T) {
	cases := []struct {
		ch      Channel
		raw     string
		want    string
		region  string
		invalid bool
	}{
		{ChannelEmail, "  Alice@Example.COM ", "alice@example.com", "", false},
		{ChannelEmail, "a.b+tag@sub.example.co", "a.b+tag@sub.example.co", "", false},
		{ChannelEmail, "no-at-sign", "", "", true},
		{ChannelEmail, "a@nodot", "", "", true},
		{ChannelEmail, "a@@b.com", "", "", true},
		{ChannelEmail, "a b@example.com", "", "", true},
		{ChannelEmail, "<a@example.com>", "", "", true},
		{ChannelEmail, "a@.example.com", "", "", true},
		{ChannelSMS, "+86 138 1234 1234", "+8613812341234", "CN", false},
		{ChannelSMS, "+1 (415) 555-2671", "+14155552671", "US", false},
		{ChannelSMS, "13812341234", "", "", true},   // no country code
		{ChannelSMS, "+86 123", "", "", true},       // too short
		{ChannelSMS, "+999 12345678", "", "", true}, // no such country
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			id, err := ParseIdentifier(tc.ch, tc.raw)
			if tc.invalid {
				if err == nil {
					t.Fatalf("want an error, got %+v", id)
				}
				return
			}
			if err != nil || id.Value != tc.want || id.Region != tc.region || id.Channel != tc.ch {
				t.Fatalf("got %+v, %v; want %s (%s)", id, err, tc.want, tc.region)
			}
		})
	}
}

func TestParseSceneAndChannel(t *testing.T) {
	if s, err := ParseScene(" register "); err != nil || s != SceneRegister || s.Authenticated() {
		t.Fatalf("scene: %v %v", s, err)
	}
	if s, _ := ParseScene("STEP_UP"); !s.Authenticated() {
		t.Fatal("STEP_UP needs a signed-in user")
	}
	if _, err := ParseScene("HACK"); err == nil {
		t.Fatal("unknown scene")
	}
	if c, err := ParseChannel("sms"); err != nil || c != ChannelSMS || c.Kind() != "PHONE" {
		t.Fatalf("channel: %v %v", c, err)
	}
	if _, err := ParseChannel("fax"); err == nil {
		t.Fatal("unknown channel")
	}
}

func TestNewCodeAndTokens(t *testing.T) {
	digits := regexp.MustCompile(`^\d{6}$`)
	seen := map[string]bool{}
	for range 200 {
		c := NewCode()
		if !digits.MatchString(c) {
			t.Fatalf("code %q is not six digits", c)
		}
		seen[c] = true
	}
	if len(seen) < 190 {
		t.Fatalf("codes repeat too often: %d distinct of 200", len(seen))
	}
	plain, hash := NewToken()
	if len(plain) < 40 || string(HashToken(plain)) != string(hash) {
		t.Fatal("token hash must be reproducible from the plain token")
	}
}

func TestCodeHasher(t *testing.T) {
	if _, err := NewCodeHasher([]byte("short")); err == nil {
		t.Fatal("short keys must be rejected")
	}
	h, _ := NewCodeHasher(RandomBytes(32))
	other, _ := NewCodeHasher(RandomBytes(32))
	hash := h.Hash("c-1", "123456")
	if !h.Match(hash, "c-1", "123456") || h.Match(hash, "c-2", "123456") || h.Match(hash, "c-1", "123457") ||
		other.Match(hash, "c-1", "123456") {
		t.Fatal("hash must bind code, challenge and key")
	}
}

func challenge(t *testing.T, h CodeHasher, now time.Time) *Challenge {
	t.Helper()
	return &Challenge{
		ID: "c-1", DeviceID: "device-1", CodeHash: h.Hash("c-1", "123456"),
		Status: ChallengePending, ExpiresAt: now.Add(CodeTTL),
	}
}

func TestChallengeAttempt(t *testing.T) {
	h, _ := NewCodeHasher(RandomBytes(32))
	now := time.Now()

	c := challenge(t, h, now)
	if err := c.Attempt("123456", "device-1", now, h); err != nil || c.Status != ChallengeVerified || c.Attempts != 1 {
		t.Fatalf("right code: %v %+v", err, c)
	}
	if err := c.Attempt("123456", "device-1", now, h); !errors.Is(err, ErrOTPInvalid) {
		t.Fatalf("a verified challenge cannot be reused: %v", err)
	}

	c = challenge(t, h, now)
	if err := c.Attempt("123456", "other-device", now, h); !errors.Is(err, ErrOTPInvalid) {
		t.Fatalf("another device: %v", err)
	}
	if err := c.Attempt("123456", "device-1", now.Add(CodeTTL), h); !errors.Is(err, ErrOTPExpired) {
		t.Fatalf("expired: %v", err)
	}

	c = challenge(t, h, now)
	for i := 1; i < MaxAttempts; i++ {
		if err := c.Attempt("000000", "device-1", now, h); !errors.Is(err, ErrOTPInvalid) {
			t.Fatalf("wrong code %d: %v", i, err)
		}
	}
	if err := c.Attempt("000000", "device-1", now, h); !errors.Is(err, ErrOTPAttemptsExceeded) || c.Status != ChallengeLocked {
		t.Fatalf("fifth wrong code must lock: %v %+v", err, c)
	}
	if err := c.Attempt("123456", "device-1", now, h); !errors.Is(err, ErrOTPAttemptsExceeded) {
		t.Fatalf("a locked challenge stays locked: %v", err)
	}
}

func TestValidDeviceID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{{"01a0e486-66af", true}, {"short", false}, {" padded-device ", false}, {"", false}}
	for _, tc := range cases {
		if ValidDeviceID(tc.id) != tc.want {
			t.Errorf("%q: want %v", tc.id, tc.want)
		}
	}
}
