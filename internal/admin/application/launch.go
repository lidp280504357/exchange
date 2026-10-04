package application

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/flags"
)

// The launch checklist (design 2026-10-04 §4.6, D2): what of the
// learning setup is still on, read-only, each item with what it is now
// and whether it is what a launch needs. It changes nothing; every role
// reads it. It covers what the console can change and see; the deployment
// side is the launch handbook's (D3).

// A launch item's status: as a launch needs it, not, not wired yet (its
// source is another batch's, D1), or its source did not answer.
const (
	LaunchOK      = "OK"
	LaunchFail    = "FAIL"
	LaunchPending = "PENDING"
	LaunchUnknown = "UNKNOWN"
)

// The launch items in the order the design lists them.
var launchKeys = []string{
	"welcome_credits", "learning_mode", "registration", "admin_totp", "two_person", "test_assets", "custodian", "withdraw",
	"brand", "coin_profile", "legal", "third_party", "admins", "domain",
}

// The legal pages a launch needs published (the others may stay bundled).
var launchLegal = []string{"terms", "privacy", "risk"}

// The third parties whose configuration the services report
// (exchange_config_present{item}).
var launchThirdParties = []string{"turnstile", "mail", "alchemy"}

// UdunStandIn is the host of the custodian's stand-in (ADR-0017); a launch
// talks to the real gateway.
const UdunStandIn = "udun-mock"

// LaunchItem is one item: what it is now (Value, for the console to say)
// and its status.
type LaunchItem struct {
	Key    string
	Status string
	Value  map[string]any
}

// LaunchChecklist is every item; Ready when all are OK.
type LaunchChecklist struct {
	Ready     bool
	Items     []LaunchItem
	CheckedAt time.Time
}

// LaunchChecklist reads every item's source now; host is the one the
// console was reached at (admin.<domain> once live).
func (s *Service) LaunchChecklist(ctx context.Context, p Principal, host string) (LaunchChecklist, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return LaunchChecklist{}, err
	}
	items := map[string]LaunchItem{}
	set := func(key, status string, value map[string]any) {
		if value == nil {
			value = map[string]any{}
		}
		items[key] = LaunchItem{Key: key, Status: status, Value: value}
	}
	// put is set for an item whose status and value one call gives.
	put := func(key string) func(string, map[string]any) {
		return func(status string, value map[string]any) { set(key, status, value) }
	}

	// The switches.
	flagged := map[string]ports.Flag{}
	list, err := s.Flags.List(ctx)
	if err != nil {
		s.Log.WarnContext(ctx, "launch checklist: the flags are unknown", "error", err)
	}
	for _, f := range list {
		flagged[f.Key] = f
	}
	flagItem := func(key, flag string, wantOn bool) {
		if err != nil {
			set(key, LaunchUnknown, map[string]any{"flag": flag})
			return
		}
		f := flagged[flag]
		status := LaunchFail
		if f.Enabled == wantOn {
			status = LaunchOK
		}
		value := map[string]any{"flag": flag, "enabled": f.Enabled}
		if len(f.Rules) > 0 && string(f.Rules) != "{}" && string(f.Rules) != "null" {
			value["rules"] = f.Rules
		}
		set(key, status, value)
	}
	flagItem("admin_totp", flags.KeyAdminNoTOTP, false)
	flagItem("two_person", flags.KeyTwoPerson, true)
	flagItem("test_assets", flags.KeyTestAssets, false)
	flagItem("withdraw", flags.KeyWithdraw, true)

	// The welcome credits (the ledger's) and the platform's profile.
	put("welcome_credits")(s.launchWelcome(ctx, flagged[flags.KeyWelcomeCredit].Enabled))
	profile, profileStatus := s.launchProfile(ctx)
	if profile == nil {
		for _, key := range []string{"learning_mode", "registration", "brand", "domain"} {
			set(key, profileStatus, nil)
		}
	} else {
		put("learning_mode")(launchLearning(profile))
		put("registration")(launchRegistration(profile))
		put("brand")(launchBrand(profile))
		put("domain")(launchDomain(profile, host))
	}

	put("custodian")(s.launchCustodian(ctx))
	put("coin_profile")(s.launchCoin(ctx))
	put("legal")(s.launchLegal(ctx))
	put("third_party")(s.launchThirdParties(ctx))
	put("admins")(s.launchAdmins(ctx))

	out := LaunchChecklist{Ready: true, CheckedAt: s.Now()}
	for _, key := range launchKeys {
		it := items[key]
		out.Ready = out.Ready && it.Status == LaunchOK
		out.Items = append(out.Items, it)
	}
	return out, nil
}

