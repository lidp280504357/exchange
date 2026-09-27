// Package resend sends mail through Resend (https://resend.com), the
// primary mail provider of the test environment.
package resend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/lidp280504357/exchange/internal/notification/domain"
)

// DefaultEndpoint is Resend's send-email API.
const DefaultEndpoint = "https://api.resend.com/emails"

// Config holds the provider settings.
type Config struct {
	// APIKey (RESEND_API_KEY); without it the provider is not used.
	APIKey string `koanf:"resend_api_key"`
	// From is the sender, on a domain verified in Resend (MAIL_FROM).
	From string `koanf:"mail_from"`
	// Endpoint overrides DefaultEndpoint in tests.
	Endpoint string `koanf:"resend_endpoint"`
}

// Provider is a ports.Provider for mail.
type Provider struct {
	cfg    Config
	client *http.Client
}

// New returns a provider; transport may be nil.
func New(cfg Config, transport http.RoundTripper) *Provider {
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	return &Provider{cfg: cfg, client: &http.Client{Timeout: 10 * time.Second, Transport: transport}}
}

// Name identifies the provider in delivery records and metrics.
func (p *Provider) Name() string { return "resend" }

type request struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
}

// Send posts the mail and classifies failures: bad addresses and
// credentials are final, rate limits and server errors are retried.
func (p *Provider) Send(ctx context.Context, m domain.Message) (string, error) {
	body, err := json.Marshal(request{From: p.cfg.From, To: []string{m.To}, Subject: m.Subject, Text: m.Text})
	if err != nil {
		return "", &domain.SendError{Class: domain.FailureUnknown, Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return "", &domain.SendError{Class: domain.FailureUnknown, Err: err}
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	if m.IdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", m.IdempotencyKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", &domain.SendError{Class: domain.FailureTimeout, Retryable: true, Err: err}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		var ok struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &ok); err != nil || ok.ID == "" {
			return "", &domain.SendError{Class: domain.FailureUnknown, Retryable: true, Err: fmt.Errorf("unexpected response %q", raw)}
		}
		return ok.ID, nil
	case http.StatusTooManyRequests:
		return "", &domain.SendError{Class: domain.FailureRejected, Retryable: true, Err: errors.New("rate limited")}
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return "", &domain.SendError{Class: domain.FailureInvalidTarget, Err: fmt.Errorf("status %d: %s", resp.StatusCode, raw)}
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", &domain.SendError{Class: domain.FailureRejected, Err: fmt.Errorf("status %d: %s", resp.StatusCode, raw)}
	default:
		return "", &domain.SendError{Class: domain.FailureUnknown, Retryable: true, Err: fmt.Errorf("status %d: %s", resp.StatusCode, raw)}
	}
}
