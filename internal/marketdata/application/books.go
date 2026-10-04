package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	marketv1 "github.com/skill/exchange/api/gen/go/exchange/market/v1"
	orderv1 "github.com/skill/exchange/api/gen/go/exchange/order/v1"
	"github.com/skill/exchange/internal/marketdata/domain"
	"github.com/skill/exchange/internal/marketdata/ports"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/kafka"
)

// The public books and trades (ADR-0010, ADR-0015).
const (
	// PublicDepth is how many levels a side the public books carry.
	PublicDepth = 200
	// bookPush is how often changed books and new trades go out, and
	// bookSnapshotEvery how often a full book does (a consumer that missed
	// an update waits at most this long).
	bookPush          = 100 * time.Millisecond
	bookSnapshotEvery = 10 * time.Second
	// bookStreamsPerConn caps the symbols of one stream connection (two
	// streams each; Binance allows 1024 streams, the design 50).
	bookStreamsPerConn = 25
	// bookRecent is how many recent trades a symbol keeps for REST.
	bookRecent = 100
	// BookStale is how long a book's stream connection may go without a
	// message before the book is not shown or used; a quiet book is still
	// current while its connection hears from the others.
	BookStale = 5 * time.Second
	// bookHeartbeat is how often a shown book that did not change sends an
	// empty update, so consumers can tell a quiet book from a lost one.
	bookHeartbeat = time.Second
)

// Books keeps a copy of the reference market's order book and its recent
// trades for every symbol that follows it (spot pairs from the spot
// market, perpetual contracts from the futures market), and publishes the
// public books (market.depth, derivatives.market.depth) and trades
// (market.trades): the reference market's for the symbols that show them
// (market.reference_depth), the platform's own, relayed from the engines
// and trade.events, for the others. Every symbol's public messages carry
// one sequence that only grows: it starts from the service's start time.
type Books struct {
	src    ports.BookSource
	refs   *ReferenceMap
	flags  Flags
	pub    kafka.Publisher
	events *event.Factory
	log    *slog.Logger
	now    func() time.Time
	// remap is how often the followed symbols are looked at.
	remap time.Duration

	mu    sync.Mutex
	books map[string]*bookState
	// seq is the last public sequence of each symbol.
	seq   map[string]int64
	epoch int64

	published *prometheus.CounterVec
	resyncs   prometheus.Counter
	failures  prometheus.Counter
}

type bookState struct {
	ref     ports.Reference
	futures bool
	local   *domain.LocalBook
	// updated is when the stream last changed the book; live is its
	// connection's latest message.
	updated time.Time
	live    *time.Time
	// shown is whether the last push published this book (the symbol
	// shows the reference market); sent is what it published last.
	shown   bool
	sent    [2][][2]string
	sentAt  time.Time
	beatAt  time.Time // the latest message sent
	changed bool
	recent  []domain.Trade // oldest first
	pending []domain.Trade // not published yet
}

// NewBooks returns the public books of the symbols refs maps, reading the
// reference market from src; register its metrics with reg.
func NewBooks(src ports.BookSource, refs *ReferenceMap, fl Flags, pub kafka.Publisher, events *event.Factory, log *slog.Logger,
	reg prometheus.Registerer,
) *Books {
	b := &Books{
		src: src, refs: refs, flags: fl, pub: pub, events: events, log: log, now: time.Now, remap: time.Minute,
		books: map[string]*bookState{}, seq: map[string]int64{}, epoch: time.Now().UnixMicro(),
		published: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_public_messages_total", Help: "Public book and trade messages published, by kind and source.",
		}, []string{"kind", "source"}),
		resyncs: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "market_reference_book_resyncs_total", Help: "Reference books loaded again from a snapshot after a gap.",
		}),
		failures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "market_reference_book_stream_failures_total", Help: "Reference book stream connections that failed.",
		}),
	}
	reg.MustRegister(b.published, b.resyncs, b.failures, bookAgeCollector{b})
	return b
}

