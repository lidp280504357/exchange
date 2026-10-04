package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// The platform's settings from the console (design 2026-10-04 §4.1, §4.2,
// §5; D2): the profile the sites show (one ADMIN, audited with what
// changed), its images, and the welcome credits, what a new account gets.
// Lowering or clearing those applies at once; raising them, or from
// nothing to something, waits for a second ADMIN (WELCOME_CREDIT), at most
// WelcomeRaiseCap in USDT a change.

// platformTarget is the audit target of the platform's settings.
const platformTarget = "platform"

// WelcomeRaiseCap is the most one change may raise the welcome credits
// by, in USDT (the raises of every asset together; design §4.2).
var WelcomeRaiseCap = decimal.NewFromInt(10_000)

// The platform settings' refusals.
var (
	ErrWelcomeRaiseCap = apperr.New(apperr.KindUnprocessable, "ADMIN_WELCOME_RAISE_CAP",
		"a change raises the welcome credits by at most 10,000 USDT, whoever approves it")
	ErrWelcomeUnpriced = apperr.New(apperr.KindUnprocessable, "ADMIN_WELCOME_UNPRICED",
		"an asset without a fresh USDT price cannot be raised: its worth is unknown")
	ErrWelcomeRaisePending = apperr.New(apperr.KindConflict, "ADMIN_WELCOME_RAISE_PENDING",
		"the same raise, asked by the same administrator against the same version, already waits for a second ADMIN")
)

// The platform's image kinds and the most an upload may be (the service
// checks the rest: square, its types per kind).
var (
	platformImageKinds = []string{"logo_light", "logo_dark", "favicon", "apple_touch_icon"}
	platformImageMIMEs = []string{"image/png", "image/svg+xml", "image/webp"}
)

const platformImageMax = 200 << 10

// PlatformProfile returns the profile the sites show and who last changed it.
func (s *Service) PlatformProfile(ctx context.Context, p Principal) (json.RawMessage, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return nil, err
	}
	return s.Platform.Profile(ctx)
}

// UpdatePlatformProfile replaces the profile (all but the images and the
// welcome credits) as of the version it carries; audited with the fields
// that changed, before and after.
func (s *Service) UpdatePlatformProfile(ctx context.Context, p Principal, write json.RawMessage, reason string) (json.RawMessage, error) {
	if err := p.require(domain.PermSettingsEdit); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if len(write) == 0 || json.Unmarshal(write, &fields) != nil {
		return nil, apperr.Invalid("the profile is an object")
	}
	if _, ok := fields["expected_version"]; !ok {
		return nil, apperr.Invalid("expected_version is the version read")
	}
	before, err := s.Platform.Profile(ctx)
	if err != nil {
		return nil, err
	}
	after, err := s.Platform.UpdateProfile(ctx, write, p.Admin.Email, strings.TrimSpace(reason))
	if err != nil {
		return nil, err
	}
	details, _ := json.Marshal(map[string]any{"changes": profileChanges(before, after), "version": versionOf(after)})
	return after, s.audit(ctx, p, platformTarget, "admin.platform.updated", strings.TrimSpace(reason), string(details))
}

// profileChanges are the profile's fields that differ between before and
// after, each {old, new}; the bookkeeping (version, times, who) left out.
func profileChanges(before, after json.RawMessage) map[string]map[string]json.RawMessage {
	var b, a map[string]json.RawMessage
	_ = json.Unmarshal(before, &b)
	_ = json.Unmarshal(after, &a)
	out := map[string]map[string]json.RawMessage{}
	for k, v := range a {
		if slices.Contains([]string{"version", "updated_at", "updated_by", "welcome_credits", "images"}, k) {
			continue
		}
		if old, ok := b[k]; !ok || !jsonEqual(old, v) {
			out[k] = map[string]json.RawMessage{"old": rawOrNull(b[k]), "new": v}
		}
	}
	return out
}

// jsonEqual compares two JSON values as values (object keys in any order).
func jsonEqual(x, y json.RawMessage) bool {
	var a, b any
	if json.Unmarshal(x, &a) != nil || json.Unmarshal(y, &b) != nil {
		return bytes.Equal(x, y)
	}
	ax, _ := json.Marshal(a)
	bx, _ := json.Marshal(b)
	return bytes.Equal(ax, bx)
}

func rawOrNull(v json.RawMessage) json.RawMessage {
	if len(v) == 0 {
		return json.RawMessage("null")
	}
	return v
}

