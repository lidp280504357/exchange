package domain

import (
	"net/netip"
	"slices"
	"strings"
	"testing"
)

// The restriction's list (N1): addresses or CIDR prefixes of either
// family, masked and once each; nothing that lets every address in; at
// most MaxAllowlist.
func TestParseAllowlist(t *testing.T) {
	got, err := ParseAllowlist([]string{
		" 203.0.113.7 ", "198.51.100.77/24", "2001:db8:1:2::abcd/64", "2001:db8::1", "", "203.0.113.7/32", "::ffff:192.0.2.0/120",
	})
	want := []string{"203.0.113.7", "198.51.100.0/24", "2001:db8:1:2::/64", "2001:db8::1", "192.0.2.0/24"}
	if err != nil || !slices.Equal(AllowlistText(got), want) {
		t.Fatalf("parsed %v %v, want %v", AllowlistText(got), err, want)
	}
	for _, bad := range []string{"0.0.0.0/0", "::/0", "203.0.113", "fe80::1%eth0", "203.0.113.0/33", "example.com"} {
		if _, err := ParseAllowlist([]string{bad}); err == nil {
			t.Fatalf("%q taken", bad)
		}
	}
	many := make([]string, MaxAllowlist+1)
	for i := range many {
		many[i] = netip.AddrFrom4([4]byte{10, 0, byte(i / 256), byte(i % 256)}).String()
	}
	if _, err := ParseAllowlist(many); err == nil || !strings.Contains(err.Error(), "at most 50") {
		t.Fatalf("51 addresses: %v", err)
	}
	if _, err := ParseAllowlist(many[:MaxAllowlist]); err != nil {
		t.Fatalf("50 addresses: %v", err)
	}
	// What is stored reads back the same.
	again, err := ParseAllowlist(AllowlistText(got))
	if err != nil || !slices.Equal(again, got) {
		t.Fatalf("read back %v %v", again, err)
	}
}

// Off, every address reaches the console; on, the list's (IPv4 written as
// IPv6 too) and no address that cannot be read.
func TestConsoleAccessAllows(t *testing.T) {
	list, err := ParseAllowlist([]string{"203.0.113.0/24", "2001:db8:1:2::/64"})
	if err != nil {
		t.Fatal(err)
	}
	a := ConsoleAccess{Allowlist: list}
	if !a.Allows(netip.MustParseAddr("192.0.2.1")) || !a.Allows(netip.Addr{}) {
		t.Fatal("off, every address")
	}
	a.Restricted = true
	for ip, want := range map[string]bool{
		"203.0.113.250": true, "::ffff:203.0.113.1": true, "2001:db8:1:2:ffff::1": true,
		"203.0.114.1": false, "2001:db8:1:3::1": false, "::1": false,
	} {
		if a.Allows(netip.MustParseAddr(ip)) != want {
			t.Fatalf("%s: want %t", ip, want)
		}
	}
	if a.Allows(netip.Addr{}) {
		t.Fatal("on, an address that cannot be read")
	}
}
