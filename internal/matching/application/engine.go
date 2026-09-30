// Package application runs the matching engine (requirements §5.7,
// ADR-0002): commands from order.commands and reference books from
// order.references (ADR-0015) are applied to in-memory books and, per
// batch in one transaction, written to the WAL together with the events
// they produced (published through the outbox) and, now and then,
// snapshots. On start, and after a failed write, the books are rebuilt
// from the latest snapshots and the WAL.
package application

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"

	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	orderv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/order/v1"
	"github.com/lidp280504357/exchange/internal/matching/domain"
	"github.com/lidp280504357/exchange/internal/matching/ports"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
)

// Group is the spot engine's consumer group on order.commands; the
// contracts' shard uses its own (MATCHING_GROUP).
const Group = "matching-engine"

// Engine applies commands to the books of the partitions it consumes.
type Engine struct {
	// Topics of the shard; New sets SpotTopics.
	Topics Topics

	store  ports.Store
	events *event.Factory
	log    *slog.Logger
	now    func() time.Time
	// snapshotEvery is how many commands of a partition may follow its last
	// snapshot before a new one is taken.
	snapshotEvery int

	// mu guards the books between Handle and the depth export.
	mu    sync.Mutex
	parts map[int32]*partition
	dirty bool
	// changed holds the symbols whose book changed since the last depth
	// export.
	changed map[string]bool

	commands *prometheus.CounterVec
	trades   prometheus.Counter
}

type partition struct {
	seq int64 // WAL position of the last entry applied; -1 for none
	// applied is, per source (ports.SourceCommands, SourceReferences),
	// the offset of the last entry applied; -1 for none.
	applied map[string]int64
	books   map[string]*domain.Book
	since   int // entries since the last snapshot
}

// New returns an engine; call Recover before Handle.
func New(store ports.Store, events *event.Factory, log *slog.Logger, reg prometheus.Registerer, snapshotEvery int) *Engine {
	e := &Engine{
		Topics: SpotTopics, store: store, events: events, log: log, now: time.Now, snapshotEvery: snapshotEvery, dirty: true,
		changed: map[string]bool{},
		commands: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "matching_commands_total",
			Help: "Commands applied by the engine, by type (PlaceOrder, CancelOrder).",
		}, []string{"type"}),
		trades: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "matching_trades_total",
			Help: "Trades executed by the engine.",
		}),
	}
	reg.MustRegister(e.commands, e.trades)
	return e
}

func (e *Engine) partition(p int32) *partition {
	st, ok := e.parts[p]
	if !ok {
		st = &partition{
			seq: -1, applied: map[string]int64{ports.SourceCommands: -1, ports.SourceReferences: -1}, books: map[string]*domain.Book{},
		}
		e.parts[p] = st
	}
	return st
}

// source names the input a delivery came from.
func (e *Engine) source(topic string) string {
	if topic == e.Topics.References {
		return ports.SourceReferences
	}
	return ports.SourceCommands
}

func (st *partition) book(symbol string) *domain.Book {
	b, ok := st.books[symbol]
	if !ok {
		b = domain.NewBook(symbol)
		st.books[symbol] = b
	}
	return b
}

// Recover rebuilds the books from the latest snapshots and the WAL entries
// after them, without publishing anything again.
func (e *Engine) Recover(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.recover(ctx)
}

func (e *Engine) recover(ctx context.Context) error {
	e.parts = map[int32]*partition{}
	snaps, err := e.store.Snapshots(ctx)
	if err != nil {
		return err
	}
	after := map[int32]int64{}
	for _, s := range snaps {
		st := e.partition(s.Partition)
		for _, b := range s.Books {
			st.books[b.Symbol] = domain.Restore(b)
		}
		st.seq = s.Seq
		st.applied[ports.SourceCommands], st.applied[ports.SourceReferences] = s.Offset, s.RefOffset
		after[s.Partition] = s.Seq
	}
	wal, err := e.store.WAL(ctx, after)
	if err != nil {
		return err
	}
	for _, w := range wal {
		var env eventv1.Envelope
		if err := proto.Unmarshal(w.Command, &env); err != nil {
			return fmt.Errorf("wal %d:%d: %w", w.Partition, w.Seq, err)
		}
		st := e.partition(w.Partition)
		if _, _, err := apply(st, &env); err != nil {
			return fmt.Errorf("wal %d:%d: %w", w.Partition, w.Seq, err)
		}
		st.seq, st.applied[w.Source] = w.Seq, w.Offset
		st.since++
	}
	e.dirty = false
	// The rebuilt books may differ from what was exported last.
	for _, st := range e.parts {
		for symbol := range st.books {
			e.changed[symbol] = true
		}
	}
	e.log.InfoContext(ctx, "matching engine recovered", "snapshots", len(snaps), "replayed", len(wal), "books", e.bookCount())
	return nil
}

func (e *Engine) bookCount() int {
	n := 0
	for _, st := range e.parts {
		n += len(st.books)
	}
	return n
}