// bookAgeCollector reports market_reference_book_age_seconds: how long
// since each followed book changed, -1 while it is not in sync.
type bookAgeCollector struct{ b *Books }

var bookAgeDesc = prometheus.NewDesc("market_reference_book_age_seconds",
	"Time since the reference book of a symbol last changed; -1 while it is not in sync.", []string{"symbol"}, nil)

func (c bookAgeCollector) Describe(ch chan<- *prometheus.Desc) { ch <- bookAgeDesc }

func (c bookAgeCollector) Collect(ch chan<- prometheus.Metric) {
	c.b.mu.Lock()
	defer c.b.mu.Unlock()
	now := c.b.now()
	for symbol, st := range c.b.books {
		age := -1.0
		if st.local.Synced() {
			age = now.Sub(st.updated).Seconds()
		}
		ch <- prometheus.MustNewConstMetric(bookAgeDesc, prometheus.GaugeValue, age, symbol)
	}
}

// isContract tells a perpetual contract's symbol from a pair's.
func isContract(symbol string) bool { return strings.HasSuffix(symbol, "-PERP") }

// shows reports whether symbol shows the reference market's book and
// trades: the reference feed is on and market.reference_depth allows it.
func (b *Books) shows(symbol string) bool {
	return b.flags.Enabled(flags.KeyReferenceFeed, flags.Subject{}) && b.flags.Enabled(flags.KeyReferenceDepth, flags.Subject{Symbol: symbol})
}

// nextSeq hands out the symbol's next public sequence; b.mu is held.
func (b *Books) nextSeq(symbol string) (prev, next int64) {
	prev = b.seq[symbol]
	if prev == 0 {
		prev = b.epoch
	}
	b.seq[symbol] = prev + 1
	return prev, prev + 1
}

// Run follows the reference market while market.reference_feed is on (an
// app.Loop body): one stream connection per group of symbols, spot and
// futures apart, restarted when the followed symbols change.
func (b *Books) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		if !b.flags.Enabled(flags.KeyReferenceFeed, flags.Subject{}) {
			b.drop()
			sleep(ctx, 10*time.Second)
			continue
		}
		followed := b.follow(ctx)
		if len(followed) == 0 {
			sleep(ctx, 10*time.Second)
			continue
		}
		session, stop := context.WithCancel(ctx)
		var wg sync.WaitGroup
		for _, group := range groups(followed) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				b.connection(session, group)
			}()
		}
		// Restart when the flag goes off or the followed symbols change.
		for session.Err() == nil {
			sleep(session, b.remap)
			if session.Err() != nil {
				break
			}
			if !b.flags.Enabled(flags.KeyReferenceFeed, flags.Subject{}) || !sameFollowed(b.follow(session), followed) {
				b.log.InfoContext(ctx, "reference books: followed symbols changed")
				stop()
			}
		}
		stop()
		wg.Wait()
	}
	return nil
}

// follow reads the followed symbols and makes sure each has a book.
func (b *Books) follow(ctx context.Context) map[string]ports.Reference {
	m := b.refs.Get(ctx)
	b.mu.Lock()
	defer b.mu.Unlock()
	for symbol, ref := range m {
		if st, ok := b.books[symbol]; !ok || st.ref.Remote != ref.Remote || !st.ref.Multiplier.Equal(ref.Multiplier) {
			b.books[symbol] = &bookState{ref: ref, futures: isContract(symbol), local: domain.NewLocalBook(isContract(symbol))}
		}
	}
	for symbol := range b.books {
		if _, ok := m[symbol]; !ok {
			delete(b.books, symbol)
		}
	}
	return m
}

func sameFollowed(a, b map[string]ports.Reference) bool {
	if len(a) != len(b) {
		return false
	}
	for s, r := range a {
		if o, ok := b[s]; !ok || o.Remote != r.Remote || !o.Multiplier.Equal(r.Multiplier) {
			return false
		}
	}
	return true
}

