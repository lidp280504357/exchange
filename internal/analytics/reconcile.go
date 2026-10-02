package analytics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lidp280504357/exchange/internal/platform/pg"
)

// Result compares one topic in one window.
type Result struct {
	Topic      string
	Postgres   int64 // events published from the outboxes
	ClickHouse int64 // distinct events ingested
}

// Missing is how many published events have not reached ClickHouse.
func (r Result) Missing() int64 { return r.Postgres - r.ClickHouse }

// Reconciler checks acceptance criterion 8 of phase 1: every event
// published from a service outbox eventually shows up in ClickHouse. It
// reads the outbox tables of the listed schemas; this read-only audit is
// the one place allowed to look into other services' schemas.
type Reconciler struct {
	db      *pg.DB
	ch      driver.Conn
	schemas []string
	log     *slog.Logger
	// Window is how far back each run looks (a day at most): within the
	// outboxes' retention, less the minutes left to the consumer and a
	// margin, so that no purged row counts as missing (SetRetention).
	Window time.Duration

	missing *prometheus.GaugeVec
	lastRun prometheus.Gauge
}

// NewReconciler registers the reconciliation metrics with reg.
func NewReconciler(db *pg.DB, ch driver.Conn, schemas []string, log *slog.Logger, reg prometheus.Registerer) *Reconciler {
	r := &Reconciler{
		db: db, ch: ch, schemas: schemas, log: log, Window: window,
		missing: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "analytics_reconcile_missing",
			Help: "Events published in the last window but absent from ClickHouse, by topic.",
		}, []string{"topic"}),
		lastRun: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "analytics_reconcile_last_success_timestamp_seconds",
			Help: "When the last reconciliation finished.",
		}),
	}
	reg.MustRegister(r.missing, r.lastRun)
	return r
}

// Reconciliation window: the last day, leaving the most recent minutes to
// the consumer, which flushes every second but may be catching up.
const (
	window   = 24 * time.Hour
	settle   = 10 * time.Minute
	interval = time.Hour
)

// SetRetention fits the window within the outboxes' retention (the
// janitor deletes published rows older than it): what is left of it after
// the settling minutes and half an hour of margin, a day at most.
func (r *Reconciler) SetRetention(retention time.Duration) {
	r.Window = max(min(window, retention-settle-30*time.Minute), time.Minute)
}

// Run reconciles every hour until ctx ends.
func (r *Reconciler) Run(ctx context.Context) error {
	for {
		to := time.Now().Add(-settle).Truncate(time.Minute)
		results, err := r.Reconcile(ctx, to.Add(-r.Window), to)
		switch {
		case err != nil && ctx.Err() == nil:
			r.log.WarnContext(ctx, "reconciliation failed", "error", err)
		case err == nil:
			r.report(ctx, results)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

func (r *Reconciler) report(ctx context.Context, results []Result) {
	r.missing.Reset()
	for _, res := range results {
		r.missing.WithLabelValues(res.Topic).Set(float64(res.Missing()))
		switch {
		case res.Missing() > 0:
			r.log.WarnContext(ctx, "events missing from clickhouse", "topic", res.Topic,
				"published", res.Postgres, "ingested", res.ClickHouse)
		case res.Missing() < 0:
			// Something publishes from an outbox the reconciliation does not read.
			r.log.WarnContext(ctx, "clickhouse has events from an outbox not in RECONCILE_SCHEMAS", "topic", res.Topic,
				"published", res.Postgres, "ingested", res.ClickHouse)
		}
	}
	r.lastRun.SetToCurrentTime()
}

// Reconcile compares [from, to) by occurred_at, which outbox and ClickHouse
// both store at millisecond precision.
func (r *Reconciler) Reconcile(ctx context.Context, from, to time.Time) ([]Result, error) {
	byTopic := map[string]*Result{}
	get := func(topic string) *Result {
		if byTopic[topic] == nil {
			byTopic[topic] = &Result{Topic: topic}
		}
		return byTopic[topic]
	}

	for _, schema := range r.schemas {
		// Only the ingested topics: commands (order.commands) share the
		// outboxes but are not business events and stay out of ClickHouse.
		q := `SELECT topic, count(*) FROM ` + pgx.Identifier{schema, "outbox"}.Sanitize() + `
			WHERE published_at IS NOT NULL AND occurred_at >= $1 AND occurred_at < $2 AND topic = ANY($3) GROUP BY topic`
		rows, err := r.db.Query(ctx, q, from, to, Topics)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P01" { // undefined_table
			continue // the service has not created its schema yet
		}
		if err != nil {
			return nil, fmt.Errorf("reconcile %s: %w", schema, err)
		}
		for rows.Next() {
			var topic string
			var n int64
			if err := rows.Scan(&topic, &n); err != nil {
				rows.Close()
				return nil, fmt.Errorf("reconcile %s: %w", schema, err)
			}
			get(topic).Postgres += n
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("reconcile %s: %w", schema, err)
		}
	}

	rows, err := r.ch.Query(ctx, `SELECT topic, uniqExact(event_id) FROM events
		WHERE occurred_at >= ? AND occurred_at < ? GROUP BY topic`, from, to)
	if err != nil {
		return nil, fmt.Errorf("reconcile clickhouse: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var topic string
		var n uint64
		if err := rows.Scan(&topic, &n); err != nil {
			return nil, fmt.Errorf("reconcile clickhouse: %w", err)
		}
		get(topic).ClickHouse += int64(n) //nolint:gosec // counts fit easily
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reconcile clickhouse: %w", err)
	}

	out := make([]Result, 0, len(byTopic))
	for _, res := range byTopic {
		out = append(out, *res)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Topic < out[j].Topic })
	return out, nil
}
