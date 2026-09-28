// Package application runs the matching engine (requirements §5.7,
// ADR-0002): commands from order.commands are applied to in-memory books
// and, per batch in one transaction, written to the WAL together with the
// events they produced (published through the outbox) and, now and then,
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

// Group is the engine's consumer group on order.commands.
const Group = "matching-engine"

// Engine applies commands to the books of the partitions it consumes.
type Engine struct {
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
	applied int64 // offset of the last command applied; -1 for none
	books   map[string]*domain.Book
	since   int // commands since the last snapshot
}

// New returns an engine; call Recover before Handle.
func New(store ports.Store, events *event.Factory, log *slog.Logger, reg prometheus.Registerer, snapshotEvery int) *Engine {
	e := &Engine{
		store: store, events: events, log: log, now: time.Now, snapshotEvery: snapshotEvery, dirty: true,
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
		st = &partition{applied: -1, books: map[string]*domain.Book{}}
		e.parts[p] = st
	}
	return st
}

func (st *partition) book(symbol string) *domain.Book {
	b, ok := st.books[symbol]
	if !ok {
		b = domain.NewBook(symbol)
		st.books[symbol] = b
	}
	return b
}

// Recover rebuilds the books from the latest snapshots and the commands
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
		st.applied = s.Offset
		after[s.Partition] = s.Offset
	}
	wal, err := e.store.WAL(ctx, after)
	if err != nil {
		return err
	}
	for _, w := range wal {
		var env eventv1.Envelope
		if err := proto.Unmarshal(w.Command, &env); err != nil {
			return fmt.Errorf("wal %d:%d: %w", w.Partition, w.Offset, err)
		}
		st := e.partition(w.Partition)
		if _, _, err := apply(st, &env); err != nil {
			return fmt.Errorf("wal %d:%d: %w", w.Partition, w.Offset, err)
		}
		st.applied = w.Offset
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

// Handle applies a batch of commands (kafka.BatchHandler). Commands the
// WAL already holds (a redelivery) are skipped. If the batch cannot be
// saved, the books are rebuilt before the retry, so memory never runs
// ahead of the log.
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
		if d.Offset <= st.applied {
			continue
		}
		symbol, evs, err := apply(st, d.Envelope)
		if err != nil {
			// A malformed command would block the partition if retried; it
			// changes nothing, so it is skipped (and never in the WAL).
			e.log.ErrorContext(ctx, "invalid command skipped", "partition", d.Partition, "offset", d.Offset,
				"event_id", d.Envelope.GetEventId(), "error", err)
			e.commands.WithLabelValues("invalid").Inc()
			st.applied = d.Offset
			continue
		}
		raw, err := proto.Marshal(d.Envelope)
		if err != nil {
			e.dirty = true
			return err
		}
		wal = append(wal, ports.WALEntry{Partition: d.Partition, Offset: d.Offset, Symbol: symbol, Command: raw, AppliedAt: now})
		cmdCtx := event.ContextFrom(ctx, d.Envelope) // events continue the order's trace
		for _, ev := range evs {
			o, err := e.output(cmdCtx, ev)
			if err != nil {
				e.dirty = true
				return err
			}
			out = append(out, o)
		}
		e.commands.WithLabelValues(string(proto.MessageName(payloadOf(d.Envelope)).Name())).Inc()
		st.applied = d.Offset
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
		if o.Topic == TopicTrade {
			e.trades.Inc()
		}
	}
	return nil
}

func snapshotOf(p int32, st *partition, now time.Time) ports.Snapshot {
	s := ports.Snapshot{Partition: p, Offset: st.applied, TakenAt: now}
	for _, b := range st.books {
		s.Books = append(s.Books, b.Snapshot())
	}
	return s
}

func payloadOf(env *eventv1.Envelope) proto.Message {
	if env.GetPayload().MessageIs(&orderv1.CancelOrder{}) {
		return &orderv1.CancelOrder{}
	}
	return &orderv1.PlaceOrder{}
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
		return o.Symbol, st.book(o.Symbol).Place(o), nil
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
