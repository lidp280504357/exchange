package gateway

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Upstreams are the proxies to the services behind the gateway.
type Upstreams struct {
	Auth         http.Handler
	User         http.Handler
	Notification http.Handler
	Instrument   http.Handler
	Ledger       http.Handler
	// DevInbox is notification-service's mock-provider inbox; nil in
	// production, where it is never routed.
	DevInbox http.Handler
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

// Mount routes the /v1 API through authentication to the services. Every
// path not listed as public or optional requires a valid access token.
func Mount(r chi.Router, authn *Authenticator, up Upstreams) {
	r.Route("/v1/auth", func(r chi.Router) {
		for _, p := range publicAuthPaths {
			r.Handle(p, up.Auth)
		}
		// Anonymous for registration and login codes, signed in for
		// step-up and identity binding.
		r.With(authn.Optional).Handle("/otp/request", up.Auth)
		r.With(authn.Required).Handle("/*", up.Auth)
	})
	// Public reference data.
	r.Handle("/v1/market/assets", up.Instrument)
	r.Handle("/v1/market/pairs", up.Instrument)
	r.Handle("/v1/market/pairs/*", up.Instrument)
	r.With(authn.Required).Handle("/v1/user/*", up.User)
	r.With(authn.Required).Handle("/v1/account/*", up.Ledger)
	r.With(authn.Required).Handle("/v1/notifications", up.Notification)
	r.With(authn.Required).Handle("/v1/notifications/*", up.Notification)
	if up.DevInbox != nil {
		r.Handle("/v1/dev/*", up.DevInbox)
	}
}
