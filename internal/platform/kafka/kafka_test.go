package kafka_test

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
)

var discard = slog.New(slog.DiscardHandler)

func setup(t *testing.T) (kafka.Config, string, *kafka.Producer) {
	t.Helper()
	cfg := kafka.Config{Brokers: testenv.KafkaBrokers(t), SchemaRegistryURL: testenv.SchemaRegistryURL(t)}
	topic := testenv.KafkaTopic(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prod, err := kafka.NewProducer(ctx, cfg, "test")
	if err != nil {
		t.Fatalf("NewProducer: %v", err)
	}
	t.Cleanup(prod.Close)
	return cfg, topic, prod
}

func publish(t *testing.T, prod *kafka.Producer, topic string, values ...string) []*eventv1.Envelope {
	t.Helper()
	f := event.NewFactory("test", "1")
	var envs []*eventv1.Envelope
	var recs []kafka.Record
	for i, v := range values {
		env, err := f.New(context.Background(), wrapperspb.String(v), "thing", "key-"+strconv.Itoa(i))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := proto.Marshal(env)
		envs = append(envs, env)
		recs = append(recs, kafka.Record{Topic: topic, Key: env.GetAggregateId(), EventType: env.GetEventType(), Envelope: body})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := prod.Publish(ctx, recs...); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	return envs
}

// runConsumer consumes topic with h until the test ends and returns the
// consumer group.
func runConsumer(t *testing.T, cfg kafka.Config, topic string, h kafka.Handler) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	group := testenv.Name("g")
	c, err := kafka.NewConsumer(ctx, cfg, kafka.ConsumerOptions{
		Group:       group,
		Topics:      []string{topic},
		Handler:     h,
		Logger:      discard,
		Metrics:     kafka.NewConsumerMetrics(prometheus.NewRegistry()),
		RetryDelays: []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond, 40 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	go func() { _ = c.Run() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Stop(ctx)
	})
	return group
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestPublishAndConsume(t *testing.T) {
	cfg, topic, prod := setup(t)
	sent := publish(t, prod, topic, "a", "b", "c")

	var mu sync.Mutex
	got := map[string]string{}
	runConsumer(t, cfg, topic, func(_ context.Context, env *eventv1.Envelope) error {
		var v wrapperspb.StringValue
		if err := env.GetPayload().UnmarshalTo(&v); err != nil {
			return err
		}
		mu.Lock()
		got[env.GetEventId()] = v.GetValue()
		mu.Unlock()
		return nil
	})
	waitFor(t, "three events", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 3
	})
	for i, env := range sent {
		if got[env.GetEventId()] != []string{"a", "b", "c"}[i] {
			t.Fatalf("event %s delivered as %q", env.GetEventId(), got[env.GetEventId()])
		}
	}
}

func TestFailedEventsAreRetried(t *testing.T) {
	cfg, topic, prod := setup(t)
	publish(t, prod, topic, "flaky")
	var calls atomic.Int32
	runConsumer(t, cfg, topic, func(context.Context, *eventv1.Envelope) error {
		if calls.Add(1) < 3 {
			return errors.New("transient failure")
		}
		return nil
	})
	waitFor(t, "third attempt", func() bool { return calls.Load() >= 3 })
	time.Sleep(500 * time.Millisecond)
	if n := calls.Load(); n != 3 {
		t.Fatalf("handled %d times, want exactly 3", n)
	}
}

func TestExhaustedAndPoisonEventsGoToTheDeadLetterTopic(t *testing.T) {
	cfg, topic, prod := setup(t)
	envs := publish(t, prod, topic, "doomed")

	// A value without the registry header cannot even be decoded.
	raw, err := kgo.NewClient(kgo.SeedBrokers(cfg.Brokers...))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if err := raw.ProduceSync(context.Background(), &kgo.Record{Topic: topic, Key: []byte("p"), Value: []byte("not an envelope")}).FirstErr(); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	runConsumer(t, cfg, topic, func(context.Context, *eventv1.Envelope) error {
		calls.Add(1)
		return errors.New("permanent failure")
	})

	dlq, err := kgo.NewClient(kgo.SeedBrokers(cfg.Brokers...), kgo.ConsumeTopics(topic+".dlq"),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer dlq.Close()
	var parked []*kgo.Record
	waitFor(t, "two dead letters", func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		dlq.PollFetches(ctx).EachRecord(func(r *kgo.Record) { parked = append(parked, r) })
		return len(parked) == 2
	})
	if calls.Load() != 5 {
		t.Fatalf("handler ran %d times, want 1 + 4 retries", calls.Load())
	}
	headers := func(r *kgo.Record) map[string]string {
		m := map[string]string{}
		for _, h := range r.Headers {
			m[h.Key] = string(h.Value)
		}
		return m
	}
	for _, r := range parked {
		h := headers(r)
		if h[kafka.HeaderOriginTopic] != topic || h[kafka.HeaderError] == "" {
			t.Fatalf("dead letter lacks context: %v", h)
		}
		if string(r.Key) == envs[0].GetAggregateId() && h[kafka.HeaderAttempt] != "4" {
			t.Fatalf("exhausted event parked at attempt %s", h[kafka.HeaderAttempt])
		}
	}
}

func TestDeadLettersCanBeReplayed(t *testing.T) {
	cfg, topic, prod := setup(t)
	envs := publish(t, prod, topic, "replay-me")
	raw, err := kgo.NewClient(kgo.SeedBrokers(cfg.Brokers...))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if err := raw.ProduceSync(context.Background(), &kgo.Record{Topic: topic, Key: []byte("p"), Value: []byte("not an envelope")}).FirstErr(); err != nil {
		t.Fatal(err)
	}

	var failing atomic.Bool
	failing.Store(true)
	var handled atomic.Int32
	runConsumer(t, cfg, topic, func(context.Context, *eventv1.Envelope) error {
		if failing.Load() {
			return errors.New("downstream down")
		}
		handled.Add(1)
		return nil
	})

	var parked []kafka.DLQRecord
	waitFor(t, "two dead letters", func() bool {
		// Each read opens a client; the test broker may be far away.
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		recs, err := kafka.ReadDLQ(ctx, cfg, topic)
		if err != nil {
			t.Logf("ReadDLQ: %v", err)
			return false
		}
		parked = recs
		return len(recs) == 2
	})
	var event, poison kafka.DLQRecord
	for _, r := range parked {
		if r.EventID != "" {
			event = r
		} else {
			poison = r
		}
	}
	if event.EventID != envs[0].GetEventId() || event.Origin != topic || event.Group == "" || event.Attempt != 4 ||
		event.EventType == "" || event.Error == "" || event.ParkedAt.IsZero() {
		t.Fatalf("dead letter: %+v", event)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := kafka.ReplayDLQ(ctx, cfg, []kafka.DLQRecord{poison}); err == nil {
		t.Fatal("an undecodable record must not be replayed")
	}
	failing.Store(false)
	if n, err := kafka.ReplayDLQ(ctx, cfg, []kafka.DLQRecord{event}); err != nil || n != 1 {
		t.Fatalf("ReplayDLQ: %d %v", n, err)
	}
	waitFor(t, "the replayed event", func() bool { return handled.Load() == 1 })
	time.Sleep(500 * time.Millisecond)
	if n := handled.Load(); n != 1 {
		t.Fatalf("handled %d times after one replay", n)
	}
}

func TestNamespaceKeepsTopicsAndGroupsApart(t *testing.T) {
	cfg := kafka.Config{Brokers: testenv.KafkaBrokers(t), SchemaRegistryURL: testenv.SchemaRegistryURL(t)}
	cfg.Namespace = testenv.Name("ns") + "."
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	topic := testenv.KafkaTopicIn(t, cfg.Namespace)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prod, err := kafka.NewProducer(ctx, cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer prod.Close()
	envs := publish(t, prod, topic, "namespaced")

	// The record lands on the prefixed topic.
	raw, err := kgo.NewClient(kgo.SeedBrokers(cfg.Brokers...), kgo.ConsumeTopics(cfg.Namespace+topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var placed int
	waitFor(t, "the record on the prefixed topic", func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		placed += raw.PollFetches(ctx).NumRecords()
		return placed == 1
	})

	var failing atomic.Bool
	failing.Store(true)
	var handled atomic.Int32
	group := runConsumer(t, cfg, topic, func(context.Context, *eventv1.Envelope) error {
		if failing.Load() {
			return errors.New("downstream down")
		}
		handled.Add(1)
		return nil
	})

	// The dead-letter tools speak logical names.
	var parked []kafka.DLQRecord
	waitFor(t, "the dead letter", func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		recs, err := kafka.ReadDLQ(ctx, cfg, topic)
		if err != nil {
			t.Logf("ReadDLQ: %v", err)
			return false
		}
		parked = recs
		return len(recs) == 1
	})
	if d := parked[0]; d.EventID != envs[0].GetEventId() || d.Origin != topic || d.Group != group {
		t.Fatalf("dead letter %+v, want origin %s and group %s", d, topic, group)
	}
	failing.Store(false)
	rctx, rcancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer rcancel()
	if n, err := kafka.ReplayDLQ(rctx, cfg, parked); err != nil || n != 1 {
		t.Fatalf("ReplayDLQ: %d %v", n, err)
	}
	waitFor(t, "the replayed event", func() bool { return handled.Load() == 1 })

	// Progress is committed under the prefixed group only.
	adm := kadm.NewClient(raw)
	waitFor(t, "the prefixed group's offset", func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		offs, err := adm.FetchOffsets(ctx, cfg.Namespace+group)
		if err != nil {
			return false
		}
		o, ok := offs.Lookup(cfg.Namespace+topic, 0)
		return ok && o.At == 1
	})
	fctx, fcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer fcancel()
	if offs, err := adm.FetchOffsets(fctx, group); err != nil || len(offs) != 0 {
		t.Fatalf("the bare group has offsets %v (%v)", offs, err)
	}
}

func TestNamespaceMustEndInADot(t *testing.T) {
	for ns, ok := range map[string]bool{"": true, "dev.": true, "pr-12.": true, "dev": false, "Dev.": false, "a.b.": false} {
		cfg := kafka.Config{Brokers: []string{"b:9092"}, SchemaRegistryURL: "http://sr", Namespace: ns}
		if err := cfg.Validate(); (err == nil) != ok {
			t.Errorf("namespace %q: %v", ns, err)
		}
	}
}