// launchWelcome: a new account gets nothing (every amount 0, or none).
// The master switch is shown beside them; the amounts are what a launch
// sets (design §4.2).
func (s *Service) launchWelcome(ctx context.Context, master bool) (string, map[string]any) {
	value := map[string]any{"switch": master}
	if s.Platform == nil {
		return LaunchPending, value
	}
	raw, err := s.Platform.WelcomeCredits(ctx)
	if err != nil {
		s.Log.WarnContext(ctx, "launch checklist: the welcome credits are unknown", "error", err)
		return LaunchUnknown, value
	}
	var set welcomeSetting
	if json.Unmarshal(raw, &set) != nil {
		return LaunchUnknown, value
	}
	value["credits"] = set.Credits
	for _, c := range set.Credits {
		if c.Amount.IsPositive() {
			return LaunchFail, value
		}
	}
	return LaunchOK, value
}

// launchProfileView is what the checklist reads of the platform's profile.
type launchProfileView struct {
	Name         string `json:"name"`
	Domain       string `json:"domain"`
	LearningMode struct {
		Enabled bool `json:"enabled"`
	} `json:"learning_mode"`
	Registration struct {
		Status string `json:"status"`
	} `json:"registration"`
	Images struct {
		LogoLight *string `json:"logo_light"`
		LogoDark  *string `json:"logo_dark"`
		Favicon   *string `json:"favicon"`
	} `json:"images"`
}

// launchProfile reads the platform's profile; nil with the status of its
// items when it cannot be read.
func (s *Service) launchProfile(ctx context.Context) (*launchProfileView, string) {
	if s.Platform == nil {
		return nil, LaunchPending
	}
	raw, err := s.Platform.Profile(ctx)
	if err != nil {
		s.Log.WarnContext(ctx, "launch checklist: the platform's profile is unknown", "error", err)
		return nil, LaunchUnknown
	}
	var v launchProfileView
	if json.Unmarshal(raw, &v) != nil {
		return nil, LaunchUnknown
	}
	return &v, LaunchOK
}

// launchLearning: the learning banner is off.
func launchLearning(pr *launchProfileView) (string, map[string]any) {
	value := map[string]any{"enabled": pr.LearningMode.Enabled}
	if pr.LearningMode.Enabled {
		return LaunchFail, value
	}
	return LaunchOK, value
}

// launchRegistration shows whether sign-ups are open: the operators'
// decision either way (design §4.6), so it never holds a launch.
func launchRegistration(pr *launchProfileView) (string, map[string]any) {
	return LaunchOK, map[string]any{"status": pr.Registration.Status}
}

// launchBrand: the name, a logo and the favicon are the platform's own
// (uploaded, not the built-in images).
func launchBrand(pr *launchProfileView) (string, map[string]any) {
	value := map[string]any{
		"name": pr.Name, "logo": pr.Images.LogoLight != nil || pr.Images.LogoDark != nil, "favicon": pr.Images.Favicon != nil,
	}
	if strings.TrimSpace(pr.Name) == "" || (pr.Images.LogoLight == nil && pr.Images.LogoDark == nil) || pr.Images.Favicon == nil {
		return LaunchFail, value
	}
	return LaunchOK, value
}

// launchDomain: the profile names the PC site's host, and the console is
// reached at admin.<that host>.
func launchDomain(pr *launchProfileView, host string) (string, map[string]any) {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, found := strings.Cut(host, ":"); found {
		host = h
	}
	value := map[string]any{"domain": pr.Domain, "console_host": host}
	if pr.Domain == "" || host != "admin."+strings.ToLower(pr.Domain) {
		return LaunchFail, value
	}
	return LaunchOK, value
}

// launchCustodian: the custodian (UDUN) is configured and talks to the real
// gateway, not its stand-in. wallet-service reports the gateway's host as
// gateway_host (D1); without it the item waits.
func (s *Service) launchCustodian(ctx context.Context) (string, map[string]any) {
	if s.Wallet == nil {
		return LaunchUnknown, nil
	}
	raw, err := s.Wallet.Custody(ctx, "UDUN")
	if err != nil {
		s.Log.WarnContext(ctx, "launch checklist: the custodian is unknown", "error", err)
		return LaunchUnknown, nil
	}
	var c struct {
		Configured  bool    `json:"configured"`
		GatewayHost *string `json:"gateway_host"`
	}
	if json.Unmarshal(raw, &c) != nil {
		return LaunchUnknown, nil
	}
	value := map[string]any{"configured": c.Configured}
	if c.GatewayHost == nil {
		return LaunchPending, value
	}
	value["gateway_host"] = *c.GatewayHost
	if !c.Configured || *c.GatewayHost == "" || strings.EqualFold(*c.GatewayHost, UdunStandIn) {
		return LaunchFail, value
	}
	return LaunchOK, value
}

