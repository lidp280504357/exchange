package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/instrument/application"
	"github.com/skill/exchange/internal/instrument/domain"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
)

// The platform's profile (design 2026-10-04 §4.1, api/openapi/platform.yaml):
// public reads for the sites through the gateway, and the admin console's
// reads and changes on /internal, which the gateway does not route.
func (h *Handler) platformRoutes(r chi.Router) {
	r.Get("/v1/platform/profile", h.publicProfile)
	r.Get("/v1/platform/images/{kind}", h.platformImage)
	r.Get("/manifest.webmanifest", h.manifest)
	r.Get("/internal/platform/profile", h.internalProfile)
	r.Put("/internal/platform/profile", h.updateProfile)
	r.Put("/internal/platform/images/{kind}", h.uploadImage)
	r.Delete("/internal/platform/images/{kind}", h.deleteImage)
}

// PlatformProfileJSON is the profile as the API shows it.
type PlatformProfileJSON struct {
	Name           string              `json:"name"`
	ShortName      string              `json:"short_name"`
	Domain         string              `json:"domain"`
	ThemeColor     string              `json:"theme_color"`
	BrandColor     string              `json:"brand_color"`
	Images         map[string]*string  `json:"images"`
	Footer         FooterJSON          `json:"footer"`
	Contact        ContactJSON         `json:"contact"`
	Social         []SocialJSON        `json:"social"`
	DefaultLocale  string              `json:"default_locale"`
	TestMode       TestModeJSON        `json:"test_mode"`
	Registration   RegistrationJSON    `json:"registration"`
	WelcomeCredits []WelcomeCreditJSON `json:"welcome_credits"`
	Version        int64               `json:"version"`
	UpdatedAt      string              `json:"updated_at"`
	// UpdatedBy is shown to the admin console only.
	UpdatedBy *string `json:"updated_by,omitempty"`
}

// FooterJSON is the sites' footer.
type FooterJSON struct {
	Copyright  domain.Texts `json:"copyright"`
	Compliance domain.Texts `json:"compliance"`
}

// ContactJSON is how users reach the exchange.
type ContactJSON struct {
	Email      string  `json:"email"`
	SupportURL *string `json:"support_url"`
}

// SocialJSON is a link to the exchange's page on a network.
type SocialJSON struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

// TestModeJSON is the test mode: on, its banner shown, the banner's text.
type TestModeJSON struct {
	Enabled bool         `json:"enabled"`
	Banner  bool         `json:"banner"`
	Text    domain.Texts `json:"text"`
}

// RegistrationJSON says whether sign-ups are open.
type RegistrationJSON struct {
	Status     string       `json:"status"`
	ClosedText domain.Texts `json:"closed_text"`
}

// WelcomeCreditJSON is an asset and the amount a new account gets.
type WelcomeCreditJSON struct {
	Asset  string `json:"asset"`
	Amount string `json:"amount"`
}

// PlatformProfileJSONOf renders the profile with the welcome credits.
func PlatformProfileJSONOf(p domain.PlatformProfile, credits []domain.Credit) PlatformProfileJSON {
	p.Normalize()
	out := PlatformProfileJSON{
		Name: p.Name, ShortName: p.ShortName, Domain: p.Domain, ThemeColor: p.ThemeColor, BrandColor: p.BrandColor,
		Images: map[string]*string{}, Footer: FooterJSON{Copyright: p.Footer.Copyright, Compliance: p.Footer.Compliance},
		Contact: ContactJSON{Email: p.Contact.Email, SupportURL: optional(p.Contact.SupportURL)}, Social: []SocialJSON{},
		DefaultLocale: p.DefaultLocale, TestMode: TestModeJSON{Enabled: p.Test.Enabled, Banner: p.Test.Banner, Text: p.Test.Text},
		Registration:   RegistrationJSON{Status: p.Registration.Status, ClosedText: p.Registration.ClosedText},
		WelcomeCredits: []WelcomeCreditJSON{}, Version: p.Version, UpdatedAt: httpx.FormatTime(p.UpdatedAt),
	}
	for _, kind := range domain.ImageKinds {
		out.Images[kind] = optional(application.PlatformImageURL(p, kind))
	}
	for _, s := range p.Social {
		out.Social = append(out.Social, SocialJSON{Kind: s.Kind, URL: s.URL})
	}
	for _, c := range credits {
		out.WelcomeCredits = append(out.WelcomeCredits, WelcomeCreditJSON{Asset: c.Asset, Amount: c.Amount.String()})
	}
	return out
}

