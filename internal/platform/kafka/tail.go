package kafka

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/lidp280504357/exchange/internal/platform/event"
)

// Tail reads topics from their current end without a consumer group:
// every instance sees every record, nothing is committed, and a record the
// handler fails on is only logged. It suits fan-out to live connections
// (the WebSocket gateway, requirements §7.3), which do not replay what
// happened while an instance was down. It is an app component.
type Tail struct {
	client  *kgo.Client
	handler Handler
	log     *slog.Logger
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewTail connects and positions itself at the end of every partition.
func NewTail(ctx context.Context, cfg Config, name string, topics []string, h Handler, log *slog.Logger) (*Tail, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ClientID(name),
		kgo.ConsumeTopics(topics...),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtEnd()),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka tail %s: %w", name, err)
	}
	if err := client.Ping(ctx); err != nil {
		client.Close()
		return nil, fmt.Errorf("kafka tail %s: ping: %w", name, err)
	}
	cctx, cancel := context.WithCancel(context.Background())
	return &Tail{client: client, handler: h, log: log, ctx: cctx, cancel: cancel, done: make(chan struct{})}, nil
}

// Run hands records to the handler until Stop.
func (t *Tail) Run() error {
	defer close(t.done)
	for {
		fetches := t.client.PollFetches(t.ctx)
		if fetches.IsClientClosed() || t.ctx.Err() != nil {
			return nil
		}
		fetches.EachError(func(topic string, p int32, err error) {
			t.log.Warn("kafka tail fetch failed", "topic", topic, "partition", p, "error", err)
		})
		fetches.EachRecord(func(r *kgo.Record) {
			env, _, err := event.Decode(r.Value)
			if err != nil {
				t.log.Warn("kafka tail skipped an undecodable record", "topic", r.Topic, "offset", r.Offset, "error", err)
				return
			}
			if err := t.handler(event.ContextFrom(t.ctx, env), env); err != nil {
				t.log.Warn("kafka tail handler failed", "topic", r.Topic, "event_id", env.GetEventId(), "error", err)
			}
		})
	}
}

// Stop ends Run and closes the client.
func (t *Tail) Stop(ctx context.Context) error {
	t.cancel()
	select {
	case <-t.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	t.client.Close()
	return nil
}
