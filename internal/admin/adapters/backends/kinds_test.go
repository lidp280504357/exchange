package backends_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skill/exchange/internal/admin/adapters/backends"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// user-service's accounts of some kinds (L1): asked once a minute by the
// kinds, whatever their order, in lower case; then revalidated with their
// ETag; the last ones read go on while user-service does not answer, asked
// again each time; kinds never read are unavailable then, and so is an
// answer with something not a UUID (A111).
func TestKindIDs(t *testing.T) {
	const a, b = "0192a000-0000-7000-8000-00000000000a", "0192a000-0000-7000-8000-00000000000b"
	var (
		mu    sync.Mutex
		asked []string
		down  bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, r.URL.Query().Get("kind")+" "+r.Header.Get("If-None-Match"))
		switch {
		case down:
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":"COMMON_UNAVAILABLE","message":"down"}`))
		case r.URL.Query().Get("kind") == "TEST":
			_, _ = w.Write([]byte(`{"user_ids":["` + a + `","house"]}`))
		case r.Header.Get("If-None-Match") == `"v1"`:
			w.WriteHeader(http.StatusNotModified)
		default:
			w.Header().Set("ETag", `"v1"`)
			_, _ = w.Write([]byte(`{"user_ids":["` + strings.ToUpper(a) + `","` + b + `"]}`))
		}
	}))
	defer srv.Close()
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	k := backends.NewKindIDs(backends.REST{Client: srv.Client()}, srv.URL)
	k.Now = func() time.Time { return now }
	ctx := context.Background()
	read := func(step string, kinds ...string) {
		t.Helper()
		if ids, err := k.IDs(ctx, kinds); err != nil || !slices.Equal(ids, []string{a, b}) {
			t.Fatalf("%s: %v %v", step, ids, err)
		}
	}
	read("asked", "TEST", "BOT")
	read("kept a minute", "BOT", "TEST")
	now = now.Add(61 * time.Second)
	read("revalidated", "BOT", "TEST")
	now = now.Add(61 * time.Second)
	mu.Lock()
	down = true
	mu.Unlock()
	read("the last ones while user-service is down", "BOT", "TEST")
	read("asked again", "BOT", "TEST")
	if _, err := k.IDs(ctx, []string{"SYSTEM"}); apperr.From(err).Kind != apperr.KindUnavailable {
		t.Fatalf("kinds never read: %v", err)
	}
	mu.Lock()
	down = false
	mu.Unlock()
	if _, err := k.IDs(ctx, []string{"TEST"}); apperr.From(err).Kind != apperr.KindUnavailable {
		t.Fatalf("an answer with something not a UUID: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"BOT,TEST ", `BOT,TEST "v1"`, `BOT,TEST "v1"`, `BOT,TEST "v1"`, "SYSTEM ", "TEST "}
	if !slices.Equal(asked, want) {
		t.Fatalf("asked %q, want %q", asked, want)
	}
}

// The lists asking for the same kinds once they are stale share one
// request to user-service (A111).
func TestKindIDsAskOnceAtATime(t *testing.T) {
	const a = "0192a000-0000-7000-8000-00000000000a"
	var asked atomic.Int32
	started, release := make(chan struct{}, 1), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if asked.Add(1) == 1 {
			started <- struct{}{}
			<-release
		}
		_, _ = w.Write([]byte(`{"user_ids":["` + a + `"]}`))
	}))
	defer srv.Close()
	k := backends.NewKindIDs(backends.REST{Client: srv.Client()}, srv.URL)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	ask := func() {
		defer wg.Done()
		if ids, err := k.IDs(ctx, []string{"BOT"}); err != nil || !slices.Equal(ids, []string{a}) {
			errs <- fmt.Errorf("%v: %w", ids, err)
		}
	}
	wg.Add(1)
	go ask()
	<-started
	for range 4 {
		wg.Add(1)
		go ask()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if n := asked.Load(); n != 1 {
		t.Fatalf("asked %d times", n)
	}
}

// A list a service serves is a GET without a kind filter, its POST .../list
// with one (L2, L3): the accounts to keep (user_ids, none of them too) or
// to leave out (exclude_user_ids) in the body, the rest in the query.
func TestListsTakeTheKindsInTheirBody(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		asked = append(asked, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery+" "+strings.TrimSpace(string(body)))
		_, _ = w.Write([]byte(`{"items":[],"positions":[]}`))
	}))
	defer srv.Close()
	rest := backends.REST{Client: srv.Client()}
	wallet := backends.Wallet{REST: rest, Base: srv.URL}
	ctx := context.Background()
	humans, bots := ports.KindFilter{Except: []string{"b", "s"}}, ports.KindFilter{Only: []string{"b"}}
	for _, call := range []func() (json.RawMessage, error){
		func() (json.RawMessage, error) {
			return wallet.List(ctx, ports.WithdrawalQuery{Status: "ALL", Limit: 5})
		},
		func() (json.RawMessage, error) {
			return wallet.List(ctx, ports.WithdrawalQuery{Status: "ALL", Limit: 5, ByKind: humans})
		},
		func() (json.RawMessage, error) {
			return backends.WalletDeposits{Wallet: wallet}.List(ctx, ports.DepositReviewQuery{Attention: true, ByKind: bots})
		},
		func() (json.RawMessage, error) {
			return wallet.Fees(ctx, ports.FeeQuery{Status: "HELD", ByKind: ports.KindFilter{Only: []string{}}})
		},
		func() (json.RawMessage, error) {
			return backends.Derivatives{REST: rest, Base: srv.URL}.Risk(ctx, humans)
		},
		func() (json.RawMessage, error) {
			return backends.Derivatives{REST: rest, Base: srv.URL}.OpenPositions(ctx, ports.PositionQuery{Limit: 500, ByKind: bots})
		},
		func() (json.RawMessage, error) {
			return backends.Margin{REST: rest, Base: srv.URL}.Accounts(ctx, ports.MarginAccountQuery{Limit: 500, ByKind: humans})
		},
	} {
		if _, err := call(); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		"GET /internal/wallet/withdrawals?limit=5&status=ALL ",
		`POST /internal/wallet/withdrawals/list?limit=5&status=ALL {"exclude_user_ids":["b","s"]}`,
		`POST /internal/wallet/deposits/list?attention=true {"user_ids":["b"]}`,
		`POST /internal/wallet/custody/fees/list?status=HELD {"user_ids":[]}`,
		`POST /internal/derivatives/risk/list? {"exclude_user_ids":["b","s"]}`,
		`POST /internal/derivatives/positions/list?limit=500 {"user_ids":["b"]}`,
		`POST /internal/margin/accounts/list?limit=500 {"exclude_user_ids":["b","s"]}`,
	}
	if !slices.Equal(asked, want) {
		t.Fatalf("asked\n%s\nwant\n%s", strings.Join(asked, "\n"), strings.Join(want, "\n"))
	}
}
