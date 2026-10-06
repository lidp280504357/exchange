package backends

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/svcsign"
)

// TestMarketMakerCaps checks how the console reads and changes HOUSE's
// caps on market-maker's internal API (review C45, A69): the paths, the
// change's body signed with the console's key (review C47) and not sent
// without it, and market-maker's refusal as its apperr.
func TestMarketMakerCaps(t *testing.T) {
	secret := strings.Repeat("k", svcsign.MinSecret)
	verifier := &svcsign.Verifier{Keys: map[string][]byte{"admin": []byte(secret)}}
	type call struct {
		line, signedBy string
		body           map[string]any
	}
	var got []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := call{line: r.Method + " " + r.URL.RequestURI()}
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &c.body)
		}
		if h := r.Header.Get(svcsign.Header); h != "" {
			k, err := verifier.Verify(r.Method, r.URL.RequestURI(), h, raw)
			if err != nil {
				t.Errorf("a bad signature: %v", err)
			}
			c.signedBy = k
		}
		got = append(got, c)
		switch {
		case r.Method == http.MethodPut && c.body["version"] == float64(1):
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"HOUSE_CAPS_VERSION","message":"the caps changed meanwhile: read them again","details":{"version":"2"}}`))
		case r.URL.Path == "/internal/house/caps/changes":
			_, _ = w.Write([]byte(`{"items":[]}`))
		default:
			_, _ = w.Write([]byte(`{"level":"1","version":3}`))
		}
	}))
	defer srv.Close()
	m := MarketMaker{REST: REST{Client: &http.Client{Timeout: 5 * time.Second}}, Base: srv.URL}
	ctx := context.Background()

	if raw, err := m.HouseCaps(ctx); err != nil || string(raw) != `{"level":"1","version":3}` {
		t.Fatalf("read %s %v", raw, err)
	}
	if raw, err := m.HouseCapsChanges(ctx, 100); err != nil || string(raw) != `{"items":[]}` {
		t.Fatalf("changes %s %v", raw, err)
	}
	w := ports.HouseCapsWrite{
		Caps: map[string]string{"safety": "1001"}, Version: 2, Actor: "fin@example.com", Approver: "boss@example.com",
		ApprovalID: "0192a000-0000-7000-8000-0000000000a9", Reason: "a larger margin",
	}
	if _, err := m.SetHouseCaps(ctx, w); !apperr.Is(err, apperr.CodeUnavailable) {
		t.Fatalf("without the key: %v", err)
	}
	m.Signer = svcsign.Client{KeyID: "admin", Secret: []byte(secret)}
	if _, err := m.SetHouseCaps(ctx, w); err != nil {
		t.Fatalf("signed: %v", err)
	}
	w.Version = 1
	if _, err := m.SetHouseCaps(ctx, w); !apperr.Is(err, "HOUSE_CAPS_VERSION") {
		t.Fatalf("market-maker's refusal: %v", err)
	}
	if len(got) != 4 || got[0].line != "GET /internal/house/caps" || got[1].line != "GET /internal/house/caps/changes?limit=100" {
		t.Fatalf("calls %+v", got)
	}
	for i, c := range got[2:3] {
		b := c.body
		if c.line != "PUT /internal/house/caps" || b["safety"] != "1001" || b["version"] != float64(2) || b["actor"] != "fin@example.com" ||
			b["approver"] != "boss@example.com" || b["approval_id"] != w.ApprovalID || b["reason"] != "a larger margin" || len(b) != 6 {
			t.Fatalf("change %d %+v", i, c)
		}
	}
	if got[2].signedBy != "admin" || got[3].signedBy != "admin" {
		t.Fatalf("signatures %q %q", got[2].signedBy, got[3].signedBy)
	}
}
