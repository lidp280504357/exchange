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

	"github.com/skill/exchange/internal/notification/adapters/mock"
	"github.com/skill/exchange/internal/notification/adapters/postgres"
	"github.com/skill/exchange/internal/notification/domain"
	"github.com/skill/exchange/internal/notification/transport/httpapi"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/migrations"
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

func TestNotices(t *testing.T) {
	db := setup(t)
	store := postgres.NewStore(db, event.NewFactory("notification-service", "test"))
	ctx := context.Background()
	user := uuid.NewString()
	var ids, events []string
	for i, typ := range []string{domain.NoticeWelcome, domain.NoticeNewDeviceLogin, domain.NoticePasswordChanged} {
		n := domain.Notice{
			ID: uuid.Must(uuid.NewV7()).String(), UserID: user, Type: typ, Title: "t", Body: "b",
			Data: map[string]string{"i": string(rune('0' + i))}, CreatedAt: time.Now(),
		}
		ids, events = append(ids, n.ID), append(events, uuid.NewString())
		if created, err := store.CreateNotice(ctx, "notification-service", events[i], n, nil); err != nil || !created {
			t.Fatalf("create: %v %v", created, err)
		}
	}
	dup := domain.Notice{ID: uuid.Must(uuid.NewV7()).String(), UserID: user, Type: domain.NoticeWelcome, CreatedAt: time.Now()}
	if created, err := store.CreateNotice(ctx, "notification-service", events[0], dup, nil); err != nil || created {
		t.Fatalf("redelivery: %v %v", created, err)
	}

	page, err := store.ListNotices(ctx, user, "", 2)
	if err != nil || len(page) != 2 || page[0].ID != ids[2] || page[1].Data["i"] != "1" {
		t.Fatalf("page 1: %+v %v", page, err)
	}
	rest, err := store.ListNotices(ctx, user, page[1].ID, 10)
	if err != nil || len(rest) != 1 || rest[0].ID != ids[0] {
		t.Fatalf("page 2: %+v %v", rest, err)
	}
	if n, err := store.MarkRead(ctx, user, []string{ids[0]}); err != nil || n != 1 {
		t.Fatalf("mark one: %d %v", n, err)
	}
	if n, _ := store.UnreadCount(ctx, user); n != 2 {
		t.Fatalf("unread: %d", n)
	}
	if n, err := store.MarkRead(ctx, user, nil); err != nil || n != 2 {
		t.Fatalf("mark all: %d %v", n, err)
	}
	var queued int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE event_type = 'notification.NotificationCreated'`).Scan(&queued); err != nil || queued != 3 {
		t.Fatalf("NotificationCreated queued: %d %v", queued, err)
	}
}
