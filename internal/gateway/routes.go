package gateway

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/lidp280504357/exchange/internal/platform/ratelimit"
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

// Guards are the gateway's cross-cutting checks; nil Limits, Idempotency
// or WS leave that part out (tests).
type Guards struct {
	Authn       *Authenticator
	Limits      *Limits
	Idempotency *Idempotency
	WS          http.Handler
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
				r.With(g.byIP(RuleIPAuth)...).Handle(p, up.Auth)
			}
			// Anonymous for registration and login codes, signed in for
			// step-up and identity binding.
			r.With(append(g.byIP(RuleIPAuth), g.signedIn(false, RuleUser)...)...).Handle("/otp/request", up.Auth)
			r.With(g.signedIn(true, RuleUser)...).Handle("/*", up.Auth)
		})
		// Public reference data.
		r.Handle("/v1/market/assets", up.Instrument)
		r.Handle("/v1/market/pairs", up.Instrument)
		r.Handle("/v1/market/pairs/*", up.Instrument)
		r.Handle("/v1/market/contracts", up.Instrument)
		r.Handle("/v1/market/contracts/*", up.Instrument)
		// Public market data; the static routes above win over {symbol}.
		if up.Market != nil {
			r.Handle("/v1/market/tickers", up.Market)
			r.Handle("/v1/market/{symbol}/*", up.Market)
		}

		private := r.With(g.signedIn(true, RuleUser)...)
		private.Handle("/v1/user/*", up.User)
		r.With(g.signedIn(true, RuleUser, RuleTransfer)...).Post("/v1/account/transfers", up.Ledger.ServeHTTP)
		private.Handle("/v1/account/*", up.Ledger)
		private.Handle("/v1/notifications", up.Notification)
		private.Handle("/v1/notifications/*", up.Notification)
		if up.Wallet != nil {
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
			private.Handle("/v1/derivatives/*", up.Derivatives)
		}
		if up.DevInbox != nil {
			r.Handle("/v1/dev/*", up.DevInbox)
		}
	})
}
