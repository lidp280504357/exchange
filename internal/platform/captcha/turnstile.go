package captcha

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TurnstileEndpoint is Cloudflare's siteverify endpoint.
const TurnstileEndpoint = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

const defaultTurnstileTimeout = 10 * time.Second

// TurnstileConfig configures a Turnstile verifier. Secret is the widget's
// secret key; Hostnames is the allowlist checked against the hostname that
// Cloudflare reports for the solved challenge.
type TurnstileConfig struct {
	Secret    string
	Hostnames []string
	Endpoint  string
	Timeout   time.Duration
	// Transport overrides the HTTP transport; tests use it to avoid the network.
	Transport http.RoundTripper
}

// LoadTurnstileConfig reads TURNSTILE_SECRET and TURNSTILE_HOSTNAMES (comma
// separated) through getenv, so callers can pass os.Getenv or a test stub.
func LoadTurnstileConfig(getenv func(string) string) (TurnstileConfig, error) {
	cfg := TurnstileConfig{
		Secret:    strings.TrimSpace(getenv("TURNSTILE_SECRET")),
		Hostnames: splitHostnames(getenv("TURNSTILE_HOSTNAMES")),
	}
	if cfg.Secret == "" {
		return TurnstileConfig{}, errors.New("captcha: TURNSTILE_SECRET is not set")
	}
	if len(cfg.Hostnames) == 0 {
		return TurnstileConfig{}, errors.New("captcha: TURNSTILE_HOSTNAMES is not set")
	}
	return cfg, nil
}

func splitHostnames(raw string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, h := range strings.Split(raw, ",") {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			continue
		}
		if _, dup := seen[h]; dup {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	return out
}

// Turnstile verifies tokens against Cloudflare Turnstile.
type Turnstile struct {
	secret    string
	hostnames map[string]struct{}
	endpoint  string
	client    *http.Client
}

// NewTurnstile builds a verifier. It fails fast on a missing secret or an
// empty hostname allowlist so misconfiguration is caught at startup.
func NewTurnstile(cfg TurnstileConfig) (*Turnstile, error) {
	if strings.TrimSpace(cfg.Secret) == "" {
		return nil, errors.New("captcha: turnstile secret is required")
	}
	hosts := map[string]struct{}{}
	for _, h := range cfg.Hostnames {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			hosts[h] = struct{}{}
		}
	}
	if len(hosts) == 0 {
		return nil, errors.New("captcha: turnstile hostname allowlist is required")
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = TurnstileEndpoint
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTurnstileTimeout
	}
	return &Turnstile{
		secret:    cfg.Secret,
		hostnames: hosts,
		endpoint:  endpoint,
		client:    &http.Client{Timeout: timeout, Transport: cfg.Transport},
	}, nil
}

type siteverifyResponse struct {
	Success     bool     `json:"success"`
	ChallengeTS string   `json:"challenge_ts"`
	Hostname    string   `json:"hostname"`
	ErrorCodes  []string `json:"error-codes"`
	Action      string   `json:"action"`
	CData       string   `json:"cdata"`
}

// Verify redeems the token with siteverify. Tokens are single-use: a second
// call with the same token is rejected by Cloudflare with
// "timeout-or-duplicate".
func (t *Turnstile) Verify(ctx context.Context, req Request) (Result, error) {
	if err := validateToken(req.Token); err != nil {
		return Result{}, err
	}

	form := url.Values{}
	form.Set("secret", t.secret)
	form.Set("response", req.Token)
	if req.RemoteIP != "" {
		form.Set("remoteip", req.RemoteIP)
	}
	form.Set("idempotency_key", newIdempotencyKey())

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Result{}, fmt.Errorf("captcha: build siteverify request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := t.client.Do(httpReq)
	if err != nil {
		return Result{}, &VerificationError{Reason: ReasonProviderUnavailable}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, &VerificationError{Reason: ReasonProviderUnavailable}
	}

	var body siteverifyResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return Result{}, &VerificationError{Reason: ReasonProviderUnavailable}
	}
	if !body.Success {
		reason := ReasonRejected
		if len(body.ErrorCodes) > 0 {
			reason = body.ErrorCodes[0]
		}
		return Result{}, &VerificationError{Reason: reason, Codes: body.ErrorCodes}
	}
	if _, ok := t.hostnames[strings.ToLower(body.Hostname)]; !ok {
		return Result{}, &VerificationError{Reason: ReasonHostnameMismatch}
	}
	if req.Action != "" && body.Action != req.Action {
		return Result{}, &VerificationError{Reason: ReasonActionMismatch}
	}

	res := Result{Hostname: body.Hostname, Action: body.Action, CData: body.CData}
	if ts, err := time.Parse(time.RFC3339, body.ChallengeTS); err == nil {
		res.ChallengeAt = ts
	}
	return res, nil
}

func newIdempotencyKey() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
