package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	eventv1 "github.com/skill/exchange/api/gen/go/exchange/event/v1"
	"github.com/skill/exchange/internal/platform/event"
)

// Delivery is one decoded record handed to a batch handler.
type Delivery struct {
	Topic     string // logical, without the namespace
	Partition int32
	Offset    int64
	Envelope  *eventv1.Envelope
}

// BatchHandler stores a batch. An error retries the same batch after a
// backoff, so it must be idempotent; offsets are committed only after it
// succeeds.
type BatchHandler func(ctx context.Context, batch []Delivery) error

// BatchOptions configures a batch consumer.
type BatchOptions struct {
	Group   string
	Topics  []string
	Handler BatchHandler
	Logger  *slog.Logger
	Metrics *ConsumerMetrics
	// MaxBatch caps a batch (default 10,000 records).
	MaxBatch int
	// MaxWait bounds how long a batch fills after its first record
	// (default 1s).
	MaxWait time.Duration
	// LagInterval is how often the lag gauge refreshes (default 30s).
	LagInterval time.Duration
	// OnAssigned runs whenever the group hands the consumer partitions,
	// at the start and after a rejoin, before it polls them; the matching
	// engine reads the reference books to their end there.
	OnAssigned func(ctx context.Context)
}

// Readiness of a batch consumer: it comes back from a poll at least every
// pollIdle when nothing arrives, and holds partitions within unassignedFor
// of starting or of losing them.
const (
	pollIdle      = 30 * time.Second
	pollStale     = 2 * time.Minute
	unassignedFor = 2 * time.Minute
)

// BatchConsumer feeds bulk sinks such as ClickHouse (requirements §9:
// flush every second or 10,000 rows). Undecodable records go to the
// dead-letter topic; a failing sink is retried, never skipped.
type BatchConsumer struct {
	opts BatchOptions
	ns   string
	cl   *kgo.Client
	adm  *kadm.Client

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	assigned  atomic.Int64 // partitions held
	changedAt atomic.Int64 // when they last changed, unix nanoseconds
	lastPoll  atomic.Int64 // when a poll last returned, unix nanoseconds
}

// NewBatchConsumer joins the group on topics.
func NewBatchConsumer(ctx context.Context, cfg Config, opts BatchOptions) (*BatchConsumer, error) {
	if opts.MaxBatch <= 0 {
		opts.MaxBatch = 10_000
	}
	if opts.MaxWait <= 0 {
		opts.MaxWait = time.Second
	}
	if opts.LagInterval <= 0 {
		opts.LagInterval = 30 * time.Second
	}
	opts.Group = cfg.Namespace + opts.Group
	opts.Topics = cfg.topics(opts.Topics...)
	cctx, cancel := context.WithCancel(context.Background())
	c := &BatchConsumer{opts: opts, ns: cfg.Namespace, ctx: cctx, cancel: cancel, done: make(chan struct{})}
	now := time.Now().UnixNano()
	c.changedAt.Store(now)
	c.lastPoll.Store(now)
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ClientID(opts.Group),
		kgo.ConsumerGroup(opts.Group),
		kgo.ConsumeTopics(opts.Topics...),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.OnPartitionsAssigned(c.onAssigned),
		kgo.OnPartitionsRevoked(c.onLost),
		kgo.OnPartitionsLost(c.onLost),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("kafka batch consumer %s: %w", opts.Group, err)
	}
	if err := cl.Ping(ctx); err != nil {
		cl.Close()
		cancel()
		return nil, fmt.Errorf("kafka batch consumer %s: ping: %w", opts.Group, err)
	}
	c.cl, c.adm = cl, kadm.NewClient(cl)
	// The series exist from the start: a consumer that never gets into
	// its group shows 0 partitions (KafkaConsumerUnassigned).
	c.setAssigned(0)
	c.touchPoll(time.Unix(0, now))
	return c, nil
}

// touchPoll records a poll's return (Ready, the last-poll gauge).
func (c *BatchConsumer) touchPoll(at time.Time) {
	c.lastPoll.Store(at.UnixNano())
	if c.opts.Metrics != nil {
		c.opts.Metrics.lastPoll.WithLabelValues(c.opts.Group).Set(float64(at.Unix()))
	}
}

