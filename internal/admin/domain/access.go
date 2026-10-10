package domain

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/skill/exchange/internal/platform/apperr"
)

// ConsoleAccess is how the console lets administrators in (design
// 2026-10-02, N1): whether signing in asks for the authenticator code
// (admin.require_totp), and whether only the addresses of a list reach its
// API (admin.access_restriction).
type ConsoleAccess struct {
	RequireTOTP bool
	Restricted  bool
	Allowlist   []netip.Prefix
	UpdatedBy   string
	UpdatedAt   time.Time
}

// MaxAllowlist bounds the addresses of the restriction's list.
const MaxAllowlist = 50

// Allows reports whether a request from ip reaches the console's API: any
// while the restriction is off; then only one in the list (an address
// that cannot be read never).
func (a ConsoleAccess) Allows(ip netip.Addr) bool {
	if !a.Restricted {
		return true
	}
	if !ip.IsValid() {
		return false
	}
	ip = ip.Unmap()
	for _, p := range a.Allowlist {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// ParseAllowlist reads the restriction's list: each entry an IPv4 or IPv6
// address (that one host) or a CIDR prefix, masked to its network; once
// each, in the order given; no /0 and at most MaxAllowlist.
func ParseAllowlist(in []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(in))
	for _, raw := range in {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		var p netip.Prefix
		if strings.Contains(s, "/") {
			q, err := netip.ParsePrefix(s)
			if err != nil {
				return nil, apperr.Invalid(fmt.Sprintf("%q is neither an IP address nor a CIDR prefix", s))
			}
			p = q.Masked()
		} else {
			a, err := netip.ParseAddr(s)
			if err != nil || a.Zone() != "" {
				return nil, apperr.Invalid(fmt.Sprintf("%q is neither an IP address nor a CIDR prefix", s))
			}
			a = a.Unmap()
			p = netip.PrefixFrom(a, a.BitLen())
		}
		if p.Addr().Is4In6() && p.Bits() >= 96 {
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96) // an IPv4 prefix written as IPv6, as Allows compares
		}
		if p.Bits() == 0 {
			return nil, apperr.Invalid(fmt.Sprintf("%q lets every address in: the restriction would restrict nothing", s))
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	if len(out) > MaxAllowlist {
		return nil, apperr.Invalid(fmt.Sprintf("at most %d addresses", MaxAllowlist))
	}
	return out, nil
}

// AllowlistText is the list as stored and shown: each prefix's text, a
// single host's without its length.
func AllowlistText(list []netip.Prefix) []string {
	out := make([]string, 0, len(list))
	for _, p := range list {
		if p.IsSingleIP() {
			out = append(out, p.Addr().String())
			continue
		}
		out = append(out, p.String())
	}
	return out
}

// The access switches' refusals.
var (
	// ErrTOTPNotBound refuses asking for the code at sign-in before the
	// administrator switching it on and at least one active ADMIN have a
	// bound authenticator: nobody would be left to sign in.
	ErrTOTPNotBound = apperr.New(apperr.KindConflict, "ADMIN_TOTP_NOT_BOUND",
		"bind your authenticator on your account page first; at least one active ADMIN must have one bound")
	// ErrTOTPRequired refuses removing one's authenticator while sign-in
	// asks for its code.
	ErrTOTPRequired = apperr.New(apperr.KindConflict, "ADMIN_TOTP_REQUIRED",
		"sign-in asks for the authenticator code: an authenticator can be replaced, not removed")
	// ErrAccessDenied refuses a request from an address the restriction's
	// list does not have; the list is not told.
	ErrAccessDenied = apperr.New(apperr.KindForbidden, "ADMIN_ACCESS_DENIED", "the console does not answer this address")
	// ErrAccessSelfLockout refuses a restriction that would leave out the
	// address of the administrator setting it (the detail ip).
	ErrAccessSelfLockout = apperr.New(apperr.KindConflict, "ADMIN_ACCESS_SELF_LOCKOUT",
		"your address is not in the list: the restriction would lock you out")
)
