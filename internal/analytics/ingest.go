// Package analytics feeds the ClickHouse read model (requirements §9): it
// ingests every business event in batches and reconciles the ingested
// counts with the outboxes of the producing services.
package analytics

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/anypb"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
)

// Topics are the business topics ingested in phase 1.
var Topics = []string{
	event.TopicAuth,
	event.TopicUser,
	event.TopicInstrument,
	event.TopicLedger,
	event.TopicAccount,
	event.TopicRisk,
	event.TopicAudit,
	event.TopicNotification,
}

// Ingestor writes event batches into the events table.
type Ingestor struct {
	conn     driver.Conn
	log      *slog.Logger
	rows     *prometheus.CounterVec
	rejected prometheus.Counter
}

// NewIngestor registers the ingest metrics with reg.
func NewIngestor(conn driver.Conn, log *slog.Logger, reg prometheus.Registerer) *Ingestor {
	in := &Ingestor{
		conn: conn,
		log:  log,
		rows: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "analytics_ingested_rows_total",
			Help: "Events written to ClickHouse, by topic.",
		}, []string{"topic"}),
		rejected: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "analytics_rejected_rows_total",
			Help: "Events dropped because they cannot be stored (malformed event_id).",
		}),
	}
	reg.MustRegister(in.rows, in.rejected)
	return in
}

// Store inserts a batch into events and the typed tables. The tables are
// ReplacingMergeTree, which collapses copies of an event, so storing a
// batch again after a failure is harmless.
func (in *Ingestor) Store(ctx context.Context, batch []kafka.Delivery) error {
	if err := in.storeEvents(ctx, batch); err != nil {
		return err
	}
	return in.storeAuditLogs(ctx, batch)
}

func (in *Ingestor) storeEvents(ctx context.Context, batch []kafka.Delivery) error {
	b, err := in.conn.PrepareBatch(ctx, `INSERT INTO events (event_id, event_type, event_version, topic, partition,
		offset, aggregate_type, aggregate_id, sequence, producer, correlation_id, causation_id, occurred_at, payload)`)
	if err != nil {
		return fmt.Errorf("clickhouse: prepare batch: %w", err)
	}
	perTopic := map[string]int{}
	for _, d := range batch {
		env := d.Envelope
		id, err := uuid.Parse(env.GetEventId())
		if err != nil {
			in.rejected.Inc()
			in.log.WarnContext(ctx, "event dropped: malformed event_id", "topic", d.Topic, "offset", d.Offset, "event_id", env.GetEventId())
			continue
		}
		err = b.Append(id, env.GetEventType(), env.GetEventVersion(), d.Topic, d.Partition, d.Offset,
			env.GetAggregateType(), env.GetAggregateId(), env.GetSequence(), env.GetProducer(),
			env.GetCorrelationId(), env.GetCausationId(), env.GetOccurredAt().AsTime(), PayloadJSON(env.GetPayload()))
		if err != nil {
			_ = b.Abort()
			return fmt.Errorf("clickhouse: append: %w", err)
		}
		perTopic[d.Topic]++
	}
	if b.Rows() == 0 {
		return b.Abort()
	}
	if err := b.Send(); err != nil {
		return fmt.Errorf("clickhouse: send batch: %w", err)
	}
	for topic, n := range perTopic {
		in.rows.WithLabelValues(topic).Add(float64(n))
	}
	return nil
}

// storeAuditLogs copies audit events into audit_logs, keyed by actor.
func (in *Ingestor) storeAuditLogs(ctx context.Context, batch []kafka.Delivery) error {
	var audit []kafka.Delivery
	for _, d := range batch {
		if d.Topic == event.TopicAudit {
			if _, err := uuid.Parse(d.Envelope.GetEventId()); err == nil {
				audit = append(audit, d)
			}
		}
	}
	if len(audit) == 0 {
		return nil
	}
	b, err := in.conn.PrepareBatch(ctx, `INSERT INTO audit_logs (event_id, event_type, actor_id, target, occurred_at, payload)`)
	if err != nil {
		return fmt.Errorf("clickhouse: prepare audit batch: %w", err)
	}
	for _, d := range audit {
		env := d.Envelope
		target := ""
		var changed auditv1.ConfigChanged
		if env.GetPayload().MessageIs(&changed) && env.GetPayload().UnmarshalTo(&changed) == nil {
			target = changed.GetTarget()
		}
		if err := b.Append(uuid.MustParse(env.GetEventId()), env.GetEventType(), env.GetAggregateId(), target,
			env.GetOccurredAt().AsTime(), PayloadJSON(env.GetPayload())); err != nil {
			_ = b.Abort()
			return fmt.Errorf("clickhouse: append audit: %w", err)
		}
	}
	if err := b.Send(); err != nil {
		return fmt.Errorf("clickhouse: send audit batch: %w", err)
	}
	return nil
}

// PayloadJSON renders the payload with protojson when its type is linked
// into the binary, and otherwise keeps the type URL and the raw bytes.
func PayloadJSON(p *anypb.Any) string {
	if p == nil {
		return "{}"
	}
	if b, err := protojson.Marshal(p); err == nil {
		return string(b)
	}
	b, _ := json.Marshal(map[string]string{"@type": p.GetTypeUrl(), "@raw": base64.StdEncoding.EncodeToString(p.GetValue())})
	return string(b)
}
