package main

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

// A custodian's gateway takes a key long enough not to be guessed (review
// B3, B7) and the addresses its callbacks come from, if any are known
// (the real gateway publishes none: empty takes any, 2026-10-03).
func TestCustodySettings(t *testing.T) {
	base := func() settings {
		return settings{
			MaxFeeGwei: decimal.NewFromInt(100), UdunURL: "https://gateway", UdunMerchant: "m",
			UdunKey: strings.Repeat("a1", 16), UdunCallback: "https://astras.vip/v1/wallet/callbacks/udun",
		}
	}
	errOf := func(s settings) string {
		if err := s.Validate(); err != nil {
			return err.Error()
		}
		return ""
	}
	s := base()
	if msg := errOf(s); strings.Contains(msg, "UDUN_") {
		t.Fatalf("no allowed addresses: any, %s", msg)
	}
	s.UdunCallbackIPs = "203.0.113.7, 198.51.100.0/24"
	if msg := errOf(s); strings.Contains(msg, "UDUN_") {
		t.Fatalf("allowed addresses set: %s", msg)
	}
	s.UdunKey = "short-key"
	if msg := errOf(s); !strings.Contains(msg, "UDUN_API_KEY must have at least 32") {
		t.Fatalf("short key: %s", msg)
	}
	s.UdunCallbackIPs = "not-an-address"
	if msg := errOf(s); !strings.Contains(msg, "UDUN_CALLBACK_ALLOWED_IPS") {
		t.Fatalf("a bad address: %s", msg)
	}
	// Without a gateway nothing is asked.
	if msg := errOf(settings{MaxFeeGwei: decimal.NewFromInt(100)}); strings.Contains(msg, "UDUN_") {
		t.Fatalf("no gateway: %s", msg)
	}
}
