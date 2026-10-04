package gateway

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/platform/ratelimit"
)

// Upstreams are the proxies to the services behind the gateway.
type Upstreams struct {
	Auth         http.Handler
	User         http.Handler
	Notification http.Handler
	Instrument   http.Handler
	Ledger       http.Handler
	Trading      http.Handler
	Market       http.Handler
	Wallet       http.Handler
	Derivatives  http.Handler
	// DevInbox is notification-service's mock-provider inbox; nil in
	// production, where it is never routed.
	DevInbox http.Handler
}

// Guards are the gateway's cross-cutting checks; nil Limits, Idempotency,
// WS or Registration leave that part out (tests).
type Guards struct {
	Authn       *Authenticator
	Limits      *Limits
	Idempotency *Idempotency
	WS          http.Handler
	// Registration refuses sign-ups while the platform closes them.
	Registration *Registration
}

// signUp is the registration gate on the sign-up requests.
func (g Guards) signUp() []middleware {
	if g.Registration == nil {
		return nil
	}
	return []middleware{g.Registration.Middleware}
}

// publicAuthPaths are the auth flows that produce tokens; they take no
// access token (a stale one sent along must not break a refresh).
var publicAuthPaths = []string{
	"/otp/verify",
	"/terms",
	"/register/complete",
	"/login/password",
	"/login/complete",
	"/login/challenge",
	"/password/reset/request",
	"/password/reset/complete",
	"/token/refresh",
}

type middleware = func(http.Handler) http.Handler

func (g Guards) byIP(rules ...ratelimit.Rule) []middleware {
	if g.Limits == nil {
		return nil
	}
	return []middleware{g.Limits.ByIP(rules...)}
}

func (g Guards) signedIn(required bool, rules ...ratelimit.Rule) []middleware {
	m := []middleware{g.Authn.Optional}
	if required {
		m[0] = g.Authn.Required
	}
	if g.Limits != nil {
		m = append(m, g.Limits.ByUser(rules...))
	}
	if g.Idempotency != nil {
		m = append(m, g.Idempotency.Middleware)
	}
	return m
}

// Mount routes the /v1 API: every request counts against its IP; token
// flows are public with a tighter IP quota; everything else needs a
// valid access token and counts against the user, and signed-in writes
// honor Idempotency-Key. Paths not listed below require a token.
func Mount(r chi.Router, g Guards, up Upstreams) {
	r.Group(func(r chi.Router) {
		r.Use(g.byIP(RuleIP)...)
		if g.WS != nil {
			r.Get("/v1/ws", g.WS.ServeHTTP) // authenticates inside the protocol
		}
		r.Route("/v1/auth", func(r chi.Router) {
			for _, p := range publicAuthPaths {
				m := g.byIP(RuleIPAuth)
				if p == "/register/complete" {
					m = append(m, g.signUp()...)
				}
				r.With(m...).Handle(p, up.Auth)
			}
			// Anonymous for registration and login codes, signed in for
			// step-up and identity binding.
			r.With(append(append(g.byIP(RuleIPAuth), g.signedIn(false, RuleUser)...), g.signUp()...)...).Handle("/otp/request", up.Auth)
			r.With(g.signedIn(true, RuleUser)...).Handle("/*", up.Auth)
		})
		// The platform's profile and its images (design 2026-10-04 §4.1),
		// and the mobile site's manifest built from it.
		r.Get("/v1/platform/profile", up.Instrument.ServeHTTP)
		r.Get("/v1/platform/images/{kind}", up.Instrument.ServeHTTP)
		r.Get("/manifest.webmanifest", up.Instrument.ServeHTTP)
		// Public reference data.
		r.Handle("/v1/market/assets", up.Instrument)
		r.Handle("/v1/market/assets/*", up.Instrument) // logos
		r.Handle("/v1/market/pairs", up.Instrument)
		r.Handle("/v1/market/pairs/*", up.Instrument)
		r.Handle("/v1/market/contracts", up.Instrument)
		r.Handle("/v1/market/contracts/*", up.Instrument)
		// Public announcements, help articles, legal pages and home-page
		// blocks (written in the admin console, design 2026-10-02 §4.5 and
		// 2026-10-04 §4.4); reads only.
		for _, p := range []string{
			"/v1/announcements", "/v1/announcements/{slug}", "/v1/help", "/v1/help/{slug}", "/v1/legal", "/v1/legal/{slug}",
			"/v1/home", "/v1/home/{slug}",
		} {
			r.Get(p, up.Notification.ServeHTTP)
		}
		// Public market data; the static routes above win over {symbol}.
		if up.Market != nil {
			r.Handle("/v1/market/tickers", up.Market)
			r.Handle("/v1/market/summary", up.Market)
			r.Handle("/v1/market/sparklines", up.Market)
			r.Handle("/v1/market/{symbol}/*", up.Market)
		}

		private := r.With(g.signedIn(true, RuleUser)...)
		private.Handle("/v1/user/*", up.User)
		r.With(g.signedIn(true, RuleUser, RuleTransfer)...).Post("/v1/account/transfers", up.Ledger.ServeHTTP)
		private.Handle("/v1/account/*", up.Ledger)
		private.Handle("/v1/notifications", up.Notification)
		private.Handle("/v1/notifications/*", up.Notification)
		if up.Wallet != nil {
			// The custodian's callbacks carry their own signature (ADR-0011).
			r.Post("/v1/wallet/callbacks/{provider}", up.Wallet.ServeHTTP)
			private.Handle("/v1/wallet/*", up.Wallet)
		}
		if up.Trading != nil {
			orders := r.With(g.signedIn(true, RuleUser, RuleOrder)...)
			orders.Handle("/v1/orders", up.Trading)
			orders.Handle("/v1/orders/*", up.Trading)
			orders.Handle("/v1/fills", up.Trading)
		}
		if up.Derivatives != nil {
			r.With(g.signedIn(true, RuleUser, RuleOrder)...).Handle("/v1/derivatives/orders", up.Derivatives)
			r.With(g.signedIn(true, RuleUser, RuleOrder)...).Handle("/v1/derivatives/orders/*", up.Derivatives)
			r.With(g.signedIn(true, RuleUser, RuleOrder)...).Handle("/v1/derivatives/conditional-orders", up.Derivatives)
			r.With(g.signedIn(true, RuleUser, RuleOrder)...).Handle("/v1/derivatives/conditional-orders/*", up.Derivatives)
			private.Handle("/v1/derivatives/*", up.Derivatives)
		}
		if up.DevInbox != nil {
			r.Handle("/v1/dev/*", up.DevInbox)
		}
	})
}