// versionOf is a setting's version (its "version" field), 0 without one.
func versionOf(raw json.RawMessage) int64 {
	var v struct {
		Version int64 `json:"version"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.Version
}

// PutPlatformImage uploads one of the platform's images (base64); audited
// with its type, size and SHA-256, never its bytes.
func (s *Service) PutPlatformImage(ctx context.Context, p Principal, kind, data, mime, reason string) (json.RawMessage, error) {
	if err := p.require(domain.PermSettingsEdit); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	if !slices.Contains(platformImageKinds, kind) {
		return nil, apperr.NotFound("no such image")
	}
	if !slices.Contains(platformImageMIMEs, mime) {
		return nil, apperr.Invalid("an image is PNG, SVG or WebP")
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(raw) == 0 {
		return nil, apperr.Invalid("data is the image in base64")
	}
	if len(raw) > platformImageMax {
		return nil, apperr.Invalid("an image is at most 200 KB")
	}
	out, err := s.Platform.PutImage(ctx, kind, data, mime, p.Admin.Email, strings.TrimSpace(reason))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	details, _ := json.Marshal(map[string]any{"kind": kind, "mime": mime, "size": len(raw), "sha256": hex.EncodeToString(sum[:]), "version": versionOf(out)})
	return out, s.audit(ctx, p, platformTarget, "admin.platform.image_updated", strings.TrimSpace(reason), string(details))
}

// DeletePlatformImage removes an uploaded image; the sites show their
// built-in one again.
func (s *Service) DeletePlatformImage(ctx context.Context, p Principal, kind, reason string) (json.RawMessage, error) {
	if err := p.require(domain.PermSettingsEdit); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	if !slices.Contains(platformImageKinds, kind) {
		return nil, apperr.NotFound("no such image")
	}
	out, err := s.Platform.DeleteImage(ctx, kind, p.Admin.Email, strings.TrimSpace(reason))
	if err != nil {
		return nil, err
	}
	details, _ := json.Marshal(map[string]any{"kind": kind, "version": versionOf(out)})
	return out, s.audit(ctx, p, platformTarget, "admin.platform.image_removed", strings.TrimSpace(reason), string(details))
}

// WelcomeCredits returns what a new account gets, the master switch and
// the setting's version.
func (s *Service) WelcomeCredits(ctx context.Context, p Principal) (json.RawMessage, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return nil, err
	}
	return s.Platform.WelcomeCredits(ctx)
}

// WelcomeResult is a change of the welcome credits: done (Setting), or
// waiting for a second ADMIN (Approval).
type WelcomeResult struct {
	Setting  json.RawMessage
	Approval *domain.Approval
}

// welcomeSetting is the ledger's welcome credits as the console reads them.
type welcomeSetting struct {
	Credits   []ports.WelcomeCredit `json:"credits"`
	Version   int64                 `json:"version"`
	UpdatedBy string                `json:"updated_by"`
}

// SetWelcomeCredits replaces the welcome credits as of expectedVersion
// (an empty list gives nothing). Lowering or clearing applies at once;
// any raise waits for a second ADMIN, at most WelcomeRaiseCap in USDT.
func (s *Service) SetWelcomeCredits(ctx context.Context, p Principal, credits []ports.WelcomeCredit, expectedVersion int64, reason string) (WelcomeResult,
	error,
) {
	if err := p.require(domain.PermSettingsEdit); err != nil {
		return WelcomeResult{}, err
	}
	if err := needReason(reason); err != nil {
		return WelcomeResult{}, err
	}
	credits, err := normalizeCredits(credits)
	if err != nil {
		return WelcomeResult{}, err
	}
	raw, err := s.Platform.WelcomeCredits(ctx)
	if err != nil {
		return WelcomeResult{}, err
	}
	var cur welcomeSetting
	if err := json.Unmarshal(raw, &cur); err != nil {
		return WelcomeResult{}, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "the ledger answered the welcome credits in another shape")
	}
	if cur.Version != expectedVersion {
		return WelcomeResult{}, apperr.New(apperr.KindConflict, "LEDGER_SETTINGS_CHANGED", "the welcome credits changed since they were read").
			WithDetail("version", cur.Version)
	}
	raises := welcomeRaises(cur.Credits, credits)
	if len(raises) == 0 {
		out, err := s.Platform.SetWelcomeCredits(ctx, credits, expectedVersion, p.Admin.Email, strings.TrimSpace(reason))
		if err != nil {
			return WelcomeResult{}, err
		}
		details, _ := json.Marshal(map[string]any{"old": cur.Credits, "new": credits, "version": versionOf(out)})
		return WelcomeResult{Setting: out}, s.audit(ctx, p, platformTarget, "admin.platform.welcome_changed", strings.TrimSpace(reason), string(details))
	}
	if err := s.checkCreditAssets(ctx, credits); err != nil {
		return WelcomeResult{}, err
	}
	worth := decimal.Zero
	for _, r := range raises {
		v, ok := s.worth(ctx, r.Asset, r.Amount)
		if !ok {
			return WelcomeResult{}, ErrWelcomeUnpriced.WithDetail("asset", r.Asset)
		}
		worth = worth.Add(v)
	}
	if worth.GreaterThan(WelcomeRaiseCap) {
		return WelcomeResult{}, ErrWelcomeRaiseCap.WithDetail("max_usdt", WelcomeRaiseCap.String()).WithDetail("raise_usdt", worth.Round(2).String())
	}
	a, err := s.requestWelcome(ctx, p, cur, credits, worth, strings.TrimSpace(reason))
	if err != nil {
		return WelcomeResult{}, err
	}
	return WelcomeResult{Approval: &a}, nil
}

// creditAssetRE is an asset code as the ledger takes one in the welcome
// credits.
var creditAssetRE = regexp.MustCompile(`^[A-Z0-9]{2,12}$`)

// normalizeCredits checks and orders a list of welcome credits: assets in
// capitals, each once, at most 10; an amount of 0 is the asset not given.
func normalizeCredits(in []ports.WelcomeCredit) ([]ports.WelcomeCredit, error) {
	out := []ports.WelcomeCredit{}
	seen := map[string]bool{}
	for _, c := range in {
		asset := strings.ToUpper(strings.TrimSpace(c.Asset))
		if !creditAssetRE.MatchString(asset) {
			return nil, apperr.Invalid("a welcome credit names its asset: 2 to 12 capitals and digits")
		}
		if seen[asset] {
			return nil, apperr.Invalid("an asset is given once: " + asset)
		}
		seen[asset] = true
		if c.Amount.IsNegative() {
			return nil, apperr.Invalid("a welcome credit is not negative: " + asset)
		}
		if c.Amount.IsZero() {
			continue
		}
		out = append(out, ports.WelcomeCredit{Asset: asset, Amount: c.Amount})
	}
	if len(out) > 10 {
		return nil, apperr.Invalid("at most 10 assets")
	}
	slices.SortFunc(out, func(a, b ports.WelcomeCredit) int { return strings.Compare(a.Asset, b.Asset) })
	return out, nil
}

// checkCreditAssets holds a raise to the ledger's rules before a second
// ADMIN sees it: every asset listed, each amount within its asset's
// decimals; otherwise the approval would only fail when carried out
// (review ㉚). A lowering goes to the ledger at once, which checks it.
func (s *Service) checkCreditAssets(ctx context.Context, credits []ports.WelcomeCredit) error {
	raw, err := s.Catalog.List(ctx)
	if err != nil {
		return err
	}
	var cat struct {
		Assets []struct {
			Code     string `json:"asset_code"`
			Decimals int32  `json:"decimals"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(raw, &cat); err != nil {
		return apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "the asset list came in another shape")
	}
	decimals := map[string]int32{}
	for _, a := range cat.Assets {
		decimals[a.Code] = a.Decimals
	}
	for _, c := range credits {
		d, ok := decimals[c.Asset]
		if !ok {
			return apperr.Invalid("no asset " + c.Asset)
		}
		if !c.Amount.Equal(c.Amount.Truncate(d)) {
			return apperr.Invalid(fmt.Sprintf("%s has %d decimals", c.Asset, d))
		}
	}
	return nil
}

