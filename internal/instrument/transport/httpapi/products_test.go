package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/platform/flags"
)

type productFlags map[string]flags.Flag

func (p productFlags) Get(key string) (flags.Flag, bool) {
	f, ok := p[key]
	return f, ok
}

func getProducts(t *testing.T, p productFlags, ifNoneMatch string) (*httptest.ResponseRecorder, map[string]ProductLineJSON) {
	t.Helper()
	r := chi.NewRouter()
	(&Handler{Products: p}).Routes(r)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/platform/products", nil)
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out map[string]ProductLineJSON
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return w, out
}

// TestProducts: the product lines (design 2026-10-07, product switches,
// K0) are open unless their flag is stored and off, with the time it was
// closed; the ETag is the flags' versions and answers 304 strong or weak.
func TestProducts(t *testing.T) {
	w, out := getProducts(t, productFlags{}, "")
	if w.Code != http.StatusOK || w.Header().Get("ETag") != `"0-0-0"` || len(out) != 3 ||
		!out["spot"].Enabled || !out["usdt_m"].Enabled || !out["coin_m"].Enabled || out["spot"].ClosedAt != nil {
		t.Fatalf("none stored: %d %s %+v", w.Code, w.Header().Get("ETag"), out)
	}
	closedAt := time.Date(2026, 10, 7, 8, 1, 2, 0, time.UTC)
	p := productFlags{
		flags.KeyProductSpot:  {Key: flags.KeyProductSpot, Enabled: false, Version: 3, UpdatedAt: closedAt},
		flags.KeyProductUSDTM: {Key: flags.KeyProductUSDTM, Enabled: true, Version: 1},
	}
	w, out = getProducts(t, p, "")
	if w.Code != http.StatusOK || w.Header().Get("ETag") != `"3-1-0"` || out["spot"].Enabled || out["spot"].ClosedAt == nil ||
		*out["spot"].ClosedAt != "2026-10-07T08:01:02.000Z" || !out["usdt_m"].Enabled || out["usdt_m"].ClosedAt != nil || !out["coin_m"].Enabled {
		t.Fatalf("spot closed: %d %s %+v", w.Code, w.Header().Get("ETag"), out)
	}
	if w.Header().Get("Cache-Control") != "public, max-age=30" {
		t.Fatalf("cache: %s", w.Header().Get("Cache-Control"))
	}
	for _, tag := range []string{`"3-1-0"`, `W/"3-1-0"`} {
		if w, _ := getProducts(t, p, tag); w.Code != http.StatusNotModified {
			t.Fatalf("If-None-Match %s: %d", tag, w.Code)
		}
	}
	if w, _ := getProducts(t, p, `"3-0-0"`); w.Code != http.StatusOK {
		t.Fatalf("an old tag: %d", w.Code)
	}
}
