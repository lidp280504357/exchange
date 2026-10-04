package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/wallet/application"
	"github.com/skill/exchange/internal/wallet/domain"
)

// GET /v1/wallet/limits (review BA): signed in only; the limits render
// with full_limits_at only while an authenticator app settles.
func TestTheLimitsRoute(t *testing.T) {
	r := chi.NewRouter()
	(&Handler{Svc: &application.Service{}}).Routes(r)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/wallet/limits", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("without a user: %d", w.Code)
	}

	full, at := domain.FullLimits(), time.Date(2026, 10, 5, 1, 0, 0, 0, time.UTC)
	v := application.LimitsView{
		Limits: domain.Limits{Daily: decimal.NewFromInt(400), Monthly: decimal.NewFromInt(4000)}, Full: full,
		UsedToday: decimal.RequireFromString("12.5"), UsedThisMonth: decimal.RequireFromString("12.5"), Identities: 2, TOTP: true,
		Settling: 24 * time.Hour, FullAt: at,
	}
	raw, _ := json.Marshal(LimitsJSONOf(v))
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["daily_limit"] != "400" || got["full_daily_limit"] != "2000" || got["used_today"] != "12.5" || got["totp_settling_hours"] != 24.0 ||
		got["full_limits_at"] != "2026-10-05T01:00:00.000Z" || got["identities"] != 2.0 || got["totp_enabled"] != true {
		t.Fatalf("settling %s", raw)
	}
	v.FullAt, v.Limits = time.Time{}, full
	raw, _ = json.Marshal(LimitsJSONOf(v))
	if err := json.Unmarshal(raw, &got); err != nil || got["full_limits_at"] != nil || got["daily_limit"] != "2000" {
		t.Fatalf("settled %s %v", raw, err)
	}
}