// bookGroup is the symbols of one stream connection.
type bookGroup struct {
	futures bool
	refs    []ports.Reference
}

// groups splits the followed symbols by market into connections of at
// most bookStreamsPerConn symbols, in symbol order.
func groups(m map[string]ports.Reference) []bookGroup {
	var spot, futures []ports.Reference
	for symbol, ref := range m {
		ref.Symbol = symbol // a contract follows its index pair's reference
		if isContract(symbol) {
			futures = append(futures, ref)
		} else {
			spot = append(spot, ref)
		}
	}
	var out []bookGroup
	for _, set := range []struct {
		futures bool
		refs    []ports.Reference
	}{{false, spot}, {true, futures}} {
		slices.SortFunc(set.refs, func(x, y ports.Reference) int { return strings.Compare(x.Symbol, y.Symbol) })
		for chunk := range slices.Chunk(set.refs, bookStreamsPerConn) {
			out = append(out, bookGroup{futures: set.futures, refs: chunk})
		}
	}
	return out
}

// connection keeps one stream connection up until ctx ends, with backoff
// after failures.
func (b *Books) connection(ctx context.Context, g bookGroup) {
	backoff := time.Second
	for ctx.Err() == nil {
		started := b.now()
		err := b.session(ctx, g)
		if ctx.Err() != nil {
			return
		}
		b.failures.Inc()
		b.log.WarnContext(ctx, "reference book stream failed", "futures", g.futures, "symbols", len(g.refs), "error", err)
		if b.now().Sub(started) > time.Minute {
			backoff = time.Second
		}
		sleep(ctx, backoff)
		backoff = min(2*backoff, time.Minute)
	}
}

// session streams a group until the connection ends: the books start
// over, buffer updates, and load their snapshots one by one (and again
// after a gap) alongside.
func (b *Books) session(ctx context.Context, g bookGroup) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	live := new(time.Time) // the connection's latest message, under b.mu
	b.mu.Lock()
	for _, ref := range g.refs {
		if st, ok := b.books[ref.Symbol]; ok {
			st.local.Reset()
			st.live = live
		}
	}
	b.mu.Unlock()
	// Symbols waiting for a snapshot: all of them first, then any that
	// lose their place.
	resync := make(chan string, len(g.refs)*2)
	for _, ref := range g.refs {
		resync <- ref.Symbol
	}
	byName := map[string]ports.Reference{}
	for _, ref := range g.refs {
		byName[ref.Symbol] = ref
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.loadRecent(ctx, g)
		b.loader(ctx, g.futures, byName, resync)
	}()
	err := b.src.BookStream(ctx, g.refs, g.futures, ports.BookHandlers{
		Depth: func(symbol string, d domain.DepthDiff) {
			b.mu.Lock()
			*live = b.now()
			st, ok := b.books[symbol]
			if !ok {
				b.mu.Unlock()
				return
			}
			err := st.local.Update(d)
			if st.local.Synced() {
				st.updated, st.changed = b.now(), true
			}
			b.mu.Unlock()
			if errors.Is(err, domain.ErrBookGap) {
				b.resyncs.Inc()
				select {
				case resync <- symbol:
				default:
				}
			}
		},
		Trade: func(t domain.Trade) {
			b.mu.Lock()
			*live = b.now()
			if st, ok := b.books[t.Symbol]; ok {
				st.recent = append(st.recent, t)
				if len(st.recent) > bookRecent {
					st.recent = slices.Delete(st.recent, 0, len(st.recent)-bookRecent)
				}
				st.pending = append(st.pending, t)
			}
			b.mu.Unlock()
		},
	})
	cancel()
	wg.Wait()
	return err
}

