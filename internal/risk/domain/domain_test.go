package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/skill/exchange/internal/risk/domain"
)

func TestDefaultRulesAreValid(t *testing.T) {
	rules := domain.DefaultRules()
	if len(rules) == 0 {
		t.Fatal("no default rules")
	}
	if got := domain.Retention(rules); got != 24*time.Hour {
		t.Fatalf("retention %v, want the longest window (24h)", got)
	}
}

func TestParseRulesRejectsMistakes(t *testing.T) {
	for name, tc := range map[string]struct{ json, want string }{
		"unknown field":     {`[{"id":"a_rule","event":"auth.LoginSucceeded","kind":"new_device","score":1,"action":"NONE","weight":2}]`, "unknown field"},
		"duplicate id":      {`[{"id":"a_rule","event":"auth.LoginSucceeded","kind":"new_device","score":1,"action":"NONE"},{"id":"a_rule","event":"auth.LoginSucceeded","kind":"new_device","score":1,"action":"NONE"}]`, "defined twice"},
		"bad id":            {`[{"id":"Rule","event":"auth.LoginSucceeded","kind":"new_device","score":1,"action":"NONE"}]`, "lower-case"},
		"unknown event":     {`[{"id":"a_rule","event":"auth.Other","kind":"new_device","score":1,"action":"NONE"}]`, "is not one of"},
		"velocity key":      {`[{"id":"a_rule","event":"auth.UserRegistered","kind":"velocity","key":"email","window":"1h","threshold":3,"score":1,"action":"NONE"}]`, "key"},
		"velocity window":   {`[{"id":"a_rule","event":"auth.UserRegistered","kind":"velocity","key":"device","window":"10s","threshold":3,"score":1,"action":"NONE"}]`, "window"},
		"velocity once":     {`[{"id":"a_rule","event":"auth.UserRegistered","kind":"velocity","key":"device","window":"1h","threshold":1,"score":1,"action":"NONE"}]`, "threshold"},
		"new device event":  {`[{"id":"a_rule","event":"auth.UserRegistered","kind":"new_device","score":1,"action":"NONE"}]`, "new_device watches"},
		"empty regions":     {`[{"id":"a_rule","event":"auth.UserRegistered","kind":"region","score":1,"action":"NONE"}]`, "regions must list"},
		"lower-case region": {`[{"id":"a_rule","event":"auth.UserRegistered","kind":"region","regions":["sg"],"score":1,"action":"NONE"}]`, "upper-case"},
		"score":             {`[{"id":"a_rule","event":"auth.LoginSucceeded","kind":"new_device","score":101,"action":"NONE"}]`, "score"},
		"action":            {`[{"id":"a_rule","event":"auth.LoginSucceeded","kind":"new_device","score":1,"action":"BAN"}]`, "unknown action"},
		"kind":              {`[{"id":"a_rule","event":"auth.LoginSucceeded","kind":"ml","score":1,"action":"NONE"}]`, "kind"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := domain.ParseRules([]byte(tc.json))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
}

// counts is a Tally backed by fixed counts per rule and value.
func counts(m map[string]int) domain.Tally {
	return func(r domain.Rule, value string) (int, error) { return m[r.ID+"/"+value], nil }
}

func TestVelocityRulesMatchAtTheirThreshold(t *testing.T) {
	rules := domain.DefaultRules()
	o := domain.Observation{EventType: domain.EventRegistered, UserID: "u", DeviceID: "d1", Network: "203.0.113.*"}

	a, err := domain.Evaluate(rules, o, counts(map[string]int{"registration_burst_device/d1": 2}))
	if err != nil || len(a.Hits) != 0 || a.Action != domain.ActionNone || a.Score != 0 {
		t.Fatalf("below the threshold: %+v %v", a, err)
	}
	a, err = domain.Evaluate(rules, o, counts(map[string]int{"registration_burst_device/d1": 3}))
	if err != nil || len(a.Hits) != 1 || a.Action != domain.ActionReview || a.Score != 60 {
		t.Fatalf("at the threshold: %+v %v", a, err)
	}
	if a.Hits[0].Detail != "3 registrations from this device within 24h" {
		t.Fatalf("detail %q", a.Hits[0].Detail)
	}
}

func TestScoresAddUpAndTheMostSevereActionWins(t *testing.T) {
	rules := append(domain.DefaultRules(), domain.Rule{
		ID: "watched_region", Event: domain.EventRegistered, Kind: domain.KindRegion,
		Regions: []string{"AQ"}, Score: 70, Action: domain.ActionStepUp,
	})
	o := domain.Observation{EventType: domain.EventRegistered, UserID: "u", DeviceID: "d1", Network: "n", Region: "AQ"}
	a, err := domain.Evaluate(rules, o, counts(map[string]int{"registration_burst_device/d1": 5, "registration_burst_network/n": 30}))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Hits) != 3 || a.Score != 100 || a.Action != domain.ActionReview {
		t.Fatalf("got %+v, want three hits capped at 100 with REVIEW", a)
	}
}

func TestEventsWithoutAValueAreNotCounted(t *testing.T) {
	called := false
	tally := func(domain.Rule, string) (int, error) { called = true; return 100, nil }
	o := domain.Observation{EventType: domain.EventLoginFailed} // unknown account: no user
	a, err := domain.Evaluate(domain.DefaultRules(), o, tally)
	if err != nil || called || len(a.Hits) != 0 {
		t.Fatalf("got %+v, tally called %v, err %v", a, called, err)
	}
}

func TestNewDeviceOnlyForLogins(t *testing.T) {
	rules := domain.DefaultRules()
	login := domain.Observation{EventType: domain.EventLogin, UserID: "u", DeviceID: "d2", NewDevice: true}
	a, _ := domain.Evaluate(rules, login, counts(nil))
	if len(a.Hits) != 1 || a.Hits[0].Rule != "new_device_login" || a.Action != domain.ActionNone || a.Score != 20 {
		t.Fatalf("new device login: %+v", a)
	}
	login.NewDevice = false
	if a, _ := domain.Evaluate(rules, login, counts(nil)); len(a.Hits) != 0 {
		t.Fatalf("known device: %+v", a)
	}
}

func TestTallyErrorsStopTheEvaluation(t *testing.T) {
	boom := errors.New("down")
	o := domain.Observation{EventType: domain.EventRegistered, UserID: "u", DeviceID: "d"}
	_, err := domain.Evaluate(domain.DefaultRules(), o, func(domain.Rule, string) (int, error) { return 0, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
}

func TestActionAndDurationText(t *testing.T) {
	for _, s := range []string{"NONE", "STEP_UP", "REVIEW", "REJECT"} {
		a, err := domain.ParseAction(s)
		if err != nil || a.String() != s {
			t.Fatalf("%s: %v %v", s, a, err)
		}
	}
	for d, want := range map[time.Duration]string{24 * time.Hour: "24h", 15 * time.Minute: "15m", 90 * time.Second: "1m30s"} {
		if got := domain.Duration(d).String(); got != want {
			t.Fatalf("%v written as %q, want %q", d, got, want)
		}
	}
}
