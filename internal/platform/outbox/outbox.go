// Package outbox implements the transactional outbox (requirements §8.2):
// services add events in the same transaction as the business change, and
// the relay publishes them in insertion order. Delivery is at least once;
// consumers deduplicate on event_id.
package outbox

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"

	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
	"github.com/lidp280504357/exchange/internal/platform/pg"
)

// Add stores events for publication to topic; the partition key is each
// envelope's aggregate ID. Call it with the transaction of the business
// change.
func Add(ctx context.Context, q pg.Querier, topic string, envs ...*eventv1.Envelope) error {
	batch := &pgx.Batch{}
	for _, env := range envs {
		body, err := proto.Marshal(env)
		if err != nil {
			return fmt.Errorf("outbox: marshal %s: %w", env.GetEventType(), err)
		}
		batch.Queue(`INSERT INTO outbox (event_id, topic, partition_key, event_type, envelope) VALUES ($1, $2, $3, $4, $5)`,
			env.GetEventId(), topic, env.GetAggregateId(), env.GetEventType(), body)
	}
	if err := q.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("outbox: insert: %w", err)
	}
	return nil
}

// Publisher is the part of kafka.Producer the relay needs.
type Publisher interface {
	Publish(ctx context.Context, recs ...kafka.Record) error
}

// Relay publishes pending outbox rows. Run it as an app.Loop.
type Relay struct {
	db        *pg.DB
	pub       Publisher
	log       *slog.Logger
	batchSize int
	idle      time.Duration

	published prometheus.Counter
	failures  prometheus.Counter
	pending   prometheus.Gauge
}

// NewRelay returns a relay for db's outbox and registers its metrics.
func NewRelay(db *pg.DB, pub Publisher, log *slog.Logger, reg prometheus.Registerer) *Relay {
	labels := prometheus.Labels{"schema": db.Schema()}
	r := &Relay{
		db: db, pub: pub, log: log, batchSize: 100, idle: 200 * time.Millisecond,
		published: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "outbox_published_total", Help: "Events published from the outbox.", ConstLabels: labels,
		}),
		failures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "outbox_publish_failures_total", Help: "Failed publish attempts.", ConstLabels: labels,
		}),
		pending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "outbox_pending", Help: "Events waiting in the outbox.", ConstLabels: labels,
		}),
	}
	reg.MustRegister(r.published, r.failures, r.pending)
	return r
}

// Run publishes until ctx ends. Failures back off up to 30 seconds and the
// rows stay pending.
func (r *Relay) Run(ctx context.Context) error {
	backoff := time.Duration(0)
	lastCount := time.Time{}
	for {
		n, err := r.PublishBatch(ctx)
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case err != nil:
			r.failures.Inc()
			backoff = min(max(backoff*2, time.Second), 30*time.Second)
			r.log.WarnContext(ctx, "outbox publish failed", "error", err, "retry_in", backoff.String())
		case n == r.batchSize:
			backoff = 0
			continue // more rows are waiting
		default:
			backoff = r.idle
		}
		if time.Since(lastCount) > 15*time.Second {
			r.refreshPending(ctx)
			lastCount = time.Now()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
}

// PublishBatch publishes up to one batch of pending rows and marks them.
// Rows are locked with SKIP LOCKED, so replicas never publish the same row
// concurrently.
func (r *Relay) PublishBatch(ctx context.Context) (int, error) {
	var n int
	err := r.db.InTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, topic, partition_key, event_type, envelope FROM outbox
			WHERE published_at IS NULL ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED`, r.batchSize)
		if err != nil {
			return err
		}
		var ids []int64
		var recs []kafka.Record
		for rows.Next() {
			var id int64
			var rec kafka.Record
			if err := rows.Scan(&id, &rec.Topic, &rec.Key, &rec.EventType, &rec.Envelope); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
			recs = append(recs, rec)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(recs) == 0 {
			return nil
		}
		if err := r.pub.Publish(ctx, recs...); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE outbox SET published_at = now() WHERE id = ANY($1)`, ids); err != nil {
			return err
		}
		n = len(recs)
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("outbox: %w", err)
	}
	r.published.Add(float64(n))
	return n, nil
}

func (r *Relay) refreshPending(ctx context.Context) {
	var count int64
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&count); err == nil {
		r.pending.Set(float64(count))
	}
}

// Purge deletes rows published before cutoff and returns how many.
func Purge(ctx context.Context, q pg.Querier, cutoff time.Time) (int64, error) {
	tag, err := q.Exec(ctx, `DELETE FROM outbox WHERE published_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("outbox: purge: %w", err)
	}
	return tag.RowsAffected(), nil
}
