package outbox_test

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/outbox"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
)

var discard = slog.New(slog.DiscardHandler)

type fakePublisher struct {
	mu   sync.Mutex
	recs []kafka.Record
	fail error
}

func (p *fakePublisher) Publish(_ context.Context, recs ...kafka.Record) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail != nil {
		return p.fail
	}
	p.recs = append(p.recs, recs...)
	return nil
}

func setup(t *testing.T) *pg.DB {
	t.Helper()
	db := testenv.Postgres(t)
	if err := migrate.UpPlatform(context.Background(), db, discard); err != nil {
		t.Fatal(err)
	}
	return db
}

func envelopes(t *testing.T, n int) []*eventv1.Envelope {
	t.Helper()
	f := event.NewFactory("test", "1")
	out := make([]*eventv1.Envelope, n)
	for i := range out {
		env, err := f.New(context.Background(), wrapperspb.Int64(int64(i)), "thing", "key-"+strconv.Itoa(i%2))
		if err != nil {
			t.Fatal(err)
		}
		out[i] = env
	}
	return out
}

func pending(t *testing.T, db *pg.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM outbox WHERE published_at IS NULL").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEventsCommitWithTheBusinessChange(t *testing.T) {
	db := setup(t)
	ctx := context.Background()
	envs := envelopes(t, 2)

	boom := errors.New("business rule failed")
	err := db.InTx(ctx, func(tx pgx.Tx) error {
		if err := outbox.Add(ctx, tx, "thing.events", envs[0]); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || pending(t, db) != 0 {
		t.Fatalf("a rolled-back change must not leave events: err=%v pending=%d", err, pending(t, db))
	}
	err = db.InTx(ctx, func(tx pgx.Tx) error { return outbox.Add(ctx, tx, "thing.events", envs...) })
	if err != nil || pending(t, db) != 2 {
		t.Fatalf("committed change must leave its events: err=%v pending=%d", err, pending(t, db))
	}
}

func TestRelayPublishesInOrderAndMarksRows(t *testing.T) {
	db := setup(t)
	ctx := context.Background()
	envs := envelopes(t, 150)
	if err := outbox.Add(ctx, db, "thing.events", envs...); err != nil {
		t.Fatal(err)
	}
	pub := &fakePublisher{}
	relay := outbox.NewRelay(db, pub, discard, prometheus.NewRegistry())

	n, err := relay.PublishBatch(ctx)
	if err != nil || n != 100 {
		t.Fatalf("first batch: n=%d err=%v", n, err)
	}
	if n, err = relay.PublishBatch(ctx); err != nil || n != 50 {
		t.Fatalf("second batch: n=%d err=%v", n, err)
	}
	if n, err = relay.PublishBatch(ctx); err != nil || n != 0 {
		t.Fatalf("nothing left: n=%d err=%v", n, err)
	}
	for i, rec := range pub.recs {
		var got eventv1.Envelope
		if err := proto.Unmarshal(rec.Envelope, &got); err != nil {
			t.Fatal(err)
		}
		if got.GetEventId() != envs[i].GetEventId() || rec.Key != envs[i].GetAggregateId() || rec.Topic != "thing.events" {
			t.Fatalf("record %d out of order or mangled: %v", i, rec)
		}
	}
}

func TestRelayKeepsRowsWhenPublishingFails(t *testing.T) {
	db := setup(t)
	ctx := context.Background()
	if err := outbox.Add(ctx, db, "thing.events", envelopes(t, 3)...); err != nil {
		t.Fatal(err)
	}
	pub := &fakePublisher{fail: errors.New("broker down")}
	relay := outbox.NewRelay(db, pub, discard, prometheus.NewRegistry())
	if _, err := relay.PublishBatch(ctx); err == nil || pending(t, db) != 3 {
		t.Fatalf("rows must stay pending: err=%v pending=%d", err, pending(t, db))
	}
	pub.fail = nil
	if n, err := relay.PublishBatch(ctx); err != nil || n != 3 || pending(t, db) != 0 {
		t.Fatalf("recovery: n=%d err=%v pending=%d", n, err, pending(t, db))
	}
}

func TestRelayRunStopsWithContext(t *testing.T) {
	db := setup(t)
	pub := &fakePublisher{}
	relay := outbox.NewRelay(db, pub, discard, prometheus.NewRegistry())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- relay.Run(ctx) }()

	if err := outbox.Add(context.Background(), db, "thing.events", envelopes(t, 1)...); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for pending(t, db) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("relay did not publish")
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
}

func TestPurge(t *testing.T) {
	db := setup(t)
	ctx := context.Background()
	if err := outbox.Add(ctx, db, "thing.events", envelopes(t, 2)...); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "UPDATE outbox SET published_at = now() - interval '8 days' WHERE id = (SELECT min(id) FROM outbox)"); err != nil {
		t.Fatal(err)
	}
	n, err := outbox.Purge(ctx, db, time.Now().Add(-7*24*time.Hour))
	if err != nil || n != 1 || pending(t, db) != 1 {
		t.Fatalf("purge: n=%d err=%v pending=%d", n, err, pending(t, db))
	}
}
