package httpapi_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/skill/exchange/internal/derivatives/application"
	"github.com/skill/exchange/internal/derivatives/domain"
	"github.com/skill/exchange/internal/derivatives/ports"
	"github.com/skill/exchange/internal/derivatives/transport/httpapi"
)

// flatStore is just enough of a store for a flatten: a user without
// positions, with one console order the engine never finishes (working)
// or none. What a flatten does not touch is left to the embedded nil
// interfaces.
type flatStore struct {
	ports.Store
	working *domain.Order
}

func (s flatStore) Tx(_ context.Context, fn func(ports.Repos) error) error {
	return fn(flatRepos{working: s.working})
}

func (s flatStore) Read() ports.Repos { return flatRepos{working: s.working} }

type flatRepos struct {
	ports.Repos
	working *domain.Order
}

func (flatRepos) LockUser(context.Context, string) error { return nil }

func (flatRepos) CrossLiquidations() ports.CrossLiquidationRepo { return flatCross{} }

func (flatRepos) Positions() ports.PositionRepo { return flatPositions{} }

func (flatRepos) Conditionals() ports.ConditionalRepo { return flatConds{} }

func (r flatRepos) Orders() ports.OrderRepo { return flatOrders{working: r.working} }

func (flatRepos) Emit(context.Context, string, proto.Message, string, string) error { return nil }

type (
	flatCross     struct{ ports.CrossLiquidationRepo }
	flatPositions struct{ ports.PositionRepo }
	flatConds     struct{ ports.ConditionalRepo }
	flatOrders    struct {
		ports.OrderRepo
		working *domain.Order
	}
)

func (flatCross) AllOpen(context.Context) ([]domain.CrossLiquidation, error) { return nil, nil }

func (flatPositions) OfUser(context.Context, string, string) ([]domain.Position, error) {
	return nil, nil
}

func (flatConds) OfUser(context.Context, string, string, string, string, int) ([]domain.Conditional, error) {
	return nil, nil
}

func (o flatOrders) Active(context.Context, string, string) ([]domain.Order, error) {
	if o.working == nil {
		return nil, nil
	}
	return []domain.Order{*o.working}, nil
}

func (o flatOrders) Get(_ context.Context, id string) (domain.Order, error) {
	if o.working == nil || o.working.ID != id {
		return domain.Order{}, domain.ErrOrderNotFound
	}
	return *o.working, nil
}

// flattenServer serves the internal routes over a real server whose
// timeouts are those of the platform's scaled down: a read timeout of
// 200 ms.
func flattenServer(t *testing.T, svc *application.Service) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	(&httpapi.Handler{Svc: svc}).InternalRoutes(r)
	srv := httptest.NewUnstartedServer(r)
	srv.Config.ReadTimeout, srv.Config.WriteTimeout = 200*time.Millisecond, 200*time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func flattenCall(t *testing.T, srv *httptest.Server, user, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/internal/derivatives/users/"+user+"/flatten",
		strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("%s: %v", user, err)
	}
	return resp.StatusCode, out
}

// The flatten endpoint (L4b): a user with nothing is 200, all zero and
// complete; no UUID or no reason is 400; HOUSE is 422. With an order the
// engine never finishes the call outlasts the server's read and write
// timeouts (review C79 ①: its context lives past the read timeout, the
// handler moves the write deadline) and answers what is left, complete
// false.
func TestTheFlattenEndpoint(t *testing.T) {
	house := uuid.NewString()
	svc := &application.Service{
		Store: flatStore{}, HouseUser: house, Log: slog.New(slog.DiscardHandler), Now: time.Now,
		Metrics: application.NewMetrics(prometheus.NewRegistry()), FlattenWait: 300 * time.Millisecond, FlattenPoll: 50 * time.Millisecond,
	}
	srv := flattenServer(t, svc)
	purge := `{"actor":"ops@example.com","reason":"purging a test account"}`
	st, body := flattenCall(t, srv, uuid.NewString(), purge)
	if st != http.StatusOK || body["canceled_orders"] != float64(0) || body["canceled_conditionals"] != float64(0) || body["complete"] != true {
		t.Fatalf("a user with nothing: %d %v", st, body)
	}
	if closed, _ := body["closed"].([]any); closed == nil || len(closed) != 0 {
		t.Fatalf("a user with nothing: %v", body)
	}
	for _, c := range []struct {
		user, body, code string
		status           int
	}{
		{"nobody", purge, "COMMON_INVALID_ARGUMENT", http.StatusBadRequest},
		{uuid.NewString(), `{"actor":"ops@example.com"}`, "COMMON_INVALID_ARGUMENT", http.StatusBadRequest},
		{house, purge, "DERIV_HOUSE_NOT_CLOSED", http.StatusUnprocessableEntity},
	} {
		if st, body := flattenCall(t, srv, c.user, c.body); st != c.status || body["code"] != c.code {
			t.Fatalf("%s %s: %d %v", c.user, c.body, st, body)
		}
	}

	user := uuid.NewString()
	svc.Store = flatStore{working: &domain.Order{
		ID: uuid.NewString(), UserID: user, Symbol: "BTC-USDT-PERP", Side: domain.Sell, PositionSide: domain.SideBoth, Kind: domain.KindAdmin,
		Status: domain.StatusNew, Qty: decimal.RequireFromString("0.1"), Filled: decimal.Zero, Consumed: decimal.Zero,
	}}
	started := time.Now()
	st, body = flattenCall(t, srv, user, purge)
	left, _ := body["remaining"].([]any)
	if took := time.Since(started); st != http.StatusOK || body["complete"] != false || len(left) != 1 || took < time.Second {
		t.Fatalf("an order the engine never finishes: %d %v after %s", st, body, took)
	}
	if l, _ := left[0].(map[string]any); l["reason"] != "DERIV_CLOSE_PENDING" || l["quantity"] != "0" || l["side"] != "SHORT" {
		t.Fatalf("what is left %v", left[0])
	}
}
