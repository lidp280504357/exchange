package backends

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/skill/exchange/internal/admin/ports"
)

// Platform implements ports.Platform on instrument-service's platform
// profile (INSTRUMENT_SERVICE_URL) and ledger-service's welcome credits
// (LEDGER_SERVICE_URL), design 2026-10-04 §4.1–4.2.
type Platform struct {
	REST
	Instruments string
	Ledger      string
}

// Profile returns the platform's profile with who last changed it.
func (p Platform) Profile(ctx context.Context) (json.RawMessage, error) {
	return p.do(ctx, http.MethodGet, p.Instruments+"/internal/platform/profile", nil, nil)
}

// UpdateProfile replaces the profile (all but the images and the credits).
func (p Platform) UpdateProfile(ctx context.Context, write json.RawMessage, actor, reason string) (json.RawMessage, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(write, &body); err != nil {
		return nil, err
	}
	body["actor"], _ = json.Marshal(actor)
	body["reason"], _ = json.Marshal(reason)
	return p.do(ctx, http.MethodPut, p.Instruments+"/internal/platform/profile", body, nil)
}

// PutImage uploads one of the platform's images.
func (p Platform) PutImage(ctx context.Context, kind, data, mime, actor, reason string) (json.RawMessage, error) {
	body := map[string]string{"data": data, "mime": mime, "actor": actor, "reason": reason}
	return p.do(ctx, http.MethodPut, p.Instruments+"/internal/platform/images/"+url.PathEscape(kind), body, nil)
}

// DeleteImage removes an uploaded image; the sites show their built-in one.
func (p Platform) DeleteImage(ctx context.Context, kind, actor, reason string) (json.RawMessage, error) {
	body := map[string]string{"actor": actor, "reason": reason}
	return p.do(ctx, http.MethodDelete, p.Instruments+"/internal/platform/images/"+url.PathEscape(kind), body, nil)
}

// WelcomeCredits returns what a new account gets and the master switch.
func (p Platform) WelcomeCredits(ctx context.Context) (json.RawMessage, error) {
	return p.do(ctx, http.MethodGet, p.Ledger+"/internal/ledger/settings/welcome-credits", nil, nil)
}

// SetWelcomeCredits replaces the welcome credits as of expectedVersion.
func (p Platform) SetWelcomeCredits(ctx context.Context, credits []ports.WelcomeCredit, expectedVersion int64, actor, reason string) (json.RawMessage,
	error,
) {
	if credits == nil {
		credits = []ports.WelcomeCredit{}
	}
	body := map[string]any{"credits": credits, "expected_version": expectedVersion, "actor": actor, "reason": reason}
	return p.do(ctx, http.MethodPut, p.Ledger+"/internal/ledger/settings/welcome-credits", body, nil)
}
