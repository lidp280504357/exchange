// Package domain holds auth-service's rules: identity normalization, OTP
// challenges and tickets, passwords, sessions and tokens (requirements
// §5.2, §6). It depends on nothing outside the standard library, the
// platform error model and phone-number parsing.
package domain

import (
	"strings"
	"time"

	"github.com/nyaruka/phonenumbers"

	"github.com/skill/exchange/internal/platform/apperr"
)

// Channel is how a code reaches the user; it doubles as the identity kind.
type Channel string

// Channels.
const (
	ChannelEmail Channel = "EMAIL"
	ChannelSMS   Channel = "SMS"
	// ChannelTOTP proves a step-up with an authenticator app; it is not an
	// identity.
	ChannelTOTP Channel = "TOTP"
)

// Kind returns the identity kind stored for the channel.
func (c Channel) Kind() string {
	if c == ChannelSMS {
		return "PHONE"
	}
	return "EMAIL"
}

// ChannelFor returns the channel that reaches an identity kind.
func ChannelFor(kind string) Channel {
	if kind == "PHONE" {
		return ChannelSMS
	}
	return ChannelEmail
}

// ParseChannel accepts EMAIL or SMS.
func ParseChannel(s string) (Channel, error) {
	switch c := Channel(strings.ToUpper(strings.TrimSpace(s))); c {
	case ChannelEmail, ChannelSMS:
		return c, nil
	default:
		return "", apperr.Invalid("channel must be EMAIL or SMS")
	}
}

// Identifier is a normalized email address or E.164 phone number.
type Identifier struct {
	Channel Channel
	Value   string
	// Region is the ISO 3166-1 alpha-2 region of a phone number.
	Region string
}

// ErrBadIdentifier reports an unusable email address or phone number.
var ErrBadIdentifier = apperr.Invalid("identifier is not a valid email address or phone number")

// ParseIdentifier normalizes raw for channel (§5.2): emails are trimmed and
// lower-cased; phone numbers must carry their country code and become
// E.164.
func ParseIdentifier(ch Channel, raw string) (Identifier, error) {
	raw = strings.TrimSpace(raw)
	switch ch {
	case ChannelEmail:
		v := strings.ToLower(raw)
		local, domain, ok := strings.Cut(v, "@")
		if !ok || local == "" || !strings.Contains(domain, ".") || strings.ContainsAny(v, " \t<>()[],;:\"") ||
			strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") || len(v) > 254 || strings.Contains(domain, "@") {
			return Identifier{}, ErrBadIdentifier
		}
		return Identifier{Channel: ch, Value: v}, nil
	case ChannelSMS:
		if !strings.HasPrefix(raw, "+") {
			return Identifier{}, apperr.Invalid("phone numbers need the country code, e.g. +8613812341234")
		}
		num, err := phonenumbers.Parse(raw, "")
		if err != nil || !phonenumbers.IsValidNumber(num) {
			return Identifier{}, ErrBadIdentifier
		}
		return Identifier{
			Channel: ch,
			Value:   phonenumbers.Format(num, phonenumbers.E164),
			Region:  phonenumbers.GetRegionCodeForNumber(num),
		}, nil
	default:
		return Identifier{}, apperr.Invalid("channel must be EMAIL or SMS")
	}
}

// Identity is an identifier bound to a user.
type Identity struct {
	ID     string
	UserID string
	Kind   string
	Value  string
	// VerifiedAt and CreatedAt are read back by ByUser.
	VerifiedAt time.Time
	CreatedAt  time.Time
}