// welcomeRaises are the assets a new list gives more of than the current
// one, each with how much more.
func welcomeRaises(cur, next []ports.WelcomeCredit) []ports.WelcomeCredit {
	before := map[string]decimal.Decimal{}
	for _, c := range cur {
		before[strings.ToUpper(c.Asset)] = c.Amount
	}
	var out []ports.WelcomeCredit
	for _, c := range next {
		if more := c.Amount.Sub(before[c.Asset]); more.IsPositive() {
			out = append(out, ports.WelcomeCredit{Asset: c.Asset, Amount: more})
		}
	}
	return out
}

// requestWelcome keeps a raise of the welcome credits as a request for a
// second ADMIN (WELCOME_CREDIT): the list asked for, the one it replaces
// and its version, and what the raise is worth.
func (s *Service) requestWelcome(ctx context.Context, p Principal, cur welcomeSetting, next []ports.WelcomeCredit, worth decimal.Decimal,
	reason string,
) (domain.Approval, error) {
	nextJSON, _ := json.Marshal(next)
	curJSON, _ := json.Marshal(cur.Credits)
	value := worth.Round(2)
	a := domain.Approval{
		ID: uuid.Must(uuid.NewV7()).String(), Kind: domain.KindWelcomeCredit, Reason: reason, Status: domain.ApprovalPending,
		Payload: map[string]string{
			"credits": string(nextJSON), "previous": string(curJSON), "expected_version": strconv.FormatInt(cur.Version, 10),
			"raise_usdt": value.String(), "actor": p.Admin.Email,
		},
		RequestedBy: p.Admin.ID, RequestedByEmail: p.Admin.Email, CreatedAt: s.Now(), Mode: domain.ModeTwoPerson,
		Escalation: domain.EscalationWelcomeRaise, ValueUSDT: &value,
	}
	actions := fundActions[a.Kind]
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		// Asking again for the same raise against the same version while
		// the first request waits is refused, naming it (review ㉛).
		pending, err := r.Approvals().List(ctx, domain.ApprovalPending, time.Time{}, "", 200)
		if err != nil {
			return err
		}
		for _, o := range pending {
			if o.Kind != domain.KindWelcomeCredit || o.RequestedBy != a.RequestedBy || o.Payload["expected_version"] != a.Payload["expected_version"] {
				continue
			}
			var asked []ports.WelcomeCredit
			if json.Unmarshal([]byte(o.Payload["credits"]), &asked) == nil && sameCredits(asked, next) {
				return ErrWelcomeRaisePending.WithDetail("approval_id", o.ID)
			}
		}
		if err := r.Approvals().Insert(ctx, a); err != nil {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: fundTarget(a), Action: actions.requested, Actor: p.Admin.Email, Reason: a.Reason, Details: fundDetails(a),
		}, p.Admin.Email)
	})
	return a, err
}

