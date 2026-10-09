package domain

import (
	"testing"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
)

func TestTransitions(t *testing.T) {
	allowed := map[[2]string]bool{
		{StatusActive, StatusRiskReview}: true, {StatusRiskReview, StatusActive}: true,
		{StatusActive, StatusFrozen}: true, {StatusRiskReview, StatusFrozen}: true,
		{StatusFrozen, StatusActive}: true, {StatusActive, StatusClosed}: true,
		{StatusRiskReview, StatusClosed}: true, {StatusFrozen, StatusClosed}: true,
	}
	all := []string{StatusActive, StatusRiskReview, StatusFrozen, StatusClosed}
	for _, from := range all {
		for _, to := range all {
			err := CheckTransition(from, to)
			if (err == nil) != allowed[[2]string{from, to}] {
				t.Errorf("%s -> %s: %v", from, to, err)
			}
			if err != nil && !apperr.Is(err, "USER_STATUS_TRANSITION_INVALID") {
				t.Errorf("%s -> %s: wrong error %v", from, to, err)
			}
		}
	}
}

func TestNewStatusChange(t *testing.T) {
	if c, err := NewStatusChange("u", "frozen", "SUSPICIOUS_LOGIN", "cli:ops", ""); err != nil || c.To != StatusFrozen {
		t.Fatalf("valid: %+v %v", c, err)
	}
	for _, tc := range [][3]string{
		{"GONE", "SUSPICIOUS_LOGIN", "cli:ops"},
		{"FROZEN", "because", "cli:ops"},
		{"FROZEN", "SUSPICIOUS_LOGIN", " "},
	} {
		if _, err := NewStatusChange("u", tc[0], tc[1], tc[2], ""); err == nil {
			t.Errorf("accepted %v", tc)
		}
	}
}

func TestEligibility(t *testing.T) {
	on := flags.Flag{Enabled: true}
	noUS := flags.Flag{Enabled: true, Rules: flags.Rules{Regions: &flags.List{Deny: []string{"US"}}}}
	// The hidden test assets (ADR-0017): the end-to-end accounts' region
	// only, as on the test server.
	onlyAQ := flags.Flag{Enabled: true, Rules: flags.Rules{Regions: &flags.List{Allow: []string{"AQ"}}}}
	table := map[string]flags.Flag{
		flags.KeyTransfer:      on,
		flags.KeyDerivatives:   noUS,
		flags.KeyTestAssets:    onlyAQ,
		flags.KeyMarginEnabled: {Enabled: true, Rules: flags.Rules{Users: &flags.List{Allow: []string{"u-1"}}}},
		// wallet.withdraw is missing: off.
	}
	lookup := func(k string) (flags.Flag, bool) { f, ok := table[k]; return f, ok }
	user := func(status, region string) User { return User{ID: "u-1", Status: status, Region: region} }

	for _, tc := range []struct {
		u       User
		feature string
		allowed bool
		reason  string
	}{
		{user(StatusActive, "SG"), FeatureSpotTrade, true, ""},
		{user(StatusActive, "SG"), FeatureTransfer, true, ""},
		{user(StatusActive, "SG"), FeatureDerivativesTrade, true, ""},
		{user(StatusActive, "US"), FeatureDerivativesTrade, false, ReasonRegion},
		{user(StatusActive, "SG"), FeatureWithdraw, false, ReasonNotEligible},
		{user(StatusRiskReview, "SG"), FeatureSpotTrade, true, ""},
		{user(StatusRiskReview, "SG"), FeatureDeposit, true, ""},
		{user(StatusRiskReview, "SG"), FeatureTransfer, false, ReasonRiskReview},
		{user(StatusRiskReview, "SG"), FeatureDerivativesTrade, false, ReasonRiskReview},
		{user(StatusFrozen, "SG"), FeatureDeposit, true, ""},
		{user(StatusFrozen, "SG"), FeatureSpotTrade, false, ReasonFrozen},
		{user(StatusClosed, "SG"), FeatureDeposit, false, ReasonClosed},
		{user("WEIRD", "SG"), FeatureSpotTrade, false, ReasonNotEligible},
		{user(StatusActive, "AQ"), FeatureTestAssets, true, ""},
		{user(StatusActive, "SG"), FeatureTestAssets, false, ReasonRegion},
		{user(StatusFrozen, "AQ"), FeatureTestAssets, false, ReasonFrozen},
		// Margin trading: ACTIVE accounts only, then the switch's users.
		{user(StatusActive, "SG"), FeatureMarginTrade, true, ""},
		{User{ID: "u-2", Status: StatusActive, Region: "SG"}, FeatureMarginTrade, false, ReasonNotEligible},
		{user(StatusRiskReview, "SG"), FeatureMarginTrade, false, ReasonRiskReview},
	} {
		allowed, reason := Eligibility(tc.u, tc.feature, "", "", lookup)
		if allowed != tc.allowed || reason != tc.reason {
			t.Errorf("%s/%s %s: %v %s, want %v %s", tc.u.Status, tc.u.Region, tc.feature, allowed, reason, tc.allowed, tc.reason)
		}
	}
	if _, err := ParseFeature("MINING"); err == nil {
		t.Fatal("unknown feature accepted")
	}
	if f, err := ParseFeature(FeatureTestAssets); err != nil || f != "TEST_ASSETS" {
		t.Fatalf("TEST_ASSETS: %q %v", f, err)
	}
	// Off, or missing, it opens nothing.
	delete(table, flags.KeyTestAssets)
	if allowed, _ := Eligibility(user(StatusActive, "AQ"), FeatureTestAssets, "", "", lookup); allowed {
		t.Fatal("the test assets without their switch")
	}
}

func TestProfilePatch(t *testing.T) {
	u := User{Language: "zh-CN", Timezone: "Asia/Shanghai"}
	lang, tz, code := "en", "Europe/London", "Blue42"
	changed, err := ProfilePatch{Language: &lang, Timezone: &tz, AntiPhishingCode: &code}.Apply(&u)
	if err != nil || len(changed) != 3 || u.Language != "en" || u.Timezone != "Europe/London" || u.AntiPhishingCode != "Blue42" {
		t.Fatalf("apply: %v %v %+v", changed, err, u)
	}
	if changed, _ := (ProfilePatch{Language: &lang}).Apply(&u); len(changed) != 0 {
		t.Fatalf("unchanged value reported: %v", changed)
	}
	bad := "no spaces allowed"
	if _, err := (ProfilePatch{AntiPhishingCode: &bad}).Apply(&u); err == nil {
		t.Fatal("bad code accepted")
	}
	empty := ""
	if _, err := (ProfilePatch{AntiPhishingCode: &empty}).Apply(&u); err != nil || u.AntiPhishingCode != "" {
		t.Fatalf("clearing the code: %v", err)
	}
	if !(ProfilePatch{AntiPhishingCode: &empty}).SensitiveChange() || (ProfilePatch{Language: &lang}).SensitiveChange() {
		t.Fatal("only the anti-phishing code needs a step-up")
	}
}
