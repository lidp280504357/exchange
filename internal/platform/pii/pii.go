// Package pii masks personal data before it reaches logs, events or
// analytics (requirements §12.1): a***@x.com, +86138****1234, 203.0.113.*.
package pii

import (
	"fmt"
	"net/netip"
	"strings"
)

const stars = "***"

// MaskEmail keeps the first character of the local part and the domain.
func MaskEmail(email string) string {
	local, domain, ok := strings.Cut(strings.TrimSpace(email), "@")
	if !ok || local == "" || domain == "" {
		return maskMiddle(email)
	}
	return string([]rune(local)[:1]) + stars + "@" + domain
}

// MaskPhone keeps the country code and leading digits plus the last four
// digits of an E.164 number: +8613812341234 becomes +86138****1234.
func MaskPhone(phone string) string {
	p := strings.TrimSpace(phone)
	switch n := len(p); {
	case n >= 11:
		return p[:6] + "****" + p[n-4:]
	case n >= 6:
		return p[:3] + "****" + p[n-2:]
	default:
		return "****"
	}
}

// MaskIdentifier masks an identity that is either an email address or a
// phone number.
func MaskIdentifier(id string) string {
	if strings.Contains(id, "@") {
		return MaskEmail(id)
	}
	return MaskPhone(id)
}

// MaskIP hides the host part: the last IPv4 octet, or everything after the
// /48 prefix of an IPv6 address. It accepts "ip" and "ip:port".
func MaskIP(s string) string {
	s = strings.TrimSpace(s)
	addr, err := netip.ParseAddr(s)
	if err != nil {
		ap, perr := netip.ParseAddrPort(s)
		if perr != nil {
			return maskMiddle(s)
		}
		addr = ap.Addr()
	}
	addr = addr.Unmap()
	if addr.Is4() {
		b := addr.As4()
		return fmt.Sprintf("%d.%d.%d.*", b[0], b[1], b[2])
	}
	prefix, err := addr.WithZone("").Prefix(48)
	if err != nil {
		return maskMiddle(s)
	}
	return strings.TrimSuffix(prefix.Addr().String(), "::") + "::*"
}

// maskMiddle is the fallback for values of unknown shape.
func maskMiddle(s string) string {
	r := []rune(s)
	if len(r) <= 2 {
		return stars
	}
	return string(r[0]) + stars + string(r[len(r)-1])
}
