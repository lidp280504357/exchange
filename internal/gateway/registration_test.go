package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skill/exchange/internal/platform/httpx"
)

// The platform's profile, its images, the manifest and the new content
// sections are public; while the profile closes sign-ups, the gateway
// refuses register/complete and REGISTER codes, and lets the other codes
// and requests through with their bodies whole (design 2026-10-04 §4.3).
func TestRegistrationGateAndPlatformRoutes(t *testing.T) {
	status := "OPEN"
	profile := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/platform/profile" {
			http.NotFound(w, r)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"registration": map[string]any{"status": status}})
	}))
	defer profile.Close()
	gate := &Registration{ProfileURL: profile.URL + "/v1/platform/profile", HTTP: profile.Client(), Log: slog.New(slog.DiscardHandler)}

	var seenBody string
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seenBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	})
	f := newAuthFixture(t)
	r := httpx.NewRouter(httpx.RouterOptions{Logger: slog.New(slog.DiscardHandler)})
	Mount(r, Guards{Authn: f.authn, Registration: gate}, Upstreams{
		Auth: upstream, User: upstream, Notification: upstream, Instrument: upstream, Ledger: upstream,
	})
	call := func(method, path, body string) (int, string) {
		req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var e struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		return rec.Code, e.Code
	}

	for _, p := range []string{
		"/v1/platform/profile", "/v1/platform/images/favicon", "/manifest.webmanifest", "/v1/platform/apps", "/v1/legal", "/v1/legal/terms",
		"/v1/home", "/v1/home/home-hero",
	} {
		if s, _ := call(http.MethodGet, p, ""); s != http.StatusNoContent {
			t.Errorf("%s is public: %d", p, s)
		}
	}

	register := `{"scene":"REGISTER","channel":"EMAIL","identifier":"a@example.com"}`
	if err := gate.read(context.Background()); err != nil || gate.Closed() {
		t.Fatalf("open: %v", err)
	}
	if s, _ := call(http.MethodPost, "/v1/auth/register/complete", "{}"); s != http.StatusNoContent {
		t.Fatalf("open sign-up: %d", s)
	}
	status = "CLOSED"
	if err := gate.read(context.Background()); err != nil || !gate.Closed() {
		t.Fatalf("closed: %v", err)
	}
	if s, code := call(http.MethodPost, "/v1/auth/register/complete", "{}"); s != http.StatusForbidden || code != "AUTH_REGISTRATION_CLOSED" {
		t.Fatalf("closed sign-up: %d %s", s, code)
	}
	if s, code := call(http.MethodPost, "/v1/auth/otp/request", register); s != http.StatusForbidden || code != "AUTH_REGISTRATION_CLOSED" {
		t.Fatalf("a REGISTER code: %d %s", s, code)
	}
	// As auth-service reads the scene: a lower-case or padded REGISTER is one
	// (review BD); a body the gate cannot read counts as one.
	for name, body := range map[string]string{
		"a lower-case scene":  `{"scene":" register ","channel":"EMAIL","identifier":"a@example.com"}`,
		"a dotless ı":         `{"scene":"regıster","channel":"EMAIL","identifier":"a@example.com"}`,
		"a body above 64 KiB": `{"scene":"LOGIN","channel":"EMAIL","identifier":"a@example.com","pad":"` + strings.Repeat("x", peekLimit) + `"}`,
		"a body not JSON":     `scene=REGISTER`,
	} {
		if s, code := call(http.MethodPost, "/v1/auth/otp/request", body); s != http.StatusForbidden || code != "AUTH_REGISTRATION_CLOSED" {
			t.Fatalf("%s: %d %s", name, s, code)
		}
	}
	login := `{"scene":"LOGIN","channel":"EMAIL","identifier":"a@example.com"}`
	if s, _ := call(http.MethodPost, "/v1/auth/otp/request", login); s != http.StatusNoContent || seenBody != login {
		t.Fatalf("a LOGIN code: %d, the upstream read %q", s, seenBody)
	}
	// A dotted İ upper-cases to "REGİSTER": no REGISTER for auth-service
	// either (it refuses the scene), so the gate passes it on (review BG).
	dotted := `{"scene":"regİster","channel":"EMAIL","identifier":"a@example.com"}`
	if s, _ := call(http.MethodPost, "/v1/auth/otp/request", dotted); s != http.StatusNoContent || seenBody != dotted {
		t.Fatalf("a dotted İ: %d, the upstream read %q", s, seenBody)
	}
	if s, _ := call(http.MethodPost, "/v1/auth/login/password", "{}"); s != http.StatusNoContent {
		t.Fatalf("sign-in stays open: %d", s)
	}

	// A profile that cannot be read keeps the state last read.
	profile.Close()
	if err := gate.read(context.Background()); err == nil || !gate.Closed() {
		t.Fatalf("unreadable: %v %v", err, gate.Closed())
	}
}