// executeWelcome sets the welcome credits an approved WELCOME_CREDIT asks
// for, as of the version it was asked against: a change since then refuses
// it (LEDGER_SETTINGS_CHANGED), and the request fails.
func (s *Service) executeWelcome(ctx context.Context, a domain.Approval, p Principal) (string, error) {
	var credits []ports.WelcomeCredit
	if err := json.Unmarshal([]byte(a.Payload["credits"]), &credits); err != nil {
		return "", err
	}
	version, err := strconv.ParseInt(a.Payload["expected_version"], 10, 64)
	if err != nil {
		return "", err
	}
	out, err := s.Platform.SetWelcomeCredits(ctx, credits, version, a.Payload["actor"], a.Reason+" (approved by "+p.Admin.Email+")")
	if apperr.Is(err, "LEDGER_SETTINGS_CHANGED") {
		// An earlier attempt whose answer was lost may have set them: the
		// ledger at the next version, holding these credits, set by this
		// request's actor, is that attempt (review ㉚). When the setting
		// cannot be read again the outcome is unknown: the request stays
		// pending (review ㉛).
		raw, rerr := s.Platform.WelcomeCredits(ctx)
		var cur welcomeSetting
		if rerr == nil {
			rerr = json.Unmarshal(raw, &cur)
		}
		if rerr != nil {
			return "", apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "the welcome credits changed and cannot be read again: try again")
		}
		if cur.Version == version+1 && cur.UpdatedBy == a.Payload["actor"] && sameCredits(cur.Credits, credits) {
			return "welcome credits version " + strconv.FormatInt(cur.Version, 10) + " (set by an earlier attempt)", nil
		}
	}
	if err != nil {
		return "", err
	}
	return "welcome credits version " + strconv.FormatInt(versionOf(out), 10), nil
}

// sameCredits reports whether two lists give the same, whatever their
// order and however their amounts are written.
func sameCredits(a, b []ports.WelcomeCredit) bool {
	x, errA := normalizeCredits(a)
	y, errB := normalizeCredits(b)
	if errA != nil || errB != nil || len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i].Asset != y[i].Asset || !x[i].Amount.Equal(y[i].Amount) {
			return false
		}
	}
	return true
}

// welcomeApprovalTTL: a raise is decided within a day of its request, while
// the prices it was valued at still hold (review ㉚).
const welcomeApprovalTTL = 24 * time.Hour

// approvalExpiry is when a pending request lapses: a simulated market's
// (simExpiry) and a welcome credits raise do; ok is false for the others.
func approvalExpiry(a domain.Approval) (at time.Time, ok bool) {
	switch {
	case simKind(a.Kind):
		return simExpiry(a), true
	case a.Kind == domain.KindWelcomeCredit:
		return a.CreatedAt.Add(welcomeApprovalTTL), true
	}
	return time.Time{}, false
}

// lapsedAt reports whether a request that lapses has lapsed by t.
func lapsedAt(a domain.Approval, t time.Time) bool {
	at, ok := approvalExpiry(a)
	return ok && !t.Before(at)
}
