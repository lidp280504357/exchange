package pii

import "testing"

func TestMask(t *testing.T) {
	cases := []struct {
		name string
		fn   func(string) string
		in   string
		want string
	}{
		{"email", MaskEmail, "alice@example.com", "a***@example.com"},
		{"email one char", MaskEmail, "a@x.com", "a***@x.com"},
		{"email unicode", MaskEmail, "张三@example.com", "张***@example.com"},
		{"email malformed", MaskEmail, "not-an-email", "n***l"},
		{"email empty", MaskEmail, "", "***"},
		{"phone cn", MaskPhone, "+8613812341234", "+86138****1234"},
		{"phone us", MaskPhone, "+14155550123", "+14155****0123"},
		{"phone short", MaskPhone, "+12345", "+12****45"},
		{"phone tiny", MaskPhone, "123", "****"},
		{"identifier email", MaskIdentifier, "bob@example.com", "b***@example.com"},
		{"identifier phone", MaskIdentifier, "+8613812341234", "+86138****1234"},
		{"ipv4", MaskIP, "203.0.113.9", "203.0.113.*"},
		{"ipv4 port", MaskIP, "203.0.113.9:4431", "203.0.113.*"},
		{"ipv4 mapped", MaskIP, "::ffff:203.0.113.9", "203.0.113.*"},
		{"ipv6", MaskIP, "2001:db8:1:2:3:4:5:6", "2001:db8:1::*"},
		{"ipv6 port", MaskIP, "[2001:db8:1::5]:443", "2001:db8:1::*"},
		{"ip garbage", MaskIP, "unknown", "u***n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.fn(tc.in); got != tc.want {
				t.Fatalf("mask(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