// loader loads the snapshots of the symbols sent on resync, one at a time
// (the source spaces its requests), until ctx ends.
func (b *Books) loader(ctx context.Context, futures bool, refs map[string]ports.Reference, resync chan string) {
	// Let the stream buffer a few updates first: a snapshot must not be
	// older than the first update (spot).
	sleep(ctx, time.Second)
	for {
		var symbol string
		select {
		case <-ctx.Done():
			return
		case symbol = <-resync:
		}
		ref, ok := refs[symbol]
		if !ok {
			continue
		}
		for attempt := 0; ctx.Err() == nil; attempt++ {
			lastID, bids, asks, err := b.src.DepthSnapshot(ctx, ref, futures)
			if err == nil {
				b.mu.Lock()
				if st, ok := b.books[symbol]; ok {
					err = st.local.Load(lastID, bids, asks)
					if err == nil {
						st.updated, st.changed = b.now(), true
					}
				}
				b.mu.Unlock()
			}
			if err == nil {
				break
			}
			if ctx.Err() == nil {
				b.log.WarnContext(ctx, "reference book snapshot not loaded", "symbol", symbol, "attempt", attempt, "error", err)
			}
			sleep(ctx, time.Duration(min(attempt+1, 10))*time.Second)
		}
	}
}

// loadRecent fills the recent trades of a group's symbols (for REST).
func (b *Books) loadRecent(ctx context.Context, g bookGroup) {
	for _, ref := range g.refs {
		trades, err := b.src.RecentTrades(ctx, ref, g.futures, bookRecent)
		if err != nil {
			if ctx.Err() == nil {
				b.log.WarnContext(ctx, "reference trades not loaded", "symbol", ref.Symbol, "error", err)
			}
			continue
		}
		b.mu.Lock()
		if st, ok := b.books[ref.Symbol]; ok && len(st.recent) == 0 {
			st.recent = trades
		}
		b.mu.Unlock()
	}
}

// drop forgets every book, when the reference feed goes off.
func (b *Books) drop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, st := range b.books {
		st.local.Reset()
		st.recent, st.pending, st.shown = nil, nil, false
	}
}

// usable reports whether st may be shown or used: in sync, and its
// connection heard from recently.
func (b *Books) usable(st *bookState) bool {
	return st.local.Synced() && st.live != nil && b.now().Sub(*st.live) < BookStale
}

// Shown reports whether symbol's public book and trades are the reference
// market's now.
func (b *Books) Shown(symbol string) bool {
	if !b.shows(symbol) {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.books[symbol]
	return ok && b.usable(st)
}

// Depth returns the top limit levels a side of symbol's reference book;
// false when it is not shown.
func (b *Books) Depth(symbol string, limit int) (*marketv1.DepthSnapshot, bool) {
	if !b.shows(symbol) {
		return nil, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.books[symbol]
	if !ok || !b.usable(st) {
		return nil, false
	}
	bids, asks := st.local.Top(limit)
	return &marketv1.DepthSnapshot{
		Symbol: symbol, Sequence: b.seq[symbol], Bids: protoLevels(bids), Asks: protoLevels(asks),
		TakenAt: timestamppb.New(st.updated), Reference: true,
	}, true
}

// Levels returns symbol's reference book (limit levels a side) while it is
// usable, whether or not it is shown: contract prices use it (Marks).
func (b *Books) Levels(symbol string, limit int) (bids, asks []domain.Level, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, found := b.books[symbol]
	if !found || !b.usable(st) {
		return nil, nil, false
	}
	bids, asks = st.local.Top(limit)
	return bids, asks, true
}

// Trades returns up to limit of symbol's recent reference trades, newest
// first; false when they are not shown.
func (b *Books) Trades(symbol string, limit int) ([]domain.Trade, bool) {
	if !b.shows(symbol) {
		return nil, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.books[symbol]
	if !ok || !b.usable(st) {
		return nil, false
	}
	out := make([]domain.Trade, 0, min(limit, len(st.recent)))
	for i := len(st.recent) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, st.recent[i])
	}
	return out, true
}

// Push publishes, every bookPush until ctx ends (an app.Loop body), the
// shown books that changed (a DepthUpdate, or a DepthSnapshot every
// bookSnapshotEvery and when a book is newly shown) and their new trades.
func (b *Books) Push(ctx context.Context) error {
	tick := time.NewTicker(bookPush)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		if err := b.publish(ctx, b.collect()); err != nil && ctx.Err() == nil {
			b.log.WarnContext(ctx, "public books not published", "error", err)
		}
	}
}

