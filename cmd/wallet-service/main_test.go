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
	// The stand-in's second merchant (ADR-0017) is checked as one, under
	// its own names.
	s = base()
	s.UdunMockURL, s.UdunMockMerchant, s.UdunMockKey = "http://udun-mock:8097", "m", "short-key"
	if msg := errOf(s); !strings.Contains(msg, "UDUNMOCK_MERCHANT_ID, UDUNMOCK_API_KEY and UDUNMOCK_CALLBACK_URL") ||
		!strings.Contains(msg, "UDUNMOCK_API_KEY must have at least 32") || strings.Contains(msg, "UDUN_API_KEY") {
		t.Fatalf("the stand-in's merchant: %s", msg)
	}
	s.UdunMockKey, s.UdunMockCallback = strings.Repeat("b2", 16), "http://api-gateway:8080/v1/wallet/callbacks/udunmock"
	s.UdunMockCallbackIPs = "172.18.0.0/16"
	if msg := errOf(s); strings.Contains(msg, "UDUN") {
		t.Fatalf("both configured: %s", msg)
	}
	if got := s.custodians(); len(got) != 2 || got[0].Provider != "UDUN" || got[1].Provider != "UDUNMOCK" || got[1].URL != s.UdunMockURL {
		t.Fatalf("custodians %+v", got)
	}
	if from, err := s.custodians()[1].callbackFrom(); err != nil || len(from) != 1 || from[0].String() != "172.18.0.0/16" {
		t.Fatalf("the stand-in's allow list %v %v", from, err)
	}
}
