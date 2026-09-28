package domain

import (
	"fmt"
	"slices"
	"time"
)

// Observation is what the rules see of one event.
type Observation struct {
	EventID   string
	EventType string
	// At is when the event happened; velocity windows end there, so a
	// backlog or a replay is judged like the live event.
	At       time.Time
	UserID   string
	DeviceID string
	Network  string // masked IP
	Region   string
	// NewDevice is set by the caller: a login from a device the user had
	// not used, after they had used another.
	NewDevice bool
}

// Value returns the observation's value for a velocity key, "" when the
// event does not carry it.
func (o Observation) Value(key string) string {
	switch key {
	case KeyUser:
		return o.UserID
	case KeyDevice:
		return o.DeviceID
	case KeyNetwork:
		return o.Network
	}
	return ""
}

// Tally records the observation for a velocity rule under value and
// returns how many events the rule has recorded for value within its
// window, this one included. Recording the same event twice counts once.
type Tally func(r Rule, value string) (int, error)

// Hit is one rule that matched.
type Hit struct {
	Rule   string `json:"rule"`
	Score  int    `json:"score"`
	Detail string `json:"detail"`
}

// Assessment is the outcome of the rules for one observation.
type Assessment struct {
	Score  int
	Action Action
	Hits   []Hit
}

// Evaluate applies the rules that watch the observation's event type.
// The score is the sum of the hits' scores, at most 100; the action is the
// most severe hit's, ActionNone when nothing matched.
func Evaluate(rules []Rule, o Observation, tally Tally) (Assessment, error) {
	a := Assessment{Action: ActionNone}
	for _, r := range rules {
		if r.Event != o.EventType {
			continue
		}
		var detail string
		switch r.Kind {
		case KindVelocity:
			v := o.Value(r.Key)
			if v == "" {
				continue
			}
			n, err := tally(r, v)
			if err != nil {
				return Assessment{}, fmt.Errorf("rule %s: %w", r.ID, err)
			}
			if n < r.Threshold {
				continue
			}
			detail = fmt.Sprintf("%d %s from this %s within %s", n, noun(r.Event), r.Key, r.Window)
		case KindNewDevice:
			if !o.NewDevice {
				continue
			}
			detail = "login from a device not used before"
		case KindRegion:
			if !slices.Contains(r.Regions, o.Region) {
				continue
			}
			detail = "region " + o.Region + " is listed"
		default:
			continue
		}
		a.Hits = append(a.Hits, Hit{Rule: r.ID, Score: r.Score, Detail: detail})
		a.Score = min(100, a.Score+r.Score)
		a.Action = max(a.Action, r.Action)
	}
	return a, nil
}

func noun(eventType string) string {
	switch eventType {
	case EventRegistered:
		return "registrations"
	case EventLogin:
		return "logins"
	case EventLoginFailed:
		return "failed logins"
	}
	return "events"
}

// Record is a stored assessment.
type Record struct {
	ID              string
	UserID          string
	SourceEventID   string
	SourceEventType string
	Score           int
	Action          Action
	Hits            []Hit
	// Enforced is true when the action was carried out (risk.enforce).
	Enforced  bool
	CreatedAt time.Time
}
