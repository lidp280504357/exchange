// Package event builds and parses the envelopes published to Redpanda
// (requirements §8): envelope fields, event type naming, causation and
// trace propagation, and the Schema Registry wire framing.
package event

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	"github.com/lidp280504357/exchange/internal/platform/tracing"
)

// Topics (requirements §8.1). Each has a .retry and a .dlq companion.
const (
	TopicAuth         = "auth.events"
	TopicUser         = "user.events"
	TopicInstrument   = "instrument.events"
	TopicLedger       = "ledger.events"
	TopicAccount      = "account.events"
	TopicRisk         = "risk.events"
	TopicAudit        = "audit.events"
	TopicNotification = "notification.events"
)

// Factory stamps envelopes with the producing service and instance.
type Factory struct {
	producer string
}

// NewFactory returns a factory for events produced by service/instance.
func NewFactory(service, instance string) *Factory {
	return &Factory{producer: service + "/" + instance}
}

// Option adjusts an envelope built by New.
type Option func(*eventv1.Envelope)

// WithVersion sets event_version (default 1).
func WithVersion(v int32) Option { return func(e *eventv1.Envelope) { e.EventVersion = v } }

// WithSequence sets the per-aggregate sequence.
func WithSequence(seq int64) Option { return func(e *eventv1.Envelope) { e.Sequence = seq } }

// WithOccurredAt overrides occurred_at, which defaults to now.
func WithOccurredAt(t time.Time) Option {
	return func(e *eventv1.Envelope) { e.OccurredAt = timestamppb.New(t) }
}

// New wraps payload in an envelope. The correlation ID is the trace ID of
// ctx; when ctx comes from an event handler, causation_id is that event.
func (f *Factory) New(ctx context.Context, payload proto.Message, aggregateType, aggregateID string, opts ...Option) (*eventv1.Envelope, error) {
	body, err := anypb.New(payload)
	if err != nil {
		return nil, fmt.Errorf("event: pack payload: %w", err)
	}
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	env := &eventv1.Envelope{
		EventId:       uuid.Must(uuid.NewV7()).String(),
		EventType:     Type(payload),
		EventVersion:  1,
		OccurredAt:    timestamppb.Now(),
		Producer:      f.producer,
		AggregateType: aggregateType,
		AggregateId:   aggregateID,
		CorrelationId: tracing.TraceID(ctx),
		CausationId:   causeFrom(ctx),
		Traceparent:   carrier.Get("traceparent"),
		Payload:       body,
	}
	for _, opt := range opts {
		opt(env)
	}
	return env, nil
}

// Type names an event "<domain>.<Message>" for messages in package
// exchange.<domain>.v1, e.g. exchange.auth.v1.UserRegistered becomes
// "auth.UserRegistered"; other messages keep their full name.
func Type(m proto.Message) string {
	full := string(m.ProtoReflect().Descriptor().FullName())
	parts := strings.Split(full, ".")
	if len(parts) >= 4 && parts[0] == "exchange" {
		return parts[1] + "." + parts[len(parts)-1]
	}
	return full
}

type causeKey struct{}

// WithCause marks ctx as handling env, so events emitted while handling it
// record env as their cause.
func WithCause(ctx context.Context, env *eventv1.Envelope) context.Context {
	return context.WithValue(ctx, causeKey{}, env.GetEventId())
}

func causeFrom(ctx context.Context) string {
	id, _ := ctx.Value(causeKey{}).(string)
	return id
}

// ContextFrom continues the trace carried by env.
func ContextFrom(ctx context.Context, env *eventv1.Envelope) context.Context {
	if env.GetTraceparent() == "" {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier{"traceparent": env.GetTraceparent()})
}

// Schema Registry wire format: magic byte 0, big-endian schema ID, the
// message-index array (a single 0 byte for the first message in the file),
// then the protobuf encoding.
const magicByte = 0

// ErrNotFramed reports a value without the Schema Registry header.
var ErrNotFramed = errors.New("event: value lacks the schema registry header")

// Encode marshals env and frames it with schemaID.
func Encode(schemaID int32, env *eventv1.Envelope) ([]byte, error) {
	body, err := proto.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("event: marshal envelope: %w", err)
	}
	return Frame(schemaID, body), nil
}

// Frame prefixes an encoded envelope with the Schema Registry header.
func Frame(schemaID int32, body []byte) []byte {
	out := make([]byte, 0, 6+len(body))
	out = append(out, magicByte)
	out = binary.BigEndian.AppendUint32(out, uint32(schemaID)) //nolint:gosec // schema IDs are positive
	out = append(out, 0)                                       // message indexes: [0]
	return append(out, body...)
}

// Decode unframes and unmarshals a value, returning the schema ID too.
func Decode(value []byte) (*eventv1.Envelope, int32, error) {
	if len(value) < 6 || value[0] != magicByte {
		return nil, 0, ErrNotFramed
	}
	id := int32(binary.BigEndian.Uint32(value[1:5])) //nolint:gosec // schema IDs are positive
	rest := value[5:]
	// Skip the zigzag-varint message-index array.
	n, l := protowire.ConsumeVarint(rest)
	if l < 0 {
		return nil, 0, ErrNotFramed
	}
	rest = rest[l:]
	for count := protowire.DecodeZigZag(n); count > 0; count-- {
		_, l := protowire.ConsumeVarint(rest)
		if l < 0 {
			return nil, 0, ErrNotFramed
		}
		rest = rest[l:]
	}
	env := &eventv1.Envelope{}
	if err := proto.Unmarshal(rest, env); err != nil {
		return nil, 0, fmt.Errorf("event: unmarshal envelope: %w", err)
	}
	return env, id, nil
}
