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

var secret = []byte("0123456789abcdef0123456789abcdef")

func TestSignAndVerify(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	v := &Verifier{Secret: secret, Now: func() time.Time { return now }}
	body := []byte(`{"type":"JUMP","size":0.1,"actor":"ops","approved_by":"ops2"}`)
	h := Sign(secret, http.MethodPost, "/internal/sim/events", body, now.Add(-time.Minute))
	if err := v.Verify(http.MethodPost, "/internal/sim/events", h, body); err != nil {
		t.Fatal(err)
	}
	if err := v.Verify(http.MethodPost, "/internal/sim/events", h, body); err == nil {
		t.Fatal("a replay passed")
	}
	for name, c := range map[string]struct {
		method, path, header string
		body                 []byte
	}{
		"another body":   {http.MethodPost, "/internal/sim/events", Sign(secret, http.MethodPost, "/internal/sim/events", body, now), []byte(`{"approved_by":"me"}`)},
		"another path":   {http.MethodPost, "/internal/sim/params", Sign(secret, http.MethodPost, "/internal/sim/events", body, now), body},
		"another secret": {http.MethodPost, "/internal/sim/events", Sign([]byte(strings.Repeat("x", 32)), http.MethodPost, "/internal/sim/events", body, now), body},
		"too old":        {http.MethodPost, "/internal/sim/events", Sign(secret, http.MethodPost, "/internal/sim/events", body, now.Add(-6*time.Minute)), body},
		"none":           {http.MethodPost, "/internal/sim/events", "", body},
	} {
		if err := v.Verify(c.method, c.path, c.header, c.body); err == nil {
			t.Fatalf("%s passed", name)
		}
	}
	if CheckSecret("short") == nil || CheckSecret(string(secret)) != nil {
		t.Fatal("secret length")
	}
}

func TestChanges(t *testing.T) {
	v := &Verifier{Secret: secret}
	var got string
	srv := httptest.NewServer(v.Changes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
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
			resp, err = (Client{Secret: secret}).Do(req, body)
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
	if got := status(http.MethodGet, "/internal/sim", nil, false); got != http.StatusNoContent {
		t.Fatalf("a read: %d", got)
	}
	if got := status(http.MethodPost, "/internal/sim/events", []byte(`{}`), false); got != http.StatusUnauthorized {
		t.Fatalf("unsigned: %d", got)
	}
	if code := status(http.MethodPost, "/internal/sim/events?x=1", []byte(`{"a":1}`), true); code != http.StatusNoContent || got != `{"a":1}` {
		t.Fatalf("signed: %d, body %q", code, got)
	}
}
