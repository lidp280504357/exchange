package domain

import (
	"crypto/rand"
	"regexp"
	"strings"
	"time"

	"github.com/skill/exchange/internal/platform/apperr"
)

// Usernames (design 2026-10-07, avatars and usernames §1.1): what the
// sites show and call the user by. Drawn at sign-up (user_ and 8 lowercase
// letters or digits); the user may change it once in UsernameCooldown, to
// 3 to 20 letters, digits or underscores not starting with an underscore,
// unique whatever the case. Signing in stays with the email or phone.

// UsernameCooldown is how long a changed username stays.
const UsernameCooldown = 7 * 24 * time.Hour

var usernameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_]{2,19}$`)

// reservedWhole are names no user may take; reservedPart may not appear
// anywhere in one (they would pass for the platform or its staff).
var (
	reservedWhole = map[string]bool{
		"house": true, "root": true, "system": true, "null": true, "undefined": true, "api": true, "www": true, "help": true,
		"service": true, "security": true, "platform": true, "exchange": true, "operator": true, "bot": true,
	}
	reservedPart = []string{"admin", "astras", "support", "official", "staff", "moderator"}
)

// Errors of usernames (appendix C).
var (
	ErrUsernameInvalid = apperr.New(apperr.KindInvalid, "USER_USERNAME_INVALID",
		"a username has 3 to 20 letters, digits or underscores, does not start with an underscore and is not reserved")
	ErrUsernameTaken    = apperr.New(apperr.KindConflict, "USER_USERNAME_TAKEN", "the username is taken")
	ErrUsernameCooldown = apperr.New(apperr.KindConflict, "USER_USERNAME_COOLDOWN", "the username may change once in 7 days")
)

// LooksLikeUsername tells whether name has a username's form (reserved
// ones included): only such a name can be one (B167, the lookup).
func LooksLikeUsername(name string) bool { return usernameRE.MatchString(name) }

// CheckUsername refuses a name of the wrong form or a reserved one.
func CheckUsername(name string) error {
	if !usernameRE.MatchString(name) {
		return ErrUsernameInvalid
	}
	lower := strings.ToLower(name)
	if reservedWhole[lower] {
		return ErrUsernameInvalid
	}
	for _, part := range reservedPart {
		if strings.Contains(lower, part) {
			return ErrUsernameInvalid
		}
	}
	return nil
}

const drawAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// DrawUsername is a sign-up's username: user_ and 8 lowercase letters or
// digits (the caller draws again on a clash).
func DrawUsername() string {
	out := make([]byte, 0, 13)
	out = append(out, "user_"...)
	var b [16]byte
	for len(out) < 13 {
		_, _ = rand.Read(b[:]) // crypto/rand.Read does not fail
		for _, c := range b {
			// 252 is the largest multiple of 36 within a byte: no bias.
			if c < 252 && len(out) < 13 {
				out = append(out, drawAlphabet[int(c)%len(drawAlphabet)])
			}
		}
	}
	return string(out)
}

// NextUsernameChange is when u may change the username again; zero when it
// may now (never changed, or the wait is over).
func (u User) NextUsernameChange(now time.Time) time.Time {
	if u.UsernameChangedAt.IsZero() {
		return time.Time{}
	}
	if next := u.UsernameChangedAt.Add(UsernameCooldown); next.After(now) {
		return next
	}
	return time.Time{}
}