// Handle applies a batch of commands and reference books
// (kafka.BatchHandler). Entries the WAL already holds (a redelivery) are
// skipped. If the batch cannot be saved, the books are rebuilt before the
// retry, so memory never runs ahead of the log.
func (e *Engine) Handle(ctx context.Context, batch []kafka.Delivery) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dirty {
		if err := e.recover(ctx); err != nil {
			return err
		}
	}
	var wal []ports.WALEntry
	var out []ports.Output
	touched := map[int32]bool{}
	now := e.now()
	for _, d := range batch {
		st := e.partition(d.Partition)
		source := e.source(d.Topic)
		if d.Offset <= st.applied[source] {
			continue
		}
		symbol, evs, err := apply(st, d.Envelope)
		if err != nil {
			// A malformed command would block the partition if retried; it
			// changes nothing, so it is skipped (and never in the WAL).
			e.log.ErrorContext(ctx, "invalid command skipped", "partition", d.Partition, "offset", d.Offset,
				"source", source, "event_id", d.Envelope.GetEventId(), "error", err)
			e.commands.WithLabelValues("invalid").Inc()
			st.applied[source] = d.Offset
			continue
		}
		raw, err := proto.Marshal(d.Envelope)
		if err != nil {
			e.dirty = true
			return err
		}
		st.seq++
		wal = append(wal, ports.WALEntry{
			Partition: d.Partition, Seq: st.seq, Source: source, Offset: d.Offset, Symbol: symbol, Command: raw, AppliedAt: now,
		})
		cmdCtx := event.ContextFrom(ctx, d.Envelope) // events continue the order's trace
		for _, ev := range evs {
			o, err := e.output(cmdCtx, ev)
			if err != nil {
				e.dirty = true
				return err
			}
			out = append(out, o)
		}
		e.commands.WithLabelValues(commandName(d.Envelope)).Inc()
		st.applied[source] = d.Offset
		st.since++
		touched[d.Partition] = true
	}
	if len(wal) == 0 {
		return nil
	}
	var snaps []ports.Snapshot
	for p := range touched {
		st := e.parts[p]
		if st.since >= e.snapshotEvery {
			snaps = append(snaps, snapshotOf(p, st, now))
		}
	}
	if err := e.store.Save(ctx, wal, out, snaps); err != nil {
		e.dirty = true
		return err
	}
	for _, s := range snaps {
		e.parts[s.Partition].since = 0
	}
	for _, w := range wal {
		e.changed[w.Symbol] = true
	}
	for _, o := range out {
		if o.Topic == e.Topics.Trades {
			e.trades.Inc()
		}
	}
	return nil
}

func snapshotOf(p int32, st *partition, now time.Time) ports.Snapshot {
	s := ports.Snapshot{
		Partition: p, Seq: st.seq, Offset: st.applied[ports.SourceCommands], RefOffset: st.applied[ports.SourceReferences], TakenAt: now,
	}
	for _, b := range st.books {
		s.Books = append(s.Books, b.Snapshot())
	}
	return s
}

// commandName is the metric label of a command: its message name
// (PlaceOrder, CancelOrder, ReferenceBookUpdate).
func commandName(env *eventv1.Envelope) string {
	for _, m := range []proto.Message{&orderv1.PlaceOrder{}, &orderv1.CancelOrder{}, &orderv1.ReferenceBookUpdate{}} {
		if env.GetPayload().MessageIs(m) {
			return string(proto.MessageName(m).Name())
		}
	}
	return "unknown"
}

// issued is when a command was issued; zero when its envelope has no time.
func issued(env *eventv1.Envelope) time.Time {
	if env.GetOccurredAt() == nil {
		return time.Time{}
	}
	return env.GetOccurredAt().AsTime()
}

// apply runs one command on its book and returns the book's symbol and
// the events.
func apply(st *partition, env *eventv1.Envelope) (string, []domain.Event, error) {
	switch {
	case env.GetPayload().MessageIs(&orderv1.PlaceOrder{}):
		var cmd orderv1.PlaceOrder
		if err := env.GetPayload().UnmarshalTo(&cmd); err != nil {
			return "", nil, err
		}
		o, err := fromProto(cmd.GetOrder())
		if err != nil {
			return "", nil, err
		}
		o.HouseOnly, o.At = cmd.GetHouseOnly(), issued(env)
		return o.Symbol, st.book(o.Symbol).Place(o), nil
	case env.GetPayload().MessageIs(&orderv1.ReferenceBookUpdate{}):
		var cmd orderv1.ReferenceBookUpdate
		if err := env.GetPayload().UnmarshalTo(&cmd); err != nil {
			return "", nil, err
		}
		r, err := referenceFromProto(&cmd, issued(env))
		if err != nil {
			return "", nil, err
		}
		return cmd.GetSymbol(), st.book(cmd.GetSymbol()).Reference(r), nil
	case env.GetPayload().MessageIs(&orderv1.CancelOrder{}):
		var cmd orderv1.CancelOrder
		if err := env.GetPayload().UnmarshalTo(&cmd); err != nil {
			return "", nil, err
		}
		return cmd.GetSymbol(), st.book(cmd.GetSymbol()).Cancel(cmd.GetOrderId(), cmd.GetUserId()), nil
	default:
		return "", nil, fmt.Errorf("unknown command %s", env.GetEventType())
	}
}
