// Package kafka connects services to Redpanda: a producer that frames
// envelopes for Schema Registry, and a consumer with retry and dead-letter
// topics (requirements §8.2). Topics are created by deploy/redpanda/topics.sh.
package kafka

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sr"

	protodef "github.com/lidp280504357/exchange/api/proto"
	"github.com/lidp280504357/exchange/internal/platform/event"
)

// Config holds the broker and Schema Registry addresses.
type Config struct {
	// Brokers is a comma-separated seed list (KAFKA_BROKERS).
	Brokers []string `koanf:"kafka_brokers"`
	// SchemaRegistryURL is the registry base URL (SCHEMA_REGISTRY_URL).
	SchemaRegistryURL string `koanf:"schema_registry_url"`
	// Namespace prefixes every topic, consumer group and schema subject on
	// the brokers (KAFKA_NAMESPACE, such as "dev."), so a local stack can
	// share the test brokers without mixing its events with the deployed
	// services' (scripts/dev.sh). Callers only ever see logical names.
	Namespace string `koanf:"kafka_namespace"`
}

var validNamespace = regexp.MustCompile(`^[a-z0-9_-]+\.$`)

// Validate reports missing settings.
func (c *Config) Validate() error {
	var errs []error
	if len(c.Brokers) == 0 {
		errs = append(errs, errors.New("KAFKA_BROKERS is required"))
	}
	if c.SchemaRegistryURL == "" {
		errs = append(errs, errors.New("SCHEMA_REGISTRY_URL is required"))
	}
	if c.Namespace != "" && !validNamespace.MatchString(c.Namespace) {
		errs = append(errs, fmt.Errorf("KAFKA_NAMESPACE %q must be lowercase letters, digits, dashes or underscores ending in a dot", c.Namespace))
	}
	return errors.Join(errs...)
}

// topics returns the broker names of logical topics.
func (c *Config) topics(names ...string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = c.Namespace + n
	}
	return out
}

// Record is an envelope to publish; Key is the partition key.
type Record struct {
	Topic     string
	Key       string
	EventType string
	// Envelope is the protobuf-encoded envelope, without framing.
	Envelope []byte
}

// Producer publishes envelopes synchronously with acks=all and idempotent
// writes, registering the envelope schema per topic on first use.
type Producer struct {
	cl *kgo.Client
	sr *sr.Client
	ns string

	mu        sync.Mutex
	schemaIDs map[string]int32
}

// NewProducer connects and pings the brokers.
func NewProducer(ctx context.Context, cfg Config, clientID string) (*Producer, error) {
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ClientID(clientID),
		kgo.RequiredAcks(kgo.AllISRAcks()),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka: %w", err)
	}
	if err := cl.Ping(ctx); err != nil {
		cl.Close()
		return nil, fmt.Errorf("kafka: ping %s: %w", strings.Join(cfg.Brokers, ","), err)
	}
	reg, err := sr.NewClient(sr.URLs(cfg.SchemaRegistryURL))
	if err != nil {
		cl.Close()
		return nil, fmt.Errorf("kafka: schema registry: %w", err)
	}
	return &Producer{cl: cl, sr: reg, ns: cfg.Namespace, schemaIDs: map[string]int32{}}, nil
}

// Publish writes the records and waits until all are acknowledged. Records
// with the same key keep their relative order.
func (p *Producer) Publish(ctx context.Context, recs ...Record) error {
	out := make([]*kgo.Record, 0, len(recs))
	for _, r := range recs {
		topic := p.ns + r.Topic
		id, err := p.schemaID(ctx, topic)
		if err != nil {
			return err
		}
		out = append(out, &kgo.Record{
			Topic: topic,
			Key:   []byte(r.Key),
			Value: event.Frame(id, r.Envelope),
			Headers: []kgo.RecordHeader{
				{Key: HeaderEventType, Value: []byte(r.EventType)},
			},
		})
	}
	if err := p.cl.ProduceSync(ctx, out...).FirstErr(); err != nil {
		return fmt.Errorf("kafka: produce: %w", err)
	}
	return nil
}

// Ping is a readiness check.
func (p *Producer) Ping(ctx context.Context) error { return p.cl.Ping(ctx) }

// Close flushes and closes the client.
func (p *Producer) Close() { p.cl.Close() }

// schemaID registers the envelope schema under "<topic>-value" once per
// topic; registering an identical schema returns the existing ID.
func (p *Producer) schemaID(ctx context.Context, topic string) (int32, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id, ok := p.schemaIDs[topic]; ok {
		return id, nil
	}
	ss, err := p.sr.CreateSchema(ctx, topic+"-value", sr.Schema{Schema: protodef.EnvelopeProto, Type: sr.TypeProtobuf})
	if err != nil {
		return 0, fmt.Errorf("kafka: register schema for %s: %w", topic, err)
	}
	id := int32(ss.ID) //nolint:gosec // registry IDs are small positive integers
	p.schemaIDs[topic] = id
	return id, nil
}

// Header keys used on event, retry and dead-letter records.
const (
	HeaderEventType   = "event_type"
	HeaderOriginTopic = "x-origin-topic"
	HeaderGroup       = "x-group"
	HeaderAttempt     = "x-attempt"
	HeaderNotBefore   = "x-not-before"
	HeaderError       = "x-error"
)
