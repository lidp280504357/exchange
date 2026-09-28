package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// ErrStatusTransition rejects a change the state machine does not allow.
var ErrStatusTransition = apperr.New(apperr.KindConflict, "USER_STATUS_TRANSITION_INVALID", "this account status change is not allowed")

// transitions is appendix B: ACTIVE <-> RISK_REVIEW; ACTIVE/RISK_REVIEW ->
// FROZEN; FROZEN -> ACTIVE; ACTIVE -> CLOSED.
var transitions = map[string][]string{
	StatusActive:     {StatusRiskReview, StatusFrozen, StatusClosed},
	StatusRiskReview: {StatusActive, StatusFrozen},
	StatusFrozen:     {StatusActive},
}

// CheckTransition reports whether an account may move from one status to
// another.
func CheckTransition(from, to string) error {
	for _, t := range transitions[from] {
		if t == to {
			return nil
		}
	}
	return ErrStatusTransition.WithDetail("from", from).WithDetail("to", to)
}

// ValidStatus reports whether s is a known status.
func ValidStatus(s string) bool {
	switch s {
	case StatusActive, StatusRiskReview, StatusFrozen, StatusClosed:
		return true
	}
	return false
}

var reasonRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,63}$`)

// StatusChange is one status transition with its reason code and actor
// (§5.4: every change carries both and an audit event).
type StatusChange struct {
	UserID string
	From   string
	To     string
	// Reason is an upper-case code, e.g. SUSPICIOUS_LOGIN, USER_REQUEST.
	Reason string
	// Actor is an admin ID, "cli:<os user>" or "system:<service>".
	Actor string
	// Note is optional free text for the audit record.
	Note string
	At   time.Time
}

// NewStatusChange validates a requested change of userID's status.
func NewStatusChange(userID, to, reason, actor, note string) (StatusChange, error) {
	to = strings.ToUpper(strings.TrimSpace(to))
	if !ValidStatus(to) {
		return StatusChange{}, apperr.Invalid(fmt.Sprintf("unknown status %q", to))
	}
	if !reasonRE.MatchString(reason) {
		return StatusChange{}, apperr.Invalid("the reason must be an upper-case code such as SUSPICIOUS_LOGIN")
	}
	if strings.TrimSpace(actor) == "" {
		return StatusChange{}, apperr.Invalid("the actor is required")
	}
	if len(note) > 500 {
		return StatusChange{}, apperr.Invalid("the note is too long")
	}
	return StatusChange{UserID: userID, To: to, Reason: reason, Actor: actor, Note: note}, nil
}
