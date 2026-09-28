// Package domain holds the risk rules (requirements §5.13): declarative
// rules, evaluated against what an event says about a user, that add up
// to a score and an action.
package domain

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Action is what the rules ask for, ordered by severity.
type Action int

// Actions (requirements §5.13).
const (
	ActionNone   Action = iota + 1 // recorded only
	ActionStepUp                   // extra verification before the next sensitive operation
	ActionReview                   // the account waits in RISK_REVIEW for a person
	ActionReject                   // the operation is refused (synchronous checks)
)

var actionNames = map[Action]string{
	ActionNone: "NONE", ActionStepUp: "STEP_UP", ActionReview: "REVIEW", ActionReject: "REJECT",
}

func (a Action) String() string {
	if s, ok := actionNames[a]; ok {
		return s
	}
	return fmt.Sprintf("Action(%d)", int(a))
}

// ParseAction reads NONE, STEP_UP, REVIEW or REJECT.
func ParseAction(s string) (Action, error) {
	for a, name := range actionNames {
		if name == s {
			return a, nil
		}
	}
	return 0, fmt.Errorf("unknown action %q (want NONE, STEP_UP, REVIEW or REJECT)", s)
}

// MarshalText writes the action's name.
func (a Action) MarshalText() ([]byte, error) {
	if _, ok := actionNames[a]; !ok {
		return nil, fmt.Errorf("invalid action %d", int(a))
	}
	return []byte(a.String()), nil
}

// UnmarshalText reads an action's name.
func (a *Action) UnmarshalText(b []byte) error {
	v, err := ParseAction(string(b))
	if err != nil {
		return err
	}
	*a = v
	return nil
}

// Event types the rules can watch.
const (
	EventRegistered  = "auth.UserRegistered"
	EventLogin       = "auth.LoginSucceeded"
	EventLoginFailed = "auth.LoginFailed"
)

// Rule kinds.
const (
	// KindVelocity matches when Threshold or more events of the rule's
	// type share a Key value within Window, the current one included.
	KindVelocity = "velocity"
	// KindNewDevice matches a login from a device the user had not used,
	// once they have used another one (their first device is not news).
	KindNewDevice = "new_device"
	// KindRegion matches when the user's region is one of Regions.
	KindRegion = "region"
)

// Velocity keys.
const (
	KeyUser    = "user"
	KeyDevice  = "device"
	KeyNetwork = "network" // the masked IP: its /24 (IPv4) or /48 (IPv6)
)

// Rule is one declarative rule.
type Rule struct {
	ID        string   `json:"id"`
	Event     string   `json:"event"`
	Kind      string   `json:"kind"`
	Key       string   `json:"key,omitempty"`
	Window    Duration `json:"window,omitempty"`
	Threshold int      `json:"threshold,omitempty"`
	Regions   []string `json:"regions,omitempty"`
	Score     int      `json:"score"`
	Action    Action   `json:"action"`
}

// Duration is a time.Duration written like "15m" or "24h".
type Duration time.Duration

// String writes whole hours as "24h" and whole minutes as "15m".
func (d Duration) String() string {
	switch v := time.Duration(d); {
	case v > 0 && v%time.Hour == 0:
		return fmt.Sprintf("%dh", v/time.Hour)
	case v > 0 && v%time.Minute == 0:
		return fmt.Sprintf("%dm", v/time.Minute)
	default:
		return v.String()
	}
}

// MarshalText writes the duration like String.
func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// UnmarshalText reads a duration such as "15m".
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

var (
	ruleID     = regexp.MustCompile(`^[a-z][a-z0-9_]{2,63}$`)
	regionCode = regexp.MustCompile(`^[A-Z]{2}$`)
	events     = []string{EventRegistered, EventLogin, EventLoginFailed}
)

// Validate reports what is wrong with the rule.
func (r Rule) Validate() error {
	var errs []error
	if !ruleID.MatchString(r.ID) {
		errs = append(errs, fmt.Errorf("id %q must be lower-case letters, digits and underscores", r.ID))
	}
	if !slices.Contains(events, r.Event) {
		errs = append(errs, fmt.Errorf("event %q is not one of %s", r.Event, strings.Join(events, ", ")))
	}
	switch r.Kind {
	case KindVelocity:
		if !slices.Contains([]string{KeyUser, KeyDevice, KeyNetwork}, r.Key) {
			errs = append(errs, fmt.Errorf("key %q must be user, device or network", r.Key))
		}
		if r.Window < Duration(time.Minute) || r.Window > Duration(30*24*time.Hour) {
			errs = append(errs, errors.New("window must be between 1m and 720h"))
		}
		if r.Threshold < 2 {
			errs = append(errs, errors.New("threshold must be at least 2"))
		}
	case KindNewDevice:
		if r.Event != EventLogin {
			errs = append(errs, fmt.Errorf("new_device watches %s", EventLogin))
		}
	case KindRegion:
		if len(r.Regions) == 0 {
			errs = append(errs, errors.New("regions must list at least one ISO 3166-1 alpha-2 code"))
		}
		for _, c := range r.Regions {
			if !regionCode.MatchString(c) {
				errs = append(errs, fmt.Errorf("region %q must be an upper-case ISO 3166-1 alpha-2 code", c))
			}
		}
	default:
		errs = append(errs, fmt.Errorf("kind %q must be velocity, new_device or region", r.Kind))
	}
	if r.Score < 0 || r.Score > 100 {
		errs = append(errs, errors.New("score must be between 0 and 100"))
	}
	if _, ok := actionNames[r.Action]; !ok {
		errs = append(errs, errors.New("action must be NONE, STEP_UP, REVIEW or REJECT"))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("rule %q: %w", r.ID, err)
	}
	return nil
}

// ParseRules reads and validates a JSON array of rules; unknown fields and
// repeated IDs are errors.
func ParseRules(b []byte) ([]Rule, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var rules []Rule
	if err := dec.Decode(&rules); err != nil {
		return nil, fmt.Errorf("risk rules: %w", err)
	}
	seen := map[string]bool{}
	var errs []error
	for _, r := range rules {
		if seen[r.ID] {
			errs = append(errs, fmt.Errorf("rule %q is defined twice", r.ID))
		}
		seen[r.ID] = true
		errs = append(errs, r.Validate())
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("risk rules: %w", err)
	}
	return rules, nil
}

//go:embed default_rules.json
var defaultRules []byte

// DefaultRules are the rules used unless RISK_RULES_FILE names others.
func DefaultRules() []Rule {
	rules, err := ParseRules(defaultRules)
	if err != nil {
		panic(err) // embedded at build time and covered by a test
	}
	return rules
}

// DefaultRulesJSON is the embedded rule file, for operators to start from.
func DefaultRulesJSON() []byte { return slices.Clone(defaultRules) }

// Retention is how long velocity events must be kept for rules to see
// them: the longest window.
func Retention(rules []Rule) time.Duration {
	var longest time.Duration
	for _, r := range rules {
		longest = max(longest, time.Duration(r.Window))
	}
	return longest
}
