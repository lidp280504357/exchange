package postgres_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lidp280504357/exchange/internal/notification/adapters/mock"
	"github.com/lidp280504357/exchange/internal/notification/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/notification/domain"
	"github.com/lidp280504357/exchange/internal/notification/transport/httpapi"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
	"github.com/lidp280504357/exchange/migrations"
)

func setup(t *testing.T) *pg.DB {
	t.Helper()
	db := testenv.Postgres(t)
	log := slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(context.Background(), db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(context.Background(), db, migrations.Notify(), log); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestDeliveryLifecycle(t *testing.T) {
	db := setup(t)
	store := postgres.NewStore(db, event.NewFactory("notification-service", "test"))
	ctx := context.Background()
	d := domain.Delivery{ID: uuid.NewString(), Kind: domain.KindOTP, Channel: domain.ChannelEmail, Template: "otp.register", TargetMask: "a***@x.com"}
	if err := store.CreateDelivery(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAttempt(ctx, d.ID, domain.StatusFailedRetrying, "resend", domain.FailureTimeout, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.FailDelivery(ctx, d, domain.FailureTimeout); err != nil {
		t.Fatal(err)
	}
	var status, class string
	var attempts int
	if err := db.QueryRow(ctx, `SELECT status, failure_class, attempts FROM deliveries WHERE id = $1`, d.ID).Scan(&status, &class, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "FAILED" || class != "TIMEOUT" || attempts != 1 {
		t.Fatalf("delivery = %s %s %d", status, class, attempts)
	}
	var queued int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE event_type = 'notification.DeliveryFailed'`).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("dead-letter event: %d %v", queued, err)
	}
}

func TestMockInbox(t *testing.T) {
	db := setup(t)
	p := mock.New("mock", db)
	ctx := context.Background()
	for _, code := range []string{"111111", "222222"} {
		if _, err := p.Send(ctx, domain.Message{Channel: domain.ChannelSMS, To: "+8613812341234", Text: "code " + code}); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/dev/messages?target=%2B8613812341234&limit=1", http.NoBody)
	rr := httptest.NewRecorder()
	httpapi.DevInbox(p)(rr, req)
	var body struct {
		Messages []mock.Message `json:"messages"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || rr.Code != http.StatusOK {
		t.Fatalf("inbox: %d %s", rr.Code, rr.Body)
	}
	if len(body.Messages) != 1 || body.Messages[0].Body != "code 222222" {
		t.Fatalf("newest first, limited: %+v", body.Messages)
	}
	rr = httptest.NewRecorder()
	httpapi.DevInbox(p)(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/dev/messages", http.NoBody))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("target is required: %d", rr.Code)
	}
	if n, err := p.Purge(ctx, time.Now().Add(time.Hour)); err != nil || n != 2 {
		t.Fatalf("purge: %d %v", n, err)
	}
}
