package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/logging"
	"github.com/lidp280504357/exchange/internal/platform/tracing"
)

// Handler processes one event. An error sends the record to the retry
// topic, so handlers must be idempotent; package inbox makes them so.
type Handler func(ctx context.Context, env *eventv1.Envelope) error

// DefaultRetryDelays are the waits before each retry (requirements §8.2).
// After the last retry fails the record goes to the dead-letter topic.
var DefaultRetryDelays = []time.Duration{time.Second, 5 * time.Second, 30 * time.Second, 5 * time.Minute}

// ConsumerMetrics are shared by the consumers of one process.
type ConsumerMetrics struct {
	records  *prometheus.CounterVec
	duration *prometheus.HistogramVec
	lag      *prometheus.GaugeVec
	assigned *prometheus.GaugeVec
	lastPoll *prometheus.GaugeVec
}

// NewConsumerMetrics registers the consumer metrics with reg.
func NewConsumerMetrics(reg prometheus.Registerer) *ConsumerMetrics {
	m := &ConsumerMetrics{
		records: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kafka_consumer_records_total",
			Help: "Records handled, by group, origin topic and result (ok, retry, dlq, skipped).",
		}, []string{"group", "topic", "result"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "kafka_consumer_handle_seconds",
			Help:    "Handler latency, by group and origin topic.",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		}, []string{"group", "topic"}),
		lag: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kafka_consumer_lag",
			Help: "Records not yet consumed, by group, topic and partition.",
		}, []string{"group", "topic", "partition"}),
		assigned: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kafka_consumer_assigned_partitions",
			Help: "Partitions a batch consumer holds in its group; 0 for long means it was put out and not let back in.",
		}, []string{"group"}),
		lastPoll: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kafka_consumer_last_poll_timestamp_seconds",
			Help: "When a batch consumer last came back from a poll (it polls at least every 30 seconds unless its handler is stuck).",
		}, []string{"group"}),
	}
	reg.MustRegister(m.records, m.duration, m.lag, m.assigned, m.lastPoll)
	return m
}

// ConsumerOptions configures a consumer.
type ConsumerOptions struct {
	// Group is the consumer group; the retry consumer uses Group+".retry".
	Group   string
	Topics  []string
	Handler Handler
	Logger  *slog.Logger
	Metrics *ConsumerMetrics
	// RetryDelays defaults to DefaultRetryDelays.
	RetryDelays []time.Duration
	// LagInterval is how often the lag gauge refreshes (default 30s).
	LagInterval time.Duration
}