func partitions(m map[string][]int32) int64 {
	n := 0
	for _, ps := range m {
		n += len(ps)
	}
	return int64(n)
}

// onAssigned counts the partitions the group handed over and runs
// OnAssigned.
func (c *BatchConsumer) onAssigned(ctx context.Context, _ *kgo.Client, assigned map[string][]int32) {
	c.setAssigned(c.assigned.Add(partitions(assigned)))
	c.opts.Logger.Info("kafka batch consumer assigned partitions", "group", c.opts.Group, "partitions", c.assigned.Load())
	if c.opts.OnAssigned == nil {
		return
	}
	// The group's goroutine runs the hook while polls wait for it: a long
	// catch-up is the consumer at work, not stuck.
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case at := <-t.C:
				c.touchPoll(at)
			}
		}
	}()
	c.opts.OnAssigned(ctx)
	close(done)
}

// onLost counts the partitions revoked by a rebalance, or lost when the
// group put the consumer out.
func (c *BatchConsumer) onLost(_ context.Context, _ *kgo.Client, lost map[string][]int32) {
	c.setAssigned(max(c.assigned.Add(-partitions(lost)), 0))
}

func (c *BatchConsumer) setAssigned(n int64) {
	c.assigned.Store(n)
	c.changedAt.Store(time.Now().UnixNano())
	if c.opts.Metrics != nil {
		c.opts.Metrics.assigned.WithLabelValues(c.opts.Group).Set(float64(n))
	}
}

// Ready reports a consumer that stopped polling (its handler stuck on a
// sink that keeps failing), or that has held no partitions for a while:
// put out of its group and not back in (2026-10-02: after Redpanda was
// recreated every batch consumer stalled so, with the producer's ping
// still green).
func (c *BatchConsumer) Ready(context.Context) error {
	now := time.Now()
	if d := now.Sub(time.Unix(0, c.lastPoll.Load())); d > pollStale {
		return fmt.Errorf("group %s: no poll for %s", c.opts.Group, d.Round(time.Second))
	}
	if d := now.Sub(time.Unix(0, c.changedAt.Load())); c.assigned.Load() == 0 && d > unassignedFor {
		return fmt.Errorf("group %s: no partitions for %s", c.opts.Group, d.Round(time.Second))
	}
	return nil
}

// Run consumes until Stop.
func (c *BatchConsumer) Run() error {
	defer close(c.done)
	c.opts.Logger.Info("kafka batch consumer started", "group", c.opts.Group, "topics", c.opts.Topics)
	go lagLoop(c.ctx, c.adm, c.opts.Metrics, c.opts.LagInterval, c.opts.Group)
	for c.ctx.Err() == nil {
		c.cycle()
	}
	return nil
}