// launchCoin: the platform coin has its name and logo (the simulated
// market's coin; ASTRA when market-sim does not say).
func (s *Service) launchCoin(ctx context.Context) (string, map[string]any) {
	coin := "ASTRA"
	if s.Sim != nil {
		if st, err := s.simState(ctx); err == nil && st.coin() != "" {
			coin = st.coin()
		}
	}
	if s.Catalog == nil {
		return LaunchUnknown, map[string]any{"asset": coin}
	}
	raw, err := s.Catalog.AssetProfile(ctx, coin)
	if err != nil {
		s.Log.WarnContext(ctx, "launch checklist: the coin's profile is unknown", "asset", coin, "error", err)
		return LaunchUnknown, map[string]any{"asset": coin}
	}
	var pr struct {
		DisplayName string `json:"display_name"`
		LogoURL     string `json:"logo_url"`
	}
	if json.Unmarshal(raw, &pr) != nil {
		return LaunchUnknown, map[string]any{"asset": coin}
	}
	value := map[string]any{"asset": coin, "display_name": pr.DisplayName, "logo": pr.LogoURL != ""}
	if strings.TrimSpace(pr.DisplayName) == "" || pr.LogoURL == "" {
		return LaunchFail, value
	}
	return LaunchOK, value
}

// launchLegal: the terms, the privacy policy and the risk notice are
// published as the platform's own (an override; "publish the default"
// counts). The LEGAL section is D1's; until notification-service has it
// the item waits.
func (s *Service) launchLegal(ctx context.Context) (string, map[string]any) {
	if s.Content == nil {
		return LaunchUnknown, nil
	}
	raw, err := s.Content.Articles(ctx, "LEGAL")
	if err != nil {
		s.Log.InfoContext(ctx, "launch checklist: no legal pages yet", "error", err)
		return LaunchPending, nil
	}
	var body struct {
		Articles []struct {
			Slug   string `json:"slug"`
			Status string `json:"status"`
		} `json:"articles"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return LaunchUnknown, nil
	}
	published := []string{}
	for _, a := range body.Articles {
		if a.Status == "PUBLISHED" && slices.Contains(launchLegal, a.Slug) && !slices.Contains(published, a.Slug) {
			published = append(published, a.Slug)
		}
	}
	missing := []string{}
	for _, slug := range launchLegal {
		if !slices.Contains(published, slug) {
			missing = append(missing, slug)
		}
	}
	value := map[string]any{"published": published, "missing": missing}
	if len(missing) > 0 {
		return LaunchFail, value
	}
	return LaunchOK, value
}

// launchThirdParties: the human check, the mail service and the chain
// provider are configured, as the services report it
// (exchange_config_present{item}, values never shown). Until one reports
// any, the item waits (D1).
func (s *Service) launchThirdParties(ctx context.Context) (string, map[string]any) {
	if s.Probe == nil {
		return LaunchUnknown, nil
	}
	present := map[string]bool{}
	for _, h := range s.Probe.Check(ctx, true) {
		for item, ok := range h.ConfigPresent {
			present[item] = present[item] || ok
		}
	}
	if len(present) == 0 {
		return LaunchPending, nil
	}
	value := map[string]any{}
	status := LaunchOK
	for _, item := range launchThirdParties {
		ok, reported := present[item]
		if reported {
			value[item] = ok
		} else {
			value[item] = nil
		}
		if !ok {
			status = LaunchFail
		}
	}
	return status, value
}

// launchAdmins: at least two active ADMINs, every one with an
// authenticator.
func (s *Service) launchAdmins(ctx context.Context) (string, map[string]any) {
	admins, err := s.Store.Read().Admins().List(ctx)
	if err != nil {
		s.Log.WarnContext(ctx, "launch checklist: the administrators are unknown", "error", err)
		return LaunchUnknown, nil
	}
	active := 0
	without := []string{}
	for _, a := range admins {
		if a.Role != domain.RoleAdmin || a.Status != domain.StatusActive {
			continue
		}
		active++
		if len(a.TOTPSealed) == 0 {
			without = append(without, a.Email)
		}
	}
	value := map[string]any{"active_admins": active, "without_totp": without}
	if active < 2 || len(without) > 0 {
		return LaunchFail, value
	}
	return LaunchOK, value
}
