// Package httpapi serves notification-service's REST endpoints.
package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/lidp280504357/exchange/internal/notification/adapters/mock"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
)

// DevInbox serves GET /v1/dev/messages?target=...: what the mock providers
// "sent" to target, newest first. Mounted only outside production, so that
// testers and end-to-end tests can read codes sent by SMS or to test mail
// domains.
func DevInbox(inbox *mock.Provider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("target")))
		if target == "" {
			httpx.WriteError(w, r, apperr.Invalid("target is required"))
			return
		}
		limit := 5
		if s := r.URL.Query().Get("limit"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 || n > 50 {
				httpx.WriteError(w, r, apperr.Invalid("limit must be 1 to 50"))
				return
			}
			limit = n
		}
		msgs, err := inbox.Messages(r.Context(), target, limit)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"messages": msgs})
	}
}
