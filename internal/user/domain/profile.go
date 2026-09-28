package domain

import (
	"regexp"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

var antiPhishingRE = regexp.MustCompile(`^[A-Za-z0-9]{4,20}$`)

// ProfilePatch is a PATCH /v1/user/profile body; nil fields stay.
type ProfilePatch struct {
	Language         *string
	Timezone         *string
	AntiPhishingCode *string
}

// SensitiveChange reports whether the patch needs a step-up: the
// anti-phishing code protects every mail we send.
func (p ProfilePatch) SensitiveChange() bool { return p.AntiPhishingCode != nil }

// Apply validates the patch and applies it to u, returning the names of
// the fields that changed.
func (p ProfilePatch) Apply(u *User) ([]string, error) {
	var changed []string
	if p.Language != nil && *p.Language != u.Language {
		if len(*p.Language) > 16 || !languageRE.MatchString(*p.Language) {
			return nil, apperr.Invalid("language must be a BCP 47 tag")
		}
		u.Language = *p.Language
		changed = append(changed, "language")
	}
	if p.Timezone != nil && *p.Timezone != u.Timezone {
		if _, err := time.LoadLocation(*p.Timezone); err != nil || len(*p.Timezone) > 64 || *p.Timezone == "" {
			return nil, apperr.Invalid("timezone must be an IANA time zone")
		}
		u.Timezone = *p.Timezone
		changed = append(changed, "timezone")
	}
	if p.AntiPhishingCode != nil && *p.AntiPhishingCode != u.AntiPhishingCode {
		if *p.AntiPhishingCode != "" && !antiPhishingRE.MatchString(*p.AntiPhishingCode) {
			return nil, apperr.Invalid("the anti-phishing code has 4 to 20 letters or digits")
		}
		u.AntiPhishingCode = *p.AntiPhishingCode
		changed = append(changed, "anti_phishing_code")
	}
	return changed, nil
}
