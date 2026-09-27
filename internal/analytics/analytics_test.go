package analytics_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	"github.com/lidp280504357/exchange/internal/analytics"
	"github.com/lidp280504357/exchange/internal/platform/chx"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/outbox"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
	"github.com/lidp280504357/exchange/migrations"
)

var discard = slog.New(slog.DiscardHandler)

func TestPayloadJSON(t *testing.T) {
	known, _ := anypb.New(wrapperspb.String("hello"))
	var got map[string]any
	if err := json.Unmarshal([]byte(analytics.PayloadJSON(known)), &got); err != nil || got["value"] != "hello" {
		t.Fatalf("known type: %v %v", got, err)
	}
	unknown := &anypb.Any{TypeUrl: "type.googleapis.com/exchange.future.v9.Thing", Value: []byte{1, 2, 3}}
	if err := json.Unmarshal([]byte(analytics.PayloadJSON(unknown)), &got); err != nil || got["@raw"] != "AQID" {
		t.Fatalf("unknown type must keep raw bytes: %v %v", got, err)
	}
	if analytics.PayloadJSON(nil) != "{}" {
		t.Fatal("nil payload")
	}
}

func TestIngestAndReconcile(t *testing.T) {
	ctx := context.Background()
	chCfg := testenv.ClickHouse(t)
	conn, err := chx.Open(ctx, chCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sqlDB := chx.OpenDB(chCfg)
	defer sqlDB.Close()
	if err := migrate.UpClickHouse(ctx, sqlDB, migrations.ClickHouse(), discard); err != nil {
		t.Fatalf("clickhouse migrations: %v", err)
	}

	db := testenv.Postgres(t)
	if err := migrate.UpPlatform(ctx, db, discard); err != nil {
		t.Fatal(err)
	}

	at := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	f := event.NewFactory("auth-service", "t")
	envs := make([]*eventv1.Envelope, 3)
	for i := range envs {
		env, err := f.New(ctx, wrapperspb.Int32(int32(i)), "user", "u-1", event.WithOccurredAt(at.Add(time.Duration(i)*time.Millisecond)))
		if err != nil {
			t.Fatal(err)
		}
		envs[i] = env
	}
	if err := outbox.Add(ctx, db, event.TopicAuth, envs...); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "UPDATE outbox SET published_at = now()"); err != nil {
		t.Fatal(err)
	}

	reg := prometheus.NewRegistry()
	in := analytics.NewIngestor(conn, discard, reg)
	deliver := func(envs ...*eventv1.Envelope) []kafka.Delivery {
		out := make([]kafka.Delivery, len(envs))
		for i, env := range envs {
			out[i] = kafka.Delivery{Topic: event.TopicAuth, Offset: int64(i), Envelope: env}
		}
		return out
	}
	malformed := &eventv1.Envelope{EventId: "not-a-uuid", OccurredAt: envs[0].GetOccurredAt()}
	// envs[0] arrives twice, as after a redelivery; the malformed one is dropped.
	if err := in.Store(ctx, deliver(envs[0], envs[1], envs[0], malformed)); err != nil {
		t.Fatalf("Store: %v", err)
	}

	rec := analytics.NewReconciler(db, conn, []string{db.Schema()}, discard, reg)
	from, to := at.Add(-time.Minute), at.Add(time.Minute)
	check := func(wantMissing int64) {
		t.Helper()
		results, err := rec.Reconcile(ctx, from, to)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if len(results) != 1 || results[0].Topic != event.TopicAuth || results[0].Postgres != 3 || results[0].Missing() != wantMissing {
			t.Fatalf("results = %+v, want 3 published and %d missing", results, wantMissing)
		}
	}
	check(1)
	if err := in.Store(ctx, deliver(envs[2])); err != nil {
		t.Fatal(err)
	}
	check(0)

	var payload string
	if err := conn.QueryRow(ctx, "SELECT payload FROM events FINAL WHERE event_id = ?", envs[1].GetEventId()).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if payload != `{"@type":"type.googleapis.com/google.protobuf.Int32Value","value":1}` {
		t.Fatalf("payload = %s", payload)
	}

	// Audit events are also copied into audit_logs, keyed by actor.
	changed, err := f.New(ctx, &auditv1.ConfigChanged{Target: "flag:account.transfer", Actor: "cli:ops", Reason: "test"}, "actor", "cli:ops")
	if err != nil {
		t.Fatal(err)
	}
	if err := in.Store(ctx, []kafka.Delivery{{Topic: event.TopicAudit, Envelope: changed}}); err != nil {
		t.Fatal(err)
	}
	var actor, target string
	if err := conn.QueryRow(ctx, "SELECT actor_id, target FROM audit_logs FINAL WHERE event_id = ?", changed.GetEventId()).Scan(&actor, &target); err != nil {
		t.Fatal(err)
	}
	if actor != "cli:ops" || target != "flag:account.transfer" {
		t.Fatalf("audit row = %s %s", actor, target)
	}
}
