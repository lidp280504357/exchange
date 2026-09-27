package httpx

import (
	"net/http"
	"strings"
	"time"
)

// Headers the gateway sets after validating the access token. Services
// trust them because only the gateway can reach their REST ports; the
// gateway strips any client-supplied copies.
const (
	HeaderUserID    = "X-User-Id"
	HeaderSessionID = "X-Session-Id"
	HeaderScope     = "X-Auth-Scope"
)

// UserID returns the authenticated user set by the gateway, or "".
func UserID(r *http.Request) string { return r.Header.Get(HeaderUserID) }

// SessionID returns the authenticated session set by the gateway, or "".
func SessionID(r *http.Request) string { return r.Header.Get(HeaderSessionID) }

// TimeLayout is RFC 3339 in UTC with millisecond precision (§7.1).
const TimeLayout = "2006-01-02T15:04:05.000Z07:00"

// FormatTime renders t in TimeLayout.
func FormatTime(t time.Time) string { return t.UTC().Format(TimeLayout) }

// Language returns the preferred language: explicit wins, then the first
// Accept-Language tag, then zh-CN.
func Language(r *http.Request, explicit string) string {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		return explicit
	}
	if al := r.Header.Get("Accept-Language"); al != "" {
		tag, _, _ := strings.Cut(al, ",")
		tag, _, _ = strings.Cut(tag, ";")
		if tag = strings.TrimSpace(tag); tag != "" && tag != "*" {
			return tag
		}
	}
	return "zh-CN"
}