// outMsg is a public message to publish.
type outMsg struct {
	topic, symbol, kind, source string
	msg                         proto.Message
}

// collect works out the messages of one push.
func (b *Books) collect() []outMsg {
	now := b.now()
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []outMsg
	for symbol, st := range b.books {
		show := b.shows(symbol) && b.usable(st)
		if !show {
			st.shown, st.pending = false, nil
			continue
		}
		topic := event.TopicMarketDepth
		if st.futures {
			topic = event.TopicDerivMarketDepth
		}
		if !st.shown || now.Sub(st.sentAt) >= bookSnapshotEvery {
			bids, asks := st.local.Top(PublicDepth)
			_, seq := b.nextSeq(symbol)
			st.sent = [2][][2]string{rows(bids), rows(asks)}
			st.shown, st.sentAt, st.beatAt, st.changed = true, now, now, false
			out = append(out, outMsg{topic, symbol, "snapshot", "reference", &marketv1.DepthSnapshot{
				Symbol: symbol, Sequence: seq, Bids: protoLevels(bids), Asks: protoLevels(asks), TakenAt: timestamppb.New(*st.live),
				Reference: true,
			}})
		} else {
			var db, da [][2]string
			if st.changed {
				st.changed = false
				bids, asks := st.local.Top(PublicDepth)
				next := [2][][2]string{rows(bids), rows(asks)}
				db, da = diffRows(st.sent[0], next[0]), diffRows(st.sent[1], next[1])
				st.sent = next
			}
			// A change, or a heartbeat: taken_at is when the connection
			// last heard anything, so a quiet book still looks live.
			if len(db)+len(da) > 0 || now.Sub(st.beatAt) >= bookHeartbeat {
				prev, seq := b.nextSeq(symbol)
				st.beatAt = now
				out = append(out, outMsg{topic, symbol, "update", "reference", &marketv1.DepthUpdate{
					Symbol: symbol, Sequence: seq, PrevSequence: prev, Bids: rowLevels(db), Asks: rowLevels(da),
					TakenAt: timestamppb.New(*st.live), Reference: true,
				}})
			}
		}
		if len(st.pending) > 0 {
			out = append(out, outMsg{event.TopicMarketTrades, symbol, "trades", "reference", tradesPrinted(symbol, st.pending, true)})
			st.pending = nil
		}
	}
	return out
}

// RelayDepth republishes an engine's depth snapshot (from its internal
// topic) as the public book of a symbol that does not show the reference
// market's, with the symbol's public sequence. Derived state: a failure
// logs, the next snapshot supersedes it.
func (b *Books) RelayDepth(ctx context.Context, d *marketv1.DepthSnapshot) {
	symbol := d.GetSymbol()
	if b.Shown(symbol) {
		return
	}
	topic := event.TopicMarketDepth
	if isContract(symbol) {
		topic = event.TopicDerivMarketDepth
	}
	b.mu.Lock()
	_, seq := b.nextSeq(symbol)
	b.mu.Unlock()
	out := &marketv1.DepthSnapshot{Symbol: symbol, Sequence: seq, Bids: d.GetBids(), Asks: d.GetAsks(), TakenAt: d.GetTakenAt()}
	if err := b.publish(ctx, []outMsg{{topic, symbol, "snapshot", "platform", out}}); err != nil && ctx.Err() == nil {
		b.log.WarnContext(ctx, "platform book not relayed", "symbol", symbol, "error", err)
	}
}

