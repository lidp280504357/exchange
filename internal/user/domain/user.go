// Package domain holds user-service's model: profiles and account status
// (requirements §5.4). Identities (email, phone) belong to auth-service.
package domain

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/platform/apperr"
)

// Account statuses (appendix B).
const (
	StatusActive     = "ACTIVE"
	StatusRiskReview = "RISK_REVIEW"
	StatusFrozen     = "FROZEN"
	StatusClosed     = "CLOSED"
)

// Defaults of a new profile.
const (
	DefaultLanguage = "zh-CN"
	DefaultTimezone = "Asia/Shanghai"
)

// Consent documents (§12.5).
const (
	DocumentTerms          = "TERMS"
	DocumentRiskDisclosure = "RISK_DISCLOSURE"
)

// User is a profile.
type User struct {
	ID               string
	Status           string
	Region           string
	Language         string
	Timezone         string
	AntiPhishingCode string
	KYCLevel         int
	Version          int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
	// Username, and when the user last changed it (zero while it is the
	// one drawn); Avatar is nil for the sites' default (design 2026-10-07,
	// avatars and usernames).
	Username          string
	UsernameChangedAt time.Time
	Avatar            *Avatar
}

// Consent is an accepted document version.
type Consent struct {
	Document string
	Version  string
	// AcceptedAt is read back by Consents.
	AcceptedAt time.Time
}

// ErrUserNotFound is returned for unknown user IDs.
var ErrUserNotFound = apperr.NotFound("no such user")

var languageRE = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

// NewUser validates a profile for registration and fills in defaults.
func NewUser(id, region, language, timezone string) (User, error) {
	if _, err := uuid.Parse(id); err != nil {
		return User{}, apperr.Invalid("user_id must be a UUID")
	}
	region = strings.ToUpper(strings.TrimSpace(region))
	if len(region) != 2 || strings.Trim(region, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return User{}, apperr.Invalid("region must be an ISO 3166-1 alpha-2 code")
	}
	if language == "" {
		language = DefaultLanguage
	}
	if len(language) > 16 || !languageRE.MatchString(language) {
		return User{}, apperr.Invalid("language must be a BCP 47 tag")
	}
	if timezone == "" {
		timezone = DefaultTimezone
	}
	if _, err := time.LoadLocation(timezone); err != nil || len(timezone) > 64 {
		return User{}, apperr.Invalid("timezone must be an IANA time zone")
	}
	return User{ID: id, Status: StatusActive, Region: region, Language: language, Timezone: timezone, Username: DrawUsername()}, nil
}
