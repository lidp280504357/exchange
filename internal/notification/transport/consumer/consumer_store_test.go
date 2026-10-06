package consumer

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	derivv1 "github.com/skill/exchange/api/gen/go/exchange/derivatives/v1"
	"github.com/skill/exchange/internal/notification/adapters/postgres"
	"github.com/skill/exchange/internal/notification/application"
	"github.com/skill/exchange/internal/notification/domain"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/migrations"
)

// TestMergedNoticesStoreOnce stores a cross takeover's notices under their
// merge key in PostgreSQL, whose inbox keys events by UUID: the first is
// kept, the takeover's other positions find it there (review R10, B138:
// a key that was no UUID failed every one of them).
func TestMergedNoticesStoreOnce(t *testing.T) {
	db := testenv.Postgres(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Notify(), log); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(db, event.NewFactory("notification-service", "test"))
	user := uuid.NewString()
	// One takeover: its events a few milliseconds apart in one minute.
	minute := time.Now().Truncate(time.Minute).Add(10 * time.Second)
	for i, symbol := range []string{"BTC-USD-PERP", "BTC-USD-PERP", "SOL-USD-PERP"} {
		e, _ := toEvent(&derivv1.LiquidationStarted{Cross: true, Position: &derivv1.Position{UserId: user, Symbol: symbol, SettleAsset: "BTC"}})
		e.At = minute.Add(time.Duration(i) * 100 * time.Millisecond)
		key := mergeKey(e)
		n := domain.Notice{ID: uuid.Must(uuid.NewV7()).String(), UserID: user, Type: e.Type, Title: "t", Body: "b", Data: e.Data, CreatedAt: time.Now()}
		created, err := store.CreateNotice(ctx, application.Consumer, key, n, nil)
		if err != nil || created != (i == 0) {
			t.Fatalf("position %d (%s): created %v, %v", i, symbol, created, err)
		}
	}
	if n, err := store.UnreadCount(ctx, user); err != nil || n != 1 {
		t.Fatalf("one notice for the account: %d %v", n, err)
	}
}
