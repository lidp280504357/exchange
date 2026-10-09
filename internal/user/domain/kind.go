package domain

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/skill/exchange/internal/platform/apperr"
)

// Account kinds (L0, the user 2026-10-09/10): what the console shows and
// filters users by, humans by default. A display attribute only: no
// trading, fee, risk, notification or bot code may read it - the platform
// coin's bots are a production feature and trade as any account - and the
// public API does not show it.
const (
	KindHuman  = "HUMAN"  // a person: every sign-up
	KindBot    = "BOT"    // the simulated market's bots
	KindTest   = "TEST"   // the end-to-end scripts' accounts
	KindSystem = "SYSTEM" // HOUSE and the like
)

// Kinds lists the kinds in the order the console shows them.
var Kinds = []string{KindHuman, KindBot, KindTest, KindSystem}

// ParseKind takes a kind whatever its case.
func ParseKind(s string) (string, error) {
	k := strings.ToUpper(strings.TrimSpace(s))
	if !slices.Contains(Kinds, k) {
		return "", apperr.Invalid(fmt.Sprintf("unknown kind %q: HUMAN, BOT, TEST or SYSTEM", s))
	}
	return k, nil
}

// ParseKinds takes a list of kinds (a filter), each once.
func ParseKinds(in []string) ([]string, error) {
	var out []string
	for _, s := range in {
		k, err := ParseKind(s)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out, nil
}

// KindChange is a change of an account's kind, kept with who made it and
// why.
type KindChange struct {
	UserID string
	From   string
	To     string
	// Actor is an admin's email, "cli:<os user>" or a script's name.
	Actor  string
	Reason string
	At     time.Time
}

// NewKindChange validates a requested change of userID's kind: a known
// kind, an actor, and a reason of up to 200 characters.
func NewKindChange(userID, to, actor, reason string) (KindChange, error) {
	k, err := ParseKind(to)
	if err != nil {
		return KindChange{}, err
	}
	if strings.TrimSpace(actor) == "" {
		return KindChange{}, apperr.Invalid("the actor is required")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > 200 {
		return KindChange{}, apperr.Invalid("a reason of 1 to 200 characters is required")
	}
	return KindChange{UserID: userID, To: k, Actor: strings.TrimSpace(actor), Reason: reason}, nil
}
