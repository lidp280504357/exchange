package captcha

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// roundTripperFunc lets tests fake siteverify without opening a socket.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func newVerifier(t *testing.T, rt roundTripperFunc) *Turnstile {
	t.Helper()
	v, err := NewTurnstile(TurnstileConfig{
		Secret:    "test-secret",
		Hostnames: []string{"astras.vip", "LOCALHOST"},
		Timeout:   time.Second,
		Transport: rt,
	})
	if err != nil {
		t.Fatalf("NewTurnstile: %v", err)
	}
	return v
}

func TestTurnstileVerifySuccess(t *testing.T) {
	var gotForm url.Values
	var gotURL, gotContentType string
	v := newVerifier(t, func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		gotContentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(raw))
		return jsonResponse(200, `{"success":true,"challenge_ts":"2026-09-28T10:00:00Z","hostname":"astras.vip","error-codes":[],"action":"otp_request","cdata":"abc"}`), nil
	})

	res, err := v.Verify(context.Background(), Request{Token: "tok", RemoteIP: "203.0.113.9", Action: "otp_request"})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if gotURL != TurnstileEndpoint {
		t.Fatalf("endpoint = %s", gotURL)
	}
	if gotContentType != "application/x-www-form-urlencoded" {
		t.Fatalf("content type = %s", gotContentType)
	}
	if res.Hostname != "astras.vip" || res.Action != "otp_request" || res.CData != "abc" || res.ChallengeAt.IsZero() {
		t.Fatalf("unexpected result: %+v", res)
	}
	for k, want := range map[string]string{"secret": "test-secret", "response": "tok", "remoteip": "203.0.113.9"} {
		if got := gotForm.Get(k); got != want {
			t.Fatalf("form field %s = %q, want %q", k, got, want)
		}
	}
	if gotForm.Get("idempotency_key") == "" {
		t.Fatal("idempotency_key not sent")
	}
}

func TestTurnstileVerifyRejectedByProvider(t *testing.T) {
	v := newVerifier(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"success":false,"error-codes":["invalid-input-response"]}`), nil
	})
	_, err := v.Verify(context.Background(), Request{Token: "bad"})
	var verr *VerificationError
	if !errors.As(err, &verr) || verr.Reason != "invalid-input-response" {
		t.Fatalf("want VerificationError invalid-input-response, got %v", err)
	}
}

func TestTurnstileVerifyHostnameMismatch(t *testing.T) {
	v := newVerifier(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"success":true,"hostname":"evil.example","action":"otp_request"}`), nil
	})
	_, err := v.Verify(context.Background(), Request{Token: "tok", Action: "otp_request"})
	var verr *VerificationError
	if !errors.As(err, &verr) || verr.Reason != ReasonHostnameMismatch {
		t.Fatalf("want hostname-mismatch, got %v", err)
	}
}

func TestTurnstileVerifyActionMismatch(t *testing.T) {
	v := newVerifier(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"success":true,"hostname":"localhost","action":"login"}`), nil
	})
	_, err := v.Verify(context.Background(), Request{Token: "tok", Action: "otp_request"})
	var verr *VerificationError
	if !errors.As(err, &verr) || verr.Reason != ReasonActionMismatch {
		t.Fatalf("want action-mismatch, got %v", err)
	}
	// Without an expected action the provider's action is not checked.
	if _, err := v.Verify(context.Background(), Request{Token: "tok"}); err != nil {
		t.Fatalf("unexpected error without expected action: %v", err)
	}
}

func TestTurnstileVerifyProviderUnavailable(t *testing.T) {
	cases := map[string]roundTripperFunc{
		"http 502":     func(*http.Request) (*http.Response, error) { return jsonResponse(502, ``), nil },
		"network":      func(*http.Request) (*http.Response, error) { return nil, errors.New("dial tcp: refused") },
		"invalid json": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `not json`), nil },
	}
	for name, rt := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := newVerifier(t, rt).Verify(context.Background(), Request{Token: "tok"})
			var verr *VerificationError
			if !errors.As(err, &verr) || verr.Reason != ReasonProviderUnavailable {
				t.Fatalf("want provider-unavailable, got %v", err)
			}
		})
	}
}

func TestTurnstileVerifyContextCancelled(t *testing.T) {
	v := newVerifier(t, func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := v.Verify(ctx, Request{Token: "tok"})
	var verr *VerificationError
	if !errors.As(err, &verr) || verr.Reason != ReasonProviderUnavailable {
		t.Fatalf("want provider-unavailable on cancel, got %v", err)
	}
}

func TestTurnstileVerifyTokenPrechecks(t *testing.T) {
	called := false
	v := newVerifier(t, func(*http.Request) (*http.Response, error) {
		called = true
		return jsonResponse(200, `{"success":true}`), nil
	})
	if _, err := v.Verify(context.Background(), Request{Token: "  "}); !errors.Is(err, ErrMissingToken) {
		t.Fatalf("want ErrMissingToken, got %v", err)
	}
	if _, err := v.Verify(context.Background(), Request{Token: strings.Repeat("a", MaxTokenLength+1)}); !errors.Is(err, ErrTokenTooLong) {
		t.Fatalf("want ErrTokenTooLong, got %v", err)
	}
	if called {
		t.Fatal("siteverify must not be called when pre-checks fail")
	}
}

func TestNewTurnstileValidation(t *testing.T) {
	if _, err := NewTurnstile(TurnstileConfig{Hostnames: []string{"a"}}); err == nil {
		t.Fatal("missing secret must fail")
	}
	if _, err := NewTurnstile(TurnstileConfig{Secret: "s"}); err == nil {
		t.Fatal("empty hostname allowlist must fail")
	}
}

func TestLoadTurnstileConfig(t *testing.T) {
	env := map[string]string{"TURNSTILE_SECRET": " s3cret ", "TURNSTILE_HOSTNAMES": " Astras.vip, localhost ,,astras.vip "}
	cfg, err := LoadTurnstileConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("LoadTurnstileConfig: %v", err)
	}
	if cfg.Secret != "s3cret" {
		t.Fatalf("secret not trimmed: %q", cfg.Secret)
	}
	if strings.Join(cfg.Hostnames, ",") != "astras.vip,localhost" {
		t.Fatalf("hostnames = %v", cfg.Hostnames)
	}
	if _, err := LoadTurnstileConfig(func(string) string { return "" }); err == nil {
		t.Fatal("missing env must fail")
	}
}

func TestStaticVerifier(t *testing.T) {
	s := Static{Token: "dev-pass", Hostname: "localhost"}
	if _, err := s.Verify(context.Background(), Request{Token: "dev-pass", Action: "otp_request"}); err != nil {
		t.Fatalf("expected pass: %v", err)
	}
	if _, err := s.Verify(context.Background(), Request{Token: "other"}); err == nil {
		t.Fatal("expected rejection")
	}
	if _, err := (Static{}).Verify(context.Background(), Request{Token: "anything"}); err == nil {
		t.Fatal("empty Static must reject everything")
	}
}