// Stop finishes the batch in hand and leaves the group.
func (c *BatchConsumer) Stop(ctx context.Context) error {
	c.cancel()
	select {
	case <-c.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	c.cl.Close()
	return nil
}

// cycle fills one batch, stores it and commits it.
func (c *BatchConsumer) cycle() {
	defer c.cl.AllowRebalance()
	var batch []Delivery
	var recs []*kgo.Record
	var first time.Time
	for len(recs) < c.opts.MaxBatch {
		// The first poll of a batch waits pollIdle at most, so that an idle
		// consumer still comes back (Ready, AllowRebalance).
		wait := pollIdle
		if !first.IsZero() {
			if wait = c.opts.MaxWait - time.Since(first); wait <= 0 {
				break
			}
		}
		pollCtx, cancel := context.WithTimeout(c.ctx, wait)
		fetches := c.cl.PollRecords(pollCtx, c.opts.MaxBatch-len(recs))
		cancel()
		if fetches.IsClientClosed() || c.ctx.Err() != nil {
			return // uncommitted records are redelivered after restart
		}
		c.touchPoll(time.Now())
		fetches.EachError(func(topic string, p int32, err error) {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				c.opts.Logger.Warn("kafka fetch error", "group", c.opts.Group, "topic", topic, "partition", p, "error", err)
			}
		})
		fetches.EachRecord(func(r *kgo.Record) {
			env, _, err := event.Decode(r.Value)
			if err != nil {
				if perr := parkRecord(c.ctx, c.cl, c.opts.Logger, r, parked{origin: r.Topic, topic: r.Topic + ".dlq", group: c.opts.Group, cause: err}); perr != nil {
					return
				}
				count(c.opts.Metrics, c.opts.Group, r.Topic, "dlq")
			} else {
				batch = append(batch, Delivery{Topic: strings.TrimPrefix(r.Topic, c.ns), Partition: r.Partition, Offset: r.Offset, Envelope: env})
			}
			recs = append(recs, r)
		})
		if len(recs) == 0 {
			// Nothing came (an error, the group rejoining): end the cycle so
			// that AllowRebalance runs. Polling on with rebalances blocked
			// never lets the group back in after the broker restarts
			// (2026-10-02: every batch consumer stalled after Redpanda was
			// recreated, the engines among them, until restarted).
			return
		}
		if first.IsZero() {
			first = time.Now()
		}
	}
	if len(recs) == 0 {
		return
	}
	if len(batch) > 0 && !c.store(batch) {
		return // stopping before the sink accepted the batch
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.cl.CommitRecords(ctx, recs...); err != nil {
		c.opts.Logger.Warn("kafka commit failed; records will be redelivered", "group", c.opts.Group, "error", err)
	}
	for _, d := range batch {
		count(c.opts.Metrics, c.opts.Group, c.ns+d.Topic, "ok")
	}
}

// store hands the batch to the sink until it succeeds or the consumer
// stops.
func (c *BatchConsumer) store(batch []Delivery) bool {
	for backoff := time.Second; ; backoff = min(backoff*2, 30*time.Second) {
		start := time.Now()
		err := c.opts.Handler(c.ctx, batch)
		if c.opts.Metrics != nil {
			c.opts.Metrics.duration.WithLabelValues(c.opts.Group, "batch").Observe(time.Since(start).Seconds())
		}
		if err == nil {
			return true
		}
		c.opts.Logger.Warn("batch store failed; retrying", "group", c.opts.Group, "records", len(batch),
			"error", err, "retry_in", backoff.String())
		select {
		case <-c.ctx.Done():
			return false
		case <-time.After(backoff):
		}
	}
}

// parked describes where a record goes when it cannot be handled.
type parked struct {
	origin, topic, group string
	attempt              int
	notBefore            time.Time
	cause                error
}

// parkRecord copies r to a retry or dead-letter topic with the context
// headers, retrying the write until it succeeds or ctx ends.
func parkRecord(ctx context.Context, cl *kgo.Client, log *slog.Logger, r *kgo.Record, p parked) error {
	msg := p.cause.Error()
	if len(msg) > 1024 {
		msg = msg[:1024]
	}
	if p.notBefore.IsZero() {
		p.notBefore = time.Now()
	}
	out := &kgo.Record{
		Topic: p.topic,
		Key:   r.Key,
		Value: r.Value,
		Headers: []kgo.RecordHeader{
			{Key: HeaderOriginTopic, Value: []byte(p.origin)},
			{Key: HeaderGroup, Value: []byte(p.group)},
			{Key: HeaderAttempt, Value: []byte(strconv.Itoa(p.attempt))},
			{Key: HeaderNotBefore, Value: []byte(strconv.FormatInt(p.notBefore.UnixMilli(), 10))},
			{Key: HeaderError, Value: []byte(msg)},
		},
	}
	for backoff := time.Second; ; backoff = min(backoff*2, 30*time.Second) {
		err := cl.ProduceSync(ctx, out).FirstErr()
		if err == nil {
			return nil
		}
		log.Warn("kafka park failed", "topic", p.topic, "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
}

func count(m *ConsumerMetrics, group, topic, result string) {
	if m != nil {
		m.records.WithLabelValues(group, topic, result).Inc()
	}
}

// lagLoop refreshes the lag gauge of groups, at once and then every
// interval, until ctx ends.
func lagLoop(ctx context.Context, adm *kadm.Client, m *ConsumerMetrics, interval time.Duration, groups ...string) {
	if m == nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if lags, err := adm.Lag(ctx, groups...); err == nil {
			for _, gl := range lags {
				for topic, parts := range gl.Lag {
					for p, l := range parts {
						if l.Lag >= 0 {
							m.lag.WithLabelValues(gl.Group, topic, strconv.Itoa(int(p))).Set(float64(l.Lag))
						}
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
