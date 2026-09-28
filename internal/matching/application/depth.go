package application

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	"github.com/lidp280504357/exchange/internal/matching/domain"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
)

// Depth export (requirements §11.8): the aggregated top of each book goes
// to market.depth straight from memory, not through the outbox; it is
// derived state, and a lost snapshot is replaced by the next.
const (
	DepthLevels   = 200
	DepthInterval = 100 * time.Millisecond
	DepthRefresh  = 10 * time.Second
)

// Depths returns the depth of the books that changed since the last call,
// or of every book when all is set, up to limit levels per side. It
// returns nothing while the books wait for a rebuild after a failed save,
// since they may run ahead of what was committed.
func (e *Engine) Depths(limit int, all bool) []domain.Depth {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dirty {
		return nil
	}
	var out []domain.Depth
	for _, st := range e.parts {
		for symbol, b := range st.books {
			if all || e.changed[symbol] {
				out = append(out, b.Depth(limit))
			}
		}
	}
	clear(e.changed)
	return out
}

// DepthExporter publishes the books' depth until its context ends: the
// changed books every DepthInterval, all books every DepthRefresh (so a
// consumer that starts late soon has every book).
type DepthExporter struct {
	engine    *Engine
	pub       kafka.Publisher
	events    *event.Factory
	published prometheus.Counter
	failed    prometheus.Counter
}

// NewDepthExporter registers the export metrics with reg.
func NewDepthExporter(engine *Engine, pub kafka.Publisher, events *event.Factory, reg prometheus.Registerer) *DepthExporter {
	x := &DepthExporter{
		engine: engine, pub: pub, events: events,
		published: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "matching_depth_published_total", Help: "Depth snapshots published to market.depth.",
		}),
		failed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "matching_depth_publish_failures_total", Help: "Depth exports that failed; the next one replaces them.",
		}),
	}
	reg.MustRegister(x.published, x.failed)
	return x
}

// Run exports until ctx ends (an app.Loop body).
func (x *DepthExporter) Run(ctx context.Context) error {
	tick := time.NewTicker(DepthInterval)
	defer tick.Stop()
	lastAll := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		all := time.Since(lastAll) >= DepthRefresh
		if all {
			lastAll = time.Now()
		}
		if err := x.Export(ctx, x.engine.Depths(DepthLevels, all)); err != nil && ctx.Err() == nil {
			x.failed.Inc()
			x.engine.log.WarnContext(ctx, "depth export failed", "error", err)
		}
	}
}

// Export publishes depths, one record per book keyed by symbol.
func (x *DepthExporter) Export(ctx context.Context, depths []domain.Depth) error {
	if len(depths) == 0 {
		return nil
	}
	recs := make([]kafka.Record, 0, len(depths))
	now := timestamppb.Now()
	for _, d := range depths {
		msg := &marketv1.DepthSnapshot{Symbol: d.Symbol, Sequence: d.Seq, Bids: levels(d.Bids), Asks: levels(d.Asks), TakenAt: now}
		env, err := x.events.New(ctx, msg, "symbol", d.Symbol)
		if err != nil {
			return err
		}
		raw, err := proto.Marshal(env)
		if err != nil {
			return fmt.Errorf("depth %s: %w", d.Symbol, err)
		}
		recs = append(recs, kafka.Record{Topic: event.TopicMarketDepth, Key: d.Symbol, EventType: env.GetEventType(), Envelope: raw})
	}
	if err := x.pub.Publish(ctx, recs...); err != nil {
		return err
	}
	x.published.Add(float64(len(recs)))
	return nil
}

func levels(in []domain.Level) []*marketv1.PriceLevel {
	out := make([]*marketv1.PriceLevel, len(in))
	for i, l := range in {
		out[i] = &marketv1.PriceLevel{Price: l.Price.String(), Quantity: l.Quantity.String()}
	}
	return out
}
