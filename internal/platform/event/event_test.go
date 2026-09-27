package event

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	"github.com/lidp280504357/exchange/internal/platform/tracing"
)

func init() { _ = tracing.Setup() }

func TestNewFillsTheEnvelope(t *testing.T) {
	f := NewFactory("auth-service", "c0ffee")
	ctx, span := tracing.Tracer("test").Start(context.Background(), "request")
	defer span.End()
	ctx = WithCause(ctx, &eventv1.Envelope{EventId: "cause-1"})
	at := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	env, err := f.New(ctx, wrapperspb.String("hello"), "user", "u-1",
		WithVersion(2), WithSequence(7), WithOccurredAt(at))
	if err != nil {
		t.Fatal(err)
	}
	id, err := uuid.Parse(env.GetEventId())
	if err != nil || id.Version() != 7 {
		t.Fatalf("event id %q must be a UUIDv7", env.GetEventId())
	}
	checks := map[string][2]any{
		"type":        {env.GetEventType(), "google.protobuf.StringValue"},
		"version":     {env.GetEventVersion(), int32(2)},
		"sequence":    {env.GetSequence(), int64(7)},
		"producer":    {env.GetProducer(), "auth-service/c0ffee"},
		"aggregate":   {env.GetAggregateType() + "/" + env.GetAggregateId(), "user/u-1"},
		"correlation": {env.GetCorrelationId(), tracing.TraceID(ctx)},
		"causation":   {env.GetCausationId(), "cause-1"},
		"occurred_at": {env.GetOccurredAt().AsTime(), at},
	}
	for name, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %v, want %v", name, c[0], c[1])
		}
	}
	if got := tracing.TraceID(ContextFrom(context.Background(), env)); got != tracing.TraceID(ctx) {
		t.Errorf("traceparent does not continue the trace: %q", got)
	}
	var payload wrapperspb.StringValue
	if err := env.GetPayload().UnmarshalTo(&payload); err != nil || payload.GetValue() != "hello" {
		t.Errorf("payload = %v, %v", payload.GetValue(), err)
	}
}

func TestNewWithoutTraceOrCause(t *testing.T) {
	env, err := NewFactory("svc", "i").New(context.Background(), wrapperspb.Int64(1), "x", "1")
	if err != nil {
		t.Fatal(err)
	}
	if env.GetCausationId() != "" || env.GetCorrelationId() != "" || env.GetTraceparent() != "" || env.GetEventVersion() != 1 {
		t.Fatalf("unexpected defaults: %v", env)
	}
}

func TestType(t *testing.T) {
	if got := Type(&eventv1.Envelope{}); got != "event.Envelope" {
		t.Fatalf("exchange messages are <domain>.<Message>, got %q", got)
	}
	if got := Type(wrapperspb.String("")); got != "google.protobuf.StringValue" {
		t.Fatalf("other messages keep the full name, got %q", got)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	env, err := NewFactory("svc", "i").New(context.Background(), wrapperspb.String("x"), "agg", "a-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Encode(42, env)
	if err != nil {
		t.Fatal(err)
	}
	if b[0] != 0 || binary.BigEndian.Uint32(b[1:5]) != 42 || b[5] != 0 {
		t.Fatalf("bad header % x", b[:6])
	}
	got, id, err := Decode(b)
	if err != nil || id != 42 || !proto.Equal(got, env) {
		t.Fatalf("round trip: id=%d err=%v equal=%v", id, err, proto.Equal(got, env))
	}
}

func TestDecodeSkipsMessageIndexArrays(t *testing.T) {
	env := &eventv1.Envelope{EventId: "e-1"}
	body, _ := proto.Marshal(env)
	// Header with the index array [1, 2]: count 2, then 1 and 2, all zigzag.
	b := []byte{0, 0, 0, 0, 9}
	for _, v := range []int64{2, 1, 2} {
		b = protowire.AppendVarint(b, protowire.EncodeZigZag(v))
	}
	got, id, err := Decode(append(b, body...))
	if err != nil || id != 9 || got.GetEventId() != "e-1" {
		t.Fatalf("got %v %d %v", got, id, err)
	}
}

func TestDecodeRejectsUnframedValues(t *testing.T) {
	for name, v := range map[string][]byte{
		"short":       {0, 0, 0},
		"wrong magic": {1, 0, 0, 0, 1, 0, 1, 2},
		"raw json":    []byte(`{"event_id":"x"}`),
	} {
		if _, _, err := Decode(v); !errors.Is(err, ErrNotFramed) {
			t.Errorf("%s: want ErrNotFramed, got %v", name, err)
		}
	}
}