// RelayTrades republishes the platform's trades of the symbols that do not
// show the reference market's; a failure logs (the trades stay stored).
func (b *Books) RelayTrades(ctx context.Context, trades []domain.Trade) {
	bySymbol := map[string][]domain.Trade{}
	var order []string
	for _, t := range trades {
		if _, ok := bySymbol[t.Symbol]; !ok {
			order = append(order, t.Symbol)
		}
		bySymbol[t.Symbol] = append(bySymbol[t.Symbol], t)
	}
	var out []outMsg
	for _, symbol := range order {
		if b.Shown(symbol) {
			continue
		}
		out = append(out, outMsg{event.TopicMarketTrades, symbol, "trades", "platform", tradesPrinted(symbol, bySymbol[symbol], false)})
	}
	if err := b.publish(ctx, out); err != nil && ctx.Err() == nil {
		b.log.WarnContext(ctx, "platform trades not relayed", "trades", len(trades), "error", err)
	}
}

// publish sends messages straight to Kafka, keyed by symbol: derived
// state, the next push supersedes a lost one.
func (b *Books) publish(ctx context.Context, msgs []outMsg) error {
	if len(msgs) == 0 {
		return nil
	}
	recs := make([]kafka.Record, 0, len(msgs))
	for _, m := range msgs {
		env, err := b.events.New(ctx, m.msg, "symbol", m.symbol)
		if err != nil {
			return err
		}
		raw, err := proto.Marshal(env)
		if err != nil {
			return fmt.Errorf("public %s %s: %w", m.kind, m.symbol, err)
		}
		recs = append(recs, kafka.Record{Topic: m.topic, Key: m.symbol, EventType: env.GetEventType(), Envelope: raw})
	}
	if err := b.pub.Publish(ctx, recs...); err != nil {
		return err
	}
	for _, m := range msgs {
		b.published.WithLabelValues(m.kind, m.source).Inc()
	}
	return nil
}

func tradesPrinted(symbol string, trades []domain.Trade, reference bool) *marketv1.TradesPrinted {
	out := &marketv1.TradesPrinted{Symbol: symbol, Reference: reference, Trades: make([]*marketv1.PublicTrade, 0, len(trades))}
	for _, t := range trades {
		side := orderv1.Side_SIDE_BUY
		if t.TakerSide == "SELL" {
			side = orderv1.Side_SIDE_SELL
		}
		out.Trades = append(out.Trades, &marketv1.PublicTrade{
			TradeId: t.ID, TradeNumber: t.Number, Price: t.Price.String(), Quantity: t.Quantity.String(),
			QuoteQuantity: t.Quote.String(), TakerSide: side, ExecutedAt: timestamppb.New(t.At),
		})
	}
	return out
}

func rows(levels []domain.Level) [][2]string {
	out := make([][2]string, len(levels))
	for i, l := range levels {
		out[i] = [2]string{l.Price.String(), l.Quantity.String()}
	}
	return out
}

func protoLevels(levels []domain.Level) []*marketv1.PriceLevel {
	out := make([]*marketv1.PriceLevel, len(levels))
	for i, l := range levels {
		out[i] = &marketv1.PriceLevel{Price: l.Price.String(), Quantity: l.Quantity.String()}
	}
	return out
}

func rowLevels(rs [][2]string) []*marketv1.PriceLevel {
	out := make([]*marketv1.PriceLevel, len(rs))
	for i, r := range rs {
		out[i] = &marketv1.PriceLevel{Price: r[0], Quantity: r[1]}
	}
	return out
}

// diffRows returns the levels of next that differ from prev, and the
// prices of prev missing from next at quantity "0".
func diffRows(prev, next [][2]string) [][2]string {
	old := make(map[string]string, len(prev))
	for _, l := range prev {
		old[l[0]] = l[1]
	}
	var out [][2]string
	seen := make(map[string]bool, len(next))
	for _, l := range next {
		seen[l[0]] = true
		if old[l[0]] != l[1] {
			out = append(out, l)
		}
	}
	for _, l := range prev {
		if !seen[l[0]] {
			out = append(out, [2]string{l[0], "0"})
		}
	}
	return out
}