// etag changes with the profile and with the welcome credits it shows.
func etag(p domain.PlatformProfile, credits []domain.Credit) string {
	f := fnv.New32a()
	for _, c := range credits {
		_, _ = fmt.Fprintf(f, "%s:%s,", c.Asset, c.Amount.String())
	}
	return fmt.Sprintf(`"%d-%x"`, p.Version, f.Sum32())
}

// notModified reports whether the request's If-None-Match names tag
// (weak or strong: Cloudflare weakens the tags it compresses).
func notModified(r *http.Request, tag string) bool {
	for _, t := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		if strings.TrimPrefix(strings.TrimSpace(t), "W/") == tag {
			return true
		}
	}
	return false
}

func (h *Handler) profileAndCredits(r *http.Request) (domain.PlatformProfile, []domain.Credit, error) {
	p, err := h.Platform.Profile(r.Context())
	if err != nil {
		return domain.PlatformProfile{}, nil, err
	}
	credits, _ := h.Platform.WelcomeCredits()
	return p, credits, nil
}

// publicProfile serves the profile to the sites: cacheable for a minute,
// 304 for the ETag they hold.
func (h *Handler) publicProfile(w http.ResponseWriter, r *http.Request) {
	p, credits, err := h.profileAndCredits(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	tag := etag(p, credits)
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Header().Set("ETag", tag)
	if notModified(r, tag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, PlatformProfileJSONOf(p, credits))
}

// platformImage serves an uploaded image as logo serves an asset's: at
// the current version cached for good, at another briefly.
func (h *Handler) platformImage(w http.ResponseWriter, r *http.Request) {
	img, version, err := h.Platform.Image(r.Context(), chi.URLParam(r, "kind"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	hdr := w.Header()
	hdr.Set("Content-Type", img.MIME)
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	if r.URL.Query().Get("v") == strconv.FormatInt(version, 10) {
		hdr.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		hdr.Set("Cache-Control", "public, max-age=60")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(img.Data)
}

// manifestIcon is an icon of the web app manifest.
type manifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose"`
}

// builtInIcons are the mobile site's own icons (web/apps/m/public), shown
// while no icon is uploaded.
var builtInIcons = []manifestIcon{
	{Src: "/icon-192.png", Sizes: "192x192", Type: "image/png", Purpose: "any"},
	{Src: "/icon-512.png", Sizes: "512x512", Type: "image/png", Purpose: "any"},
	{Src: "/icon-192.png", Sizes: "192x192", Type: "image/png", Purpose: "maskable"},
	{Src: "/icon-512.png", Sizes: "512x512", Type: "image/png", Purpose: "maskable"},
	{Src: "/icon.svg", Sizes: "any", Type: "image/svg+xml", Purpose: "any"},
}

// appIconSize is the least an install prompt takes (192 px); an SVG serves
// any size.
const appIconSize = 192

// manifest serves the mobile site's web app manifest from the profile:
// the uploaded favicon and apple-touch-icon, and the marks that make an app
// icon (an SVG, or 192 px and more), as its icons; the site's own without
// any. With none of 192 px or more the site's own large icons stay too, or
// the site could not be installed (review BD).
func (h *Handler) manifest(w http.ResponseWriter, r *http.Request) {
	p, err := h.Platform.Profile(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	icons := []manifestIcon{}
	large := false
	for _, kind := range []string{domain.ImageFavicon, domain.ImageAppleTouchIcon, domain.ImageLogoDark, domain.ImageLogoLight} {
		img, ok := p.Images[kind]
		if !ok {
			continue
		}
		big := img.MIME == domain.LogoSVG || img.Width >= appIconSize
		if (kind == domain.ImageLogoDark || kind == domain.ImageLogoLight) && !big {
			continue
		}
		large = large || big
		sizes := fmt.Sprintf("%dx%d", img.Width, img.Width)
		if img.MIME == domain.LogoSVG {
			sizes = "any"
		}
		icons = append(icons, manifestIcon{Src: application.PlatformImageURL(p, kind), Sizes: sizes, Type: img.MIME, Purpose: "any"})
	}
	switch {
	case len(icons) == 0:
		icons = builtInIcons
	case !large:
		icons = append(icons, builtInIcons[:2]...)
	}
	description := p.Name
	if p.Test.Enabled {
		description = p.Test.Text.Text(p.DefaultLocale)
	}
	body, err := json.Marshal(map[string]any{
		"name": p.Name, "short_name": p.ShortName, "description": description, "id": "/", "start_url": "/", "scope": "/",
		"display": "standalone", "orientation": "portrait", "background_color": p.ThemeColor, "theme_color": p.ThemeColor,
		"lang": p.DefaultLocale, "icons": icons,
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *Handler) internalProfile(w http.ResponseWriter, r *http.Request) {
	p, credits, err := h.profileAndCredits(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeAdminProfile(w, p, credits)
}

func writeAdminProfile(w http.ResponseWriter, p domain.PlatformProfile, credits []domain.Credit) {
	out := PlatformProfileJSONOf(p, credits)
	out.UpdatedBy = &p.UpdatedBy
	httpx.WriteJSON(w, http.StatusOK, out)
}

// profileWriteJSON is the console's new profile: everything but the
// images and the welcome credits.
type profileWriteJSON struct {
	Name          string           `json:"name"`
	ShortName     string           `json:"short_name"`
	Domain        string           `json:"domain"`
	ThemeColor    string           `json:"theme_color"`
	BrandColor    string           `json:"brand_color"`
	Footer        FooterJSON       `json:"footer"`
	Contact       ContactJSON      `json:"contact"`
	Social        []SocialJSON     `json:"social"`
	DefaultLocale string           `json:"default_locale"`
	TestMode      *TestModeJSON    `json:"test_mode"`
	Registration  RegistrationJSON `json:"registration"`
	// ExpectedVersion is the version the change was made on.
	ExpectedVersion *int64 `json:"expected_version"`
	Actor           string `json:"actor"`
	Reason          string `json:"reason"`
}

func (b profileWriteJSON) profile() domain.PlatformProfile {
	p := domain.PlatformProfile{
		Name: b.Name, ShortName: b.ShortName, Domain: b.Domain, ThemeColor: b.ThemeColor, BrandColor: b.BrandColor,
		Footer:        domain.PlatformFooter{Copyright: b.Footer.Copyright, Compliance: b.Footer.Compliance},
		Contact:       domain.PlatformContact{Email: b.Contact.Email},
		DefaultLocale: b.DefaultLocale,
		Registration:  domain.Registration{Status: b.Registration.Status, ClosedText: b.Registration.ClosedText},
	}
	if b.Contact.SupportURL != nil {
		p.Contact.SupportURL = *b.Contact.SupportURL
	}
	for _, s := range b.Social {
		p.Social = append(p.Social, domain.SocialLink{Kind: s.Kind, URL: s.URL})
	}
	return p
}

func (h *Handler) updateProfile(w http.ResponseWriter, r *http.Request) {
	var body profileWriteJSON
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if body.ExpectedVersion == nil {
		httpx.WriteError(w, r, apperr.Invalid("expected_version is required"))
		return
	}
	if body.TestMode == nil {
		httpx.WriteError(w, r, apperr.Invalid("test_mode is required"))
		return
	}
	next := body.profile()
	next.Test = domain.TestMode{Enabled: body.TestMode.Enabled, Banner: body.TestMode.Banner, Text: body.TestMode.Text}
	p, err := h.Platform.UpdateProfile(r.Context(), next, *body.ExpectedVersion, body.Actor, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	credits, _ := h.Platform.WelcomeCredits()
	writeAdminProfile(w, p, credits)
}

func (h *Handler) uploadImage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Data   string `json:"data"`
		MIME   string `json:"mime"`
		Actor  string `json:"actor"`
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	data, err := base64.StdEncoding.DecodeString(body.Data)
	if err != nil {
		httpx.WriteError(w, r, apperr.Invalid("data: base64"))
		return
	}
	p, err := h.Platform.SetImage(r.Context(), chi.URLParam(r, "kind"), &domain.Logo{Data: data, MIME: body.MIME}, body.Actor, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	credits, _ := h.Platform.WelcomeCredits()
	writeAdminProfile(w, p, credits)
}

func (h *Handler) deleteImage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Actor  string `json:"actor"`
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	p, err := h.Platform.SetImage(r.Context(), chi.URLParam(r, "kind"), nil, body.Actor, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	credits, _ := h.Platform.WelcomeCredits()
	writeAdminProfile(w, p, credits)
}
