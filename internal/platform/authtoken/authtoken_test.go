package authtoken

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func seed(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestIssueAndVerify(t *testing.T) {
	s, err := NewSigner(seed(t), "k1")
	if err != nil {
		t.Fatal(err)
	}
	fetches := 0
	v := NewVerifier(func(context.Context) (JWKS, error) {
		fetches++
		return s.JWKS(), nil
	})
	now := time.Now()
	tok, exp, err := s.Issue("u-1", "s-1", ScopeFull, now)
	if err != nil || !exp.Equal(now.Add(AccessTTL)) {
		t.Fatalf("issue: %v %v", exp, err)
	}
	c, err := v.Verify(context.Background(), tok, now.Add(time.Minute))
	if err != nil || c.Subject != "u-1" || c.SessionID != "s-1" || c.Scope != ScopeFull {
		t.Fatalf("verify: %+v %v", c, err)
	}
	if _, err := v.Verify(context.Background(), tok, now.Add(2*time.Minute)); err != nil || fetches != 1 {
		t.Fatalf("keys must be cached: %v, fetched %d times", err, fetches)
	}
	if _, err := v.Verify(context.Background(), tok, now.Add(AccessTTL+time.Second)); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired: %v", err)
	}
}

func TestVerifyRejectsForgeries(t *testing.T) {
	good, _ := NewSigner(seed(t), "k1")
	evil, _ := NewSigner(seed(t), "k1") // same kid, different key
	v := NewVerifier(func(context.Context) (JWKS, error) { return good.JWKS(), nil })
	now := time.Now()

	forged, _, _ := evil.Issue("u-1", "s-1", ScopeFull, now)
	if _, err := v.Verify(context.Background(), forged, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("forged signature: %v", err)
	}
	valid, _, _ := good.Issue("u-1", "s-1", ScopeFull, now)
	parts := strings.Split(valid, ".")
	unsigned := `eyJhbGciOiJub25lIiwia2lkIjoiazEifQ.` + parts[1] + `.`
	if _, err := v.Verify(context.Background(), unsigned, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("alg=none: %v", err)
	}
	if _, err := v.Verify(context.Background(), "garbage", now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("garbage: %v", err)
	}
	noSession, _, _ := good.Issue("u-1", "", ScopeFull, now)
	if _, err := v.Verify(context.Background(), noSession, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tokens need a session: %v", err)
	}
}

func TestKeyRotation(t *testing.T) {
	old, _ := NewSigner(seed(t), "k1")
	current := old
	v := NewVerifier(func(context.Context) (JWKS, error) { return current.JWKS(), nil })
	now := time.Now()
	tok, _, _ := old.Issue("u", "s", ScopeFull, now)
	if _, err := v.Verify(context.Background(), tok, now); err != nil {
		t.Fatal(err)
	}
	rotated, _ := NewSigner(seed(t), "k2")
	current = rotated
	fresh, _, _ := rotated.Issue("u", "s", ScopeFull, now)
	// An unknown kid triggers a refetch, but at most once a minute.
	if _, err := v.Verify(context.Background(), fresh, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("rotated key must be picked up: %v", err)
	}
}

func TestNewSignerValidates(t *testing.T) {
	if _, err := NewSigner([]byte("short"), "k"); err == nil {
		t.Fatal("seed length")
	}
	if _, err := NewSigner(seed(t), ""); err == nil {
		t.Fatal("kid required")
	}
}

func TestUnreachableKeysAreNotABadToken(t *testing.T) {
	s, _ := NewSigner(seed(t), "k1")
	down := true
	fetches := 0
	v := NewVerifier(func(context.Context) (JWKS, error) {
		fetches++
		if down {
			return JWKS{}, errors.New("connection refused")
		}
		return s.JWKS(), nil
	})
	now := time.Now()
	tok, _, _ := s.Issue("u", "s", ScopeFull, now)
	if _, err := v.Verify(context.Background(), tok, now); !errors.Is(err, ErrKeysUnavailable) {
		t.Fatalf("keys down: %v", err)
	}
	// Failed fetches are retried after retryGap, not on every request.
	if _, err := v.Verify(context.Background(), tok, now.Add(time.Second)); !errors.Is(err, ErrKeysUnavailable) || fetches != 1 {
		t.Fatalf("retry too soon: %v, %d fetches", err, fetches)
	}
	down = false
	if _, err := v.Verify(context.Background(), tok, now.Add(retryGap+time.Second)); err != nil || fetches != 2 {
		t.Fatalf("recovered: %v, %d fetches", err, fetches)
	}
	// Once keys are known, a failed refresh keeps using them.
	down = true
	if _, err := v.Verify(context.Background(), tok, now.Add(refreshAfter+2*time.Second)); err != nil {
		t.Fatalf("stale keys must still verify: %v", err)
	}
}

func TestHTTPKeys(t *testing.T) {
	s, _ := NewSigner(seed(t), "k1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/jwks" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(s.JWKS())
	}))
	defer srv.Close()
	set, err := HTTPKeys(srv.Client(), srv.URL+"/internal/jwks")(context.Background())
	if err != nil || len(set.Keys) != 1 || set.Keys[0].Kid != "k1" {
		t.Fatalf("keys: %+v %v", set, err)
	}
	if _, err := HTTPKeys(srv.Client(), srv.URL+"/missing")(context.Background()); err == nil {
		t.Fatal("a 404 must fail")
	}
}