// Consumer reads event topics with at-least-once delivery: offsets are
// committed after the batch is handled or parked on a retry or dead-letter
// topic. It is an app component.
type Consumer struct {
	opts   ConsumerOptions
	main   *kgo.Client
	retry  *kgo.Client
	adm    *kadm.Client
	tracer trace.Tracer

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// NewConsumer joins the group on the business topics and on their retry
// topics.
func NewConsumer(ctx context.Context, cfg Config, opts ConsumerOptions) (*Consumer, error) {
	if opts.RetryDelays == nil {
		opts.RetryDelays = DefaultRetryDelays
	}
	if opts.LagInterval == 0 {
		opts.LagInterval = 30 * time.Second
	}
	// From here on topics and groups carry the namespace; the handler only
	// sees envelopes.
	opts.Group = cfg.Namespace + opts.Group
	opts.Topics = cfg.topics(opts.Topics...)
	retryTopics := make([]string, len(opts.Topics))
	for i, t := range opts.Topics {
		retryTopics[i] = t + ".retry"
	}
	client := func(group string, topics []string) (*kgo.Client, error) {
		return kgo.NewClient(
			kgo.SeedBrokers(cfg.Brokers...),
			kgo.ClientID(group),
			kgo.ConsumerGroup(group),
			kgo.ConsumeTopics(topics...),
			kgo.DisableAutoCommit(),
			kgo.BlockRebalanceOnPoll(),
			kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
			kgo.RequiredAcks(kgo.AllISRAcks()),
		)
	}
	main, err := client(opts.Group, opts.Topics)
	if err != nil {
		return nil, fmt.Errorf("kafka consumer %s: %w", opts.Group, err)
	}
	retry, err := client(opts.Group+".retry", retryTopics)
	if err != nil {
		main.Close()
		return nil, fmt.Errorf("kafka consumer %s: %w", opts.Group, err)
	}
	if err := main.Ping(ctx); err != nil {
		main.Close()
		retry.Close()
		return nil, fmt.Errorf("kafka consumer %s: ping: %w", opts.Group, err)
	}
	cctx, cancel := context.WithCancel(context.Background())
	return &Consumer{
		opts:   opts,
		main:   main,
		retry:  retry,
		adm:    kadm.NewClient(main),
		tracer: tracing.Tracer("kafka"),
		ctx:    cctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}, nil
}

// Run consumes until Stop.
func (c *Consumer) Run() error {
	defer close(c.done)
	c.opts.Logger.Info("kafka consumer started", "group", c.opts.Group, "topics", c.opts.Topics)
	var wg sync.WaitGroup
	wg.Go(func() { c.loop(c.main, false) })
	wg.Go(func() { c.loop(c.retry, true) })
	wg.Go(func() { lagLoop(c.ctx, c.adm, c.opts.Metrics, c.opts.LagInterval, c.opts.Group, c.opts.Group+".retry") })
	wg.Wait()
	return nil
}

// Stop finishes the record in hand, commits, and leaves the groups.
func (c *Consumer) Stop(ctx context.Context) error {
	c.cancel()
	select {
	case <-c.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	c.main.Close()
	c.retry.Close()
	return nil
}

func (c *Consumer) loop(cl *kgo.Client, retrying bool) {
	for {
		fetches := cl.PollRecords(c.ctx, 500)
		if fetches.IsClientClosed() || c.ctx.Err() != nil {
			cl.AllowRebalance()
			return
		}
		fetches.EachError(func(topic string, p int32, err error) {
			if !errors.Is(err, context.Canceled) {
				c.opts.Logger.Warn("kafka fetch error", "group", c.opts.Group, "topic", topic, "partition", p, "error", err)
			}
		})
		var handled []*kgo.Record
		fetches.EachRecord(func(r *kgo.Record) {
			if c.ctx.Err() != nil {
				return
			}
			if err := c.handleRecord(cl, r, retrying); err != nil {
				return // shutting down before the record was settled: redelivered later
			}
			handled = append(handled, r)
		})
		if len(handled) > 0 {
			// Commit with a fresh context so a shutdown still records progress.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := cl.CommitRecords(ctx, handled...); err != nil {
				c.opts.Logger.Warn("kafka commit failed; records will be redelivered", "group", c.opts.Group, "error", err)
			}
			cancel()
		}
		cl.AllowRebalance()
	}
}

// handleRecord settles one record: handled, parked for retry, parked in
// the dead-letter topic, or skipped. It returns an error only when the
// consumer stops before the record is settled.
func (c *Consumer) handleRecord(cl *kgo.Client, r *kgo.Record, retrying bool) error {
	origin, attempt := r.Topic, 0
	if retrying {
		if header(r, HeaderGroup) != c.opts.Group {
			return nil // parked by another group
		}
		origin = header(r, HeaderOriginTopic)
		attempt, _ = strconv.Atoi(header(r, HeaderAttempt))
		if ms, err := strconv.ParseInt(header(r, HeaderNotBefore), 10, 64); err == nil {
			if wait := time.Until(time.UnixMilli(ms)); wait > 0 {
				select {
				case <-c.ctx.Done():
					return c.ctx.Err()
				case <-time.After(wait):
				}
			}
		}
	}

	env, _, err := event.Decode(r.Value)
	if err != nil {
		return c.park(cl, r, parked{origin: origin, topic: origin + ".dlq", attempt: attempt, cause: err})
	}

	start := time.Now()
	err = c.handle(env, origin)
	if c.opts.Metrics != nil {
		c.opts.Metrics.duration.WithLabelValues(c.opts.Group, origin).Observe(time.Since(start).Seconds())
	}
	if err == nil {
		count(c.opts.Metrics, c.opts.Group, origin, "ok")
		return nil
	}
	if attempt < len(c.opts.RetryDelays) {
		c.opts.Logger.Warn("event handling failed; retrying", "group", c.opts.Group, "event_id", env.GetEventId(),
			"event_type", env.GetEventType(), "attempt", attempt+1, "error", err)
		return c.park(cl, r, parked{
			origin: origin, topic: origin + ".retry", attempt: attempt + 1,
			notBefore: time.Now().Add(c.opts.RetryDelays[attempt]), cause: err,
		})
	}
	c.opts.Logger.Error("event handling failed; sent to dead-letter topic", "group", c.opts.Group,
		"event_id", env.GetEventId(), "event_type", env.GetEventType(), "error", err)
	return c.park(cl, r, parked{origin: origin, topic: origin + ".dlq", attempt: attempt, cause: err})
}

// park moves the record to its retry or dead-letter topic.
func (c *Consumer) park(cl *kgo.Client, r *kgo.Record, p parked) error {
	p.group = c.opts.Group
	if err := parkRecord(c.ctx, cl, c.opts.Logger, r, p); err != nil {
		return err
	}
	result := "dlq"
	if !p.notBefore.IsZero() {
		result = "retry"
	}
	count(c.opts.Metrics, c.opts.Group, p.origin, result)
	return nil
}

// handle runs the handler with the event's trace, causation and log
// context, turning a panic into an error.
func (c *Consumer) handle(env *eventv1.Envelope, origin string) (err error) {
	ctx := event.ContextFrom(c.ctx, env)
	ctx, span := c.tracer.Start(ctx, "consume "+env.GetEventType(), trace.WithSpanKind(trace.SpanKindConsumer))
	defer span.End()
	ctx = event.WithCause(ctx, env)
	ctx = logging.WithAttrs(ctx,
		slog.String("event_id", env.GetEventId()),
		slog.String("event_type", env.GetEventType()),
		slog.String("topic", origin),
	)
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v\n%s", p, debug.Stack())
		}
		if err != nil {
			span.SetStatus(otelcodes.Error, err.Error())
		}
	}()
	return c.opts.Handler(ctx, env)
}

func header(r *kgo.Record, key string) string {
	for _, h := range r.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}
