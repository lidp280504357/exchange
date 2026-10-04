package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
)

// ErrRegistrationClosed refuses a sign-up while the platform profile says
// sign-ups are closed (design 2026-10-04 §4.3).
var ErrRegistrationClosed = apperr.New(apperr.KindForbidden, "AUTH_REGISTRATION_CLOSED", "sign-ups are closed")

// registrationEvery is how often the gate reads the platform profile.
const registrationEvery = 15 * time.Second

// peekLimit bounds the part of a code request read for its scene.
const peekLimit = 64 << 10

// Registration closes sign-ups while the platform profile's registration
// is CLOSED: POST /v1/auth/register/complete and the REGISTER codes of
// POST /v1/auth/otp/request get 403 AUTH_REGISTRATION_CLOSED. It reads
// instrument-service's profile every 15 seconds and keeps the last state
// it read; until a first read sign-ups stay open, so an outage of the
// profile never closes them.
type Registration struct {
	// ProfileURL is instrument-service's GET /v1/platform/profile.
	ProfileURL string
	HTTP       *http.Client
	Log        *slog.Logger

	closed atomic.Bool
}

// Closed reports whether sign-ups are closed, as last read.
func (g *Registration) Closed() bool { return g.closed.Load() }

// Run reads the profile now and every 15 seconds until ctx ends.
func (g *Registration) Run(ctx context.Context) error {
	for {
		if err := g.read(ctx); err != nil && ctx.Err() == nil && g.Log != nil {
			g.Log.WarnContext(ctx, "registration status not read from the platform profile", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(registrationEvery):
		}
	}
}

func (g *Registration) read(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.ProfileURL, nil)
	if err != nil {
		return err
	}
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var p struct {
		Registration struct {
			Status string `json:"status"`
		} `json:"registration"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return err
	}
	g.closed.Store(p.Registration.Status == "CLOSED")
	return nil
}

// Middleware refuses the sign-up requests while sign-ups are closed: a
// code request is let through when it is not for REGISTER (review BD:
// read as auth-service reads it).
func (g *Registration) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.Closed() || r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/otp/request") && !registerCode(r) {
			next.ServeHTTP(w, r)
			return
		}
		httpx.WriteError(w, r, ErrRegistrationClosed)
	})
}

// registerCode reads a code request's scene, as auth-service does (upper
// case, spaces trimmed), and puts the body back for the upstream. A body
// it cannot read whole (above peekLimit; auth-service takes up to 1 MiB) or
// as JSON counts as a REGISTER code: while sign-ups are closed the gate
// lets through only what it can tell is not one (review BD).
func registerCode(r *http.Request) bool {
	buf, err := io.ReadAll(io.LimitReader(r.Body, peekLimit+1))
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(buf), r.Body))
	if err != nil || len(buf) > peekLimit {
		return true
	}
	var body struct {
		Scene string `json:"scene"`
	}
	if json.Unmarshal(buf, &body) != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(body.Scene), "REGISTER")
}
