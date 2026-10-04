package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/instrument/adapters/postgres"
	"github.com/skill/exchange/internal/instrument/application"
	"github.com/skill/exchange/internal/instrument/domain"
	"github.com/skill/exchange/internal/instrument/transport/httpapi"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
)

type creditsFrom []domain.Credit

func (c creditsFrom) WelcomeCredits(context.Context) ([]domain.Credit, error) { return c, nil }

// The platform profile (design 2026-10-04 §4.1): the seeded row, changes
// on the version read, images that move the version on, the history, and
// what the sites read.
func TestPlatformProfile(t *testing.T) {
	_, db := setup(t)
	ctx := context.Background()
	plat := &application.Platform{Store: postgres.NewStore(db, event.NewFactory("instrument-service", "test")), Now: time.Now}

	p, err := plat.Profile(ctx)
	if err != nil || p.Version != 1 || p.Name != "Astras" || p.Registration.Status != domain.RegistrationOpen || !p.Learning.Enabled ||
		p.Footer.Copyright[domain.LocaleZH] == "" || len(p.Images) != 0 || p.UpdatedBy != "system:migration" {
		t.Fatalf("seeded %+v %v", p, err)
	}

	next := p
	next.Name, next.ShortName, next.Domain = "Example Exchange", "Example", "example.com"
	next.Learning.Enabled = false
	next.Registration = domain.Registration{Status: domain.RegistrationClosed, ClosedText: domain.Texts{"zh-CN": "邀请制", "en": "By invitation"}}
	next.Social = []domain.SocialLink{{Kind: "x", URL: "https://x.com/example"}}
	saved, err := plat.UpdateProfile(ctx, next, 1, "admin:ops@example.com", "going live")
	if err != nil || saved.Version != 2 || saved.Name != "Example Exchange" || saved.Registration.Status != domain.RegistrationClosed ||
		len(saved.Social) != 1 || saved.UpdatedBy != "admin:ops@example.com" {
		t.Fatalf("updated %+v %v", saved, err)
	}
	if _, err := plat.UpdateProfile(ctx, next, 1, "admin:b@example.com", "stale"); !apperr.Is(err, "INSTRUMENT_PLATFORM_CHANGED") {
		t.Fatalf("on a stale version: %v", err)
	}
	bad := next
	bad.ThemeColor = "red"
	if _, err := plat.UpdateProfile(ctx, bad, 2, "admin:ops@example.com", "bad color"); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("a bad color: %v", err)
	}
	if p, _ := plat.Profile(ctx); p.Version != 2 {
		t.Fatalf("the cache was not dropped: %d", p.Version)
	}

	svg := []byte(`<svg viewBox="0 0 16 16"><circle cx="8" cy="8" r="8" onclick="x()"/></svg>`)
	withIcon, err := plat.SetImage(ctx, domain.ImageFavicon, &domain.Logo{Data: svg, MIME: domain.LogoSVG}, "admin:ops@example.com", "our icon")
	if err != nil || withIcon.Version != 3 || withIcon.Images[domain.ImageFavicon].MIME != domain.LogoSVG ||
		application.PlatformImageURL(withIcon, domain.ImageFavicon) != "/v1/platform/images/favicon?v=3" || withIcon.Name != "Example Exchange" {
		t.Fatalf("an icon %+v %v", withIcon, err)
	}
	img, version, err := plat.Image(ctx, domain.ImageFavicon)
	if err != nil || version != 3 || string(img.Data) == string(svg) {
		t.Fatalf("the icon %s v%d %v", img.Data, version, err)
	}
	if _, _, err := plat.Image(ctx, domain.ImageLogoDark); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("no dark logo: %v", err)
	}

	r := chi.NewRouter()
	(&httpapi.Handler{Svc: &application.Service{Store: plat.Store}, Platform: plat}).Routes(r)
	if err := plat.RefreshWelcomeCredits(ctx, creditsFrom{{Asset: "USDT", Amount: d("10000")}}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/platform/profile", nil))
	var got httpapi.PlatformProfileJSON
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK || got.Name != "Example Exchange" ||
		got.Images[domain.ImageFavicon] == nil || got.Images[domain.ImageLogoDark] != nil || len(got.WelcomeCredits) != 1 ||
		got.WelcomeCredits[0].Amount != "10000" || got.UpdatedBy != nil || w.Header().Get("Cache-Control") != "public, max-age=60" {
		t.Fatalf("public profile %d %s", w.Code, w.Body)
	}
	tag := w.Header().Get("ETag")
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/platform/profile", nil)
	req.Header.Set("If-None-Match", "W/"+tag)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotModified {
		t.Fatalf("with its ETag: %d", w.Code)
	}
	if err := plat.RefreshWelcomeCredits(ctx, creditsFrom{}); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Header().Get("ETag") == tag {
		t.Fatalf("after the credits changed: %d %s", w.Code, w.Header().Get("ETag"))
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, "/manifest.webmanifest", nil))
	var manifest struct {
		Name  string `json:"name"`
		Icons []struct {
			Src   string `json:"src"`
			Sizes string `json:"sizes"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &manifest); err != nil || w.Header().Get("Content-Type") != "application/manifest+json" ||
		manifest.Name != "Example Exchange" || len(manifest.Icons) != 1 || manifest.Icons[0].Sizes != "any" ||
		manifest.Icons[0].Src != "/v1/platform/images/favicon?v=3" {
		t.Fatalf("manifest %s %v", w.Body, err)
	}

	cleared, err := plat.SetImage(ctx, domain.ImageFavicon, nil, "admin:ops@example.com", "back to the default")
	if err != nil || cleared.Version != 4 || len(cleared.Images) != 0 {
		t.Fatalf("cleared %+v %v", cleared, err)
	}
	if again, err := plat.SetImage(ctx, domain.ImageFavicon, nil, "admin:ops@example.com", "again"); err != nil || again.Version != 4 {
		t.Fatalf("clearing nothing %+v %v", again, err)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, "/manifest.webmanifest", nil))
	if err := json.Unmarshal(w.Body.Bytes(), &manifest); err != nil || len(manifest.Icons) != 5 || manifest.Icons[0].Src != "/icon-192.png" {
		t.Fatalf("the built-in icons %s", w.Body)
	}
	if n := count(t, db, `SELECT count(*) FROM config_history WHERE entity = 'PLATFORM_PROFILE' AND source = 'PROFILE'`); n != 3 {
		t.Fatalf("%d history rows", n)
	}
}
