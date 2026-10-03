package httpapi

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

	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
	"github.com/lidp280504357/exchange/internal/wallet/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/wallet/application"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
	"github.com/lidp280504357/exchange/migrations"
)

// ethOnly withdraws ETH and nothing else.
type ethOnly struct{ ports.Networks }

func (ethOnly) ForAsset(_ context.Context, asset string) ([]domain.Network, error) {
	if asset == "ETH" {
		return []domain.Network{{Asset: "ETH", Network: "ETH"}}, nil
	}
	return nil, nil
}

// TestTheConsoleSuspendsAndResumesWithdrawals: the console lists the
// suspended assets and lifts one, as exchangectl does (C5.5 ⑯).
func TestTheConsoleSuspendsAndResumesWithdrawals(t *testing.T) {
	db := testenv.Postgres(t)
	ctx, log := context.Background(), slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Wallet(), log); err != nil {
		t.Fatal(err)
	}
	svc := &application.Service{Store: postgres.NewStore(db, event.NewFactory("wallet-test", "t")), Networks: ethOnly{}, Log: log, Now: time.Now}
	r := chi.NewRouter()
	(&Handler{Svc: svc}).Routes(r)
	call := func(method, path, body string) (int, string) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body)))
		return w.Code, w.Body.String()
	}
	list := func() []SuspensionJSON {
		code, body := call(http.MethodGet, "/internal/wallet/suspensions", "")
		var out struct{ Items []SuspensionJSON }
		if code != http.StatusOK || json.Unmarshal([]byte(body), &out) != nil {
			t.Fatalf("list: %d %s", code, body)
		}
		return out.Items
	}
	if got := list(); len(got) != 0 {
		t.Fatalf("nothing suspended yet: %+v", got)
	}
	by := `{"actor":"boss@example.com","reason":"the custodian's balance is short"}`
	if code, body := call(http.MethodPost, "/internal/wallet/suspensions/eth/suspend", by); code != http.StatusOK || !strings.Contains(body, `"asset":"ETH"`) {
		t.Fatalf("suspend: %d %s", code, body)
	}
	if code, _ := call(http.MethodPost, "/internal/wallet/suspensions/ETH/suspend", by); code != http.StatusConflict {
		t.Fatalf("suspended twice: %d", code)
	}
	if code, _ := call(http.MethodPost, "/internal/wallet/suspensions/XYZ/suspend", by); code != http.StatusNotFound {
		t.Fatalf("an asset nothing withdraws: %d", code)
	}
	if code, _ := call(http.MethodPost, "/internal/wallet/suspensions/ETH/resume", `{"actor":"boss@example.com","reason":""}`); code != http.StatusBadRequest {
		t.Fatalf("a resume without a reason: %d", code)
	}
	if got := list(); len(got) != 1 || got[0].Asset != "ETH" || got[0].SuspendedBy != "boss@example.com" || got[0].Shortfall != "0" {
		t.Fatalf("suspended %+v", got)
	}
	if code, body := call(http.MethodPost, "/internal/wallet/suspensions/ETH/resume", `{"actor":"boss@example.com","reason":"the balance is back"}`); code != http.StatusOK ||
		!strings.Contains(body, `"suspended_by":"boss@example.com"`) {
		t.Fatalf("resume: %d %s", code, body)
	}
	if code, _ := call(http.MethodPost, "/internal/wallet/suspensions/ETH/resume", `{"actor":"boss@example.com","reason":"again"}`); code != http.StatusNotFound {
		t.Fatalf("resumed twice: %d", code)
	}
	if got := list(); len(got) != 0 {
		t.Fatalf("lifted %+v", got)
	}
	var audits int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE event_type = 'audit.AdminActionPerformed'`).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("audited %d times, %v", audits, err)
	}
}
