package svcsign

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var (
	opsKey   = []byte("0123456789abcdef0123456789abcdef")
	adminKey = []byte("fedcba9876543210fedcba9876543210")
)

func TestSignAndVerify(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	v := &Verifier{Keys: map[string][]byte{"ops": opsKey, "admin": adminKey}, Now: func() time.Time { return now }}
	body := []byte(`{"type":"JUMP","size":0.1,"actor":"ops","approved_by":"ops2"}`)
	h := Sign("admin", adminKey, http.MethodPost, "/internal/sim/events", body, now.Add(-time.Minute))
	if k, err := v.Verify(http.MethodPost, "/internal/sim/events", h, body); err != nil || k != "admin" {
		t.Fatalf("admin: %q %v", k, err)
	}
	if _, err := v.Verify(http.MethodPost, "/internal/sim/events", h, body); err == nil {
		t.Fatal("a replay passed")
	}
	// The same request again in the same second is another nonce: taken.
	if k, err := v.Verify(http.MethodPost, "/internal/sim/events", Sign("admin", adminKey, http.MethodPost, "/internal/sim/events", body, now.Add(-time.Minute)), body); err != nil || k != "admin" {
		t.Fatalf("the same request again: %q %v", k, err)
	}
	for name, c := range map[string]struct {
		path, header string
		body         []byte
	}{
		"another body":      {"/internal/sim/events", Sign("ops", opsKey, http.MethodPost, "/internal/sim/events", body, now), []byte(`{"approved_by":"me"}`)},
		"another path":      {"/internal/sim/params", Sign("ops", opsKey, http.MethodPost, "/internal/sim/events", body, now), body},
		"the wrong key":     {"/internal/sim/events", Sign("admin", opsKey, http.MethodPost, "/internal/sim/events", body, now), body},
		"an unknown key ID": {"/internal/sim/events", Sign("nobody", opsKey, http.MethodPost, "/internal/sim/events", body, now), body},
		"too old":           {"/internal/sim/events", Sign("ops", opsKey, http.MethodPost, "/internal/sim/events", body, now.Add(-6*time.Minute)), body},
		"none":              {"/internal/sim/events", "", body},
	} {
		if _, err := v.Verify(http.MethodPost, c.path, c.header, c.body); err == nil {
			t.Fatalf("%s passed", name)
		}
	}
	if CheckSecret("short") == nil || CheckSecret(string(opsKey)) != nil {
		t.Fatal("secret length")
	}
}

func TestChanges(t *testing.T) {
	v := &Verifier{Keys: map[string][]byte{"ops": opsKey}}
	var got, key string
	srv := httptest.NewServer(v.Changes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got, key = string(b), KeyID(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})))
	defer srv.Close()
	status := func(method, path string, body []byte, signed bool) int {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		var resp *http.Response
		if signed {
			resp, err = (Client{KeyID: "ops", Secret: opsKey}).Do(req, body)
		} else {
			if body != nil {
				req.Body = io.NopCloser(strings.NewReader(string(body)))
			}
			resp, err = http.DefaultClient.Do(req)
		}
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := status(http.MethodGet, "/internal/sim", nil, false); code != http.StatusNoContent || key != "" {
		t.Fatalf("a read: %d %q", code, key)
	}
	if code := status(http.MethodPost, "/internal/sim/events", []byte(`{}`), false); code != http.StatusUnauthorized {
		t.Fatalf("unsigned: %d", code)
	}
	if code := status(http.MethodPost, "/internal/sim/events?x=1", []byte(`{"a":1}`), true); code != http.StatusNoContent || got != `{"a":1}` || key != "ops" {
		t.Fatalf("signed: %d, body %q, key %q", code, got, key)
	}
}
