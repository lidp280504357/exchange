package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skill/exchange/internal/platform/httpx"
	"github.com/skill/exchange/internal/platform/svcsign"
)

func TestOnlyTheAdminKeyNamesAnApprover(t *testing.T) {
	ops := []byte("0123456789abcdef0123456789abcdef")
	admin := []byte("fedcba9876543210fedcba9876543210")
	v := &svcsign.Verifier{Keys: map[string][]byte{KeyOps: ops, KeyAdmin: admin}}
	srv := httptest.NewServer(v.Changes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ApprovedBy string `json:"approved_by"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, err := approver(r, body.ApprovedBy); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})))
	defer srv.Close()
	post := func(key string, secret []byte, body string) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/internal/sim/events", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := svcsign.Client{KeyID: key, Secret: secret}.Do(req, []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var e struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return resp.StatusCode, e.Code
	}
	for _, c := range []struct {
		key    string
		secret []byte
		body   string
		status int
		code   string
	}{
		{KeyOps, ops, `{"actor":"a"}`, http.StatusCreated, ""},
		{KeyOps, ops, `{"actor":"a","approved_by":"b"}`, http.StatusForbidden, "SIM_APPROVAL_NEEDS_ADMIN"},
		{KeyAdmin, admin, `{"actor":"a","approved_by":"b"}`, http.StatusCreated, ""},
		{KeyAdmin, ops, `{"actor":"a","approved_by":"b"}`, http.StatusUnauthorized, "SERVICE_UNSIGNED"},
	} {
		if status, code := post(c.key, c.secret, c.body); status != c.status || code != c.code {
			t.Fatalf("%s %s: %d %s, want %d %s", c.key, c.body, status, code, c.status, c.code)
		}
	}
	if !strings.Contains(ErrApprovalNeedsAdmin.Error(), "admin console") {
		t.Fatal(ErrApprovalNeedsAdmin)
	}
}
