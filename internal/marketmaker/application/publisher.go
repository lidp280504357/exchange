// Package application publishes HOUSE's virtual liquidity (ADR-0015). It
// keeps the reference market's public books (the market.depth and
// derivatives.market.depth messages marked reference) and, every
// Interval, sends each pair's and contract's ReferenceBookUpdate to the
// engines' reference topics: HOUSE's levels and the rooms its inventory
// and caps leave, or an empty book where HOUSE must not trade now.
package application

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	orderv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/order/v1"
	"github.com/lidp280504357/exchange/internal/marketmaker/domain"
	"github.com/lidp280504357/exchange/internal/marketmaker/ports"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/flags"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
)

// Config are the publisher's settings.
type Config struct {
	// HouseUser is HOUSE's user ID on the trades (HOUSE_USER_ID).
	HouseUser string
	Caps      domain.Caps
	// Levels is how many levels a side HOUSE offers.
	Levels int
	// Interval is how often books go out when they changed, Heartbeat how
	// often an unchanged one does, Stale how long a reference book may go
	// without a message before HOUSE stops offering on it.
	Interval  time.Duration
	Heartbeat time.Duration
	Stale     time.Duration
}

// DefaultConfig follows the design (§8.6): levels up to 20,000 USDT, a
// symbol's position up to 100,000, all spot positions up to 1,000,000,
// 1,000 of backed inventory kept back, all contract positions up to 10
// times HOUSE's contract equity; the best 20 levels, every 250 ms at most,
// every 2 s at least.
func DefaultConfig() Config {
	return Config{
		Caps: domain.Caps{
			Level: decimal.NewFromInt(20000), Symbol: decimal.NewFromInt(100000), Total: decimal.NewFromInt(1000000),
			Contract: decimal.NewFromInt(100000), Safety: decimal.NewFromInt(1000), ContractLeverage: decimal.NewFromInt(10),
		},
		Levels:   20,
		Interval: 250 * time.Millisecond, Heartbeat: 2 * time.Second, Stale: 3 * time.Second,
	}
}

// Refresh intervals of what the publisher reads.
const (
	specsEvery = 30 * time.Second
	houseEvery = time.Second
	// houseStale is how old HOUSE's holdings may be before it stops
	// offering (the rooms would be guesses).
	houseStale = 10 * time.Second
)

// Publisher sends HOUSE's reference books.
type Publisher struct {
	cfg    Config
	specs  ports.Specs
	house  ports.House
	flags  ports.Flags
	pub    kafka.Publisher
	events *event.Factory
	log    *slog.Logger
	now    func() time.Time

	mu     sync.Mutex
	books  map[string]*refBook
	list   []domain.Spec
	listAt time.Time
	// The backed assets as last read, and when (zero: never).
	backedList []string
	backedAt   time.Time
	holdings   domain.Holdings
	contracts  domain.ContractAccount
	houseAt    time.Time
	sent       map[string]sent

	inventory *prometheus.GaugeVec
	exposure  *prometheus.GaugeVec
	room      *prometheus.GaugeVec
	active    *prometheus.GaugeVec
	updates   *prometheus.CounterVec
	failures  prometheus.Counter
	// HOUSE's contract equity, what its contract positions are worth and
	// how many times the equity they may be worth (ContractLeverage).
	equity, worth, leverage prometheus.Gauge
}

// refBook is a reference market's book as the public messages give it.
type refBook struct {
	seq        int64
	bids, asks []domain.Level
	// heard is when the last message arrived, taken the book's time.
	heard, taken time.Time
	gap          bool // an update was missed: wait for a snapshot
}

type sent struct {
	key string
	at  time.Time
}

// New returns a publisher; register its metrics with reg.
func New(cfg Config, specs ports.Specs, house ports.House, fl ports.Flags, pub kafka.Publisher, events *event.Factory, log *slog.Logger,
	reg prometheus.Registerer,
) *Publisher {
	p := &Publisher{
		cfg: cfg, specs: specs, house: house, flags: fl, pub: pub, events: events, log: log, now: time.Now,
		books: map[string]*refBook{}, contracts: domain.ContractAccount{Positions: map[string]decimal.Decimal{}}, sent: map[string]sent{},
		inventory: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "market_house_inventory", Help: "HOUSE's spot holding of an asset (MARKET_MAKER available; below zero for an internal asset it sold).",
		}, []string{"asset", "backed"}),
		exposure: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "market_house_exposure_usdt", Help: "USDT value of HOUSE's position on a pair's base asset or a contract (short below zero).",
		}, []string{"symbol"}),
		room: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "market_house_room", Help: "Base quantity HOUSE may still buy or sell on a symbol, as last published.",
		}, []string{"symbol", "side"}),
		active: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "market_house_active", Help: "1 while HOUSE offers liquidity on a symbol, 0 while its book is empty.",
		}, []string{"symbol"}),
		updates: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_house_updates_total", Help: "Reference books published, with levels or empty.",
		}, []string{"kind"}),
		failures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "market_house_publish_failures_total", Help: "Rounds of reference books that could not be published.",
		}),
		equity: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "market_house_contract_equity_usdt", Help: "HOUSE's contract equity: its FUTURES margin balance at the mark prices.",
		}),
		worth: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "market_house_contract_exposure_usdt", Help: "What HOUSE's contract positions are worth together at the mark prices.",
		}),
		leverage: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "market_house_contract_max_leverage", Help: "How many times its contract equity HOUSE's contract positions may be worth (HOUSE_CONTRACT_LEVERAGE).",
		}),
	}
	p.leverage.Set(cfg.Caps.ContractLeverage.InexactFloat64())
	// Not a number until HOUSE's contract account is read: 0 would say its
	// equity is gone (HouseContractEquityGone) while derivatives-service
	// was only not reached yet.
	p.equity.Set(math.NaN())
	p.worth.Set(math.NaN())
	reg.MustRegister(p.inventory, p.exposure, p.room, p.active, p.updates, p.failures, p.equity, p.worth, p.leverage)
	return p
}

// OnSnapshot takes a public book snapshot: a reference market's book
// replaces what the symbol had; the platform's own means the symbol does
// not show the reference market, and HOUSE offers nothing there.
func (p *Publisher) OnSnapshot(d *marketv1.DepthSnapshot) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !d.GetReference() {
		delete(p.books, d.GetSymbol())
		return
	}
	b := p.books[d.GetSymbol()]
	if b != nil && d.GetSequence() < b.seq {
		return
	}
	p.books[d.GetSymbol()] = &refBook{
		seq: d.GetSequence(), bids: levelsOf(d.GetBids()), asks: levelsOf(d.GetAsks()), heard: p.now(), taken: d.GetTakenAt().AsTime(),
	}
}

// OnUpdate applies a public book update that follows the message taken
// last; one that does not leaves the book unusable until a snapshot.
func (p *Publisher) OnUpdate(u *marketv1.DepthUpdate) {
	p.mu.Lock()
	defer p.mu.Unlock()
	b := p.books[u.GetSymbol()]
	switch {
	case !u.GetReference():
		delete(p.books, u.GetSymbol())
		return
	case b == nil:
		return
	case u.GetPrevSequence() != b.seq:
		b.gap = true
		return
	}
	b.seq, b.heard, b.taken = u.GetSequence(), p.now(), u.GetTakenAt().AsTime()
	b.bids = apply(b.bids, levelsOf(u.GetBids()), true)
	b.asks = apply(b.asks, levelsOf(u.GetAsks()), false)
}

// Run publishes until ctx ends (an app.Loop body).
func (p *Publisher) Run(ctx context.Context) error {
	tick := time.NewTicker(p.cfg.Interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		p.refresh(ctx)
		if err := p.publish(ctx, p.round()); err != nil && ctx.Err() == nil {
			p.failures.Inc()
			p.log.WarnContext(ctx, "house liquidity not published", "error", err)
		}
	}
}

// refresh reads the specs and HOUSE's holdings when they are due; a
// failure keeps what was read last.
func (p *Publisher) refresh(ctx context.Context) {
	p.mu.Lock()
	specsDue, houseDue := p.now().Sub(p.listAt) >= specsEvery, p.now().Sub(p.houseAt) >= houseEvery
	p.mu.Unlock()
	if specsDue {
		list, err := p.specs.Specs(ctx)
		if err != nil {
			p.log.WarnContext(ctx, "house liquidity: pairs and contracts not read", "error", err)
		} else {
			p.mu.Lock()
			p.list, p.listAt = list, p.now()
			p.mu.Unlock()
		}
		if backed, err := p.specs.Backed(ctx); err != nil {
			p.log.WarnContext(ctx, "house liquidity: the backed assets not read", "error", err)
		} else {
			p.mu.Lock()
			if !slices.Equal(backed, p.backedList) {
				p.log.InfoContext(ctx, "house liquidity: the backed assets", "assets", backed)
			}
			p.backedList, p.backedAt = backed, p.now()
			p.mu.Unlock()
		}
	}
	if houseDue {
		// The holdings count what settled before the read began; the
		// engine takes later fills off the rooms (holdings_at).
		readAt := p.now()
		holdings, err1 := p.house.Holdings(ctx)
		contracts, err2 := p.house.Contracts(ctx)
		if err1 != nil || err2 != nil {
			p.log.WarnContext(ctx, "house liquidity: HOUSE's holdings not read", "holdings", err1, "contracts", err2)
			return
		}
		p.mu.Lock()
		p.holdings, p.contracts, p.houseAt = holdings, contracts, readAt
		backed := make(map[string]bool, len(holdings))
		for asset := range holdings {
			backed[asset] = p.backed(asset)
		}
		p.mu.Unlock()
		for asset, amount := range holdings {
			// An asset counts as backed until the backed assets are read:
			// its series under the other label goes.
			p.inventory.DeleteLabelValues(asset, fmt.Sprint(!backed[asset]))
			p.inventory.WithLabelValues(asset, fmt.Sprint(backed[asset])).Set(amount.InexactFloat64())
		}
		p.equity.Set(contracts.Equity.InexactFloat64())
		p.worth.Set(contracts.Exposure.InexactFloat64())
	}
}

// backed reports whether HOUSE must hold asset to sell it: every asset
// must until the backed assets are read (ports.Specs.Backed).
func (p *Publisher) backed(asset string) bool {
	return p.backedAt.IsZero() || slices.Contains(p.backedList, asset)
}

// outgoing is a reference book to publish.
type outgoing struct {
	contract bool
	msg      *orderv1.ReferenceBookUpdate
	empty    bool
}

// round works out the books to publish now: each symbol's when it changed
// or its heartbeat is due, an empty one once when HOUSE stops offering.
func (p *Publisher) round() []outgoing {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	feed := p.flags.Enabled(flags.KeyReferenceFeed, flags.Subject{})
	houseFresh := !p.houseAt.IsZero() && now.Sub(p.houseAt) < houseStale
	prices := p.prices(now)
	usable := make([]bool, len(p.list))
	offered := 0 // the contracts HOUSE offers
	for i, spec := range p.list {
		b := p.books[spec.Symbol]
		_, priced := domain.LevelCap(spec, p.cfg.Caps, prices)
		usable[i] = feed && houseFresh && b != nil && !b.gap && now.Sub(b.heard) < p.cfg.Stale && priced &&
			p.flags.Enabled(flags.KeyHouseLiquidity, flags.Subject{Symbol: spec.Symbol})
		if usable[i] && spec.Contract {
			offered++
		}
	}
	// The contracts HOUSE offers share the room its positions may still
	// grow by in equal parts: between two reads of its positions, fills on
	// all of them cannot together take more than the room.
	contractRoom := domain.ContractRoom(p.contracts, p.cfg.Caps)
	if offered > 1 {
		contractRoom = contractRoom.Div(decimal.NewFromInt(int64(offered)))
	}
	var out []outgoing
	for i, spec := range p.list {
		b := p.books[spec.Symbol]
		msg := &orderv1.ReferenceBookUpdate{Symbol: spec.Symbol, HouseUserId: p.cfg.HouseUser}
		levelCap, _ := domain.LevelCap(spec, p.cfg.Caps, prices)
		if usable[i] {
			bids := domain.Levels(b.bids, true, spec, levelCap, p.cfg.Levels)
			asks := domain.Levels(b.asks, false, spec, levelCap, p.cfg.Levels)
			var buy, sell decimal.Decimal
			if spec.Contract {
				pos, mid := p.contracts.Positions[spec.Symbol], midOf(b)
				buy, sell = domain.ContractRooms(spec, pos, mid, contractRoom, p.cfg.Caps)
				p.exposure.WithLabelValues(spec.Symbol).Set(pos.Mul(mid).InexactFloat64())
			} else {
				buy, sell = domain.SpotRooms(spec, p.holdings, prices, p.backed, p.cfg.Caps)
				p.exposure.WithLabelValues(spec.Symbol).Set(p.holdings[spec.Base].Mul(prices[spec.Base]).InexactFloat64())
			}
			msg.Bids, msg.Asks = refLevels(bids), refLevels(asks)
			msg.BuyRoom, msg.SellRoom = buy.String(), sell.String()
			msg.SourceTime, msg.HoldingsAt = timestamppb.New(b.taken), timestamppb.New(p.houseAt)
			p.room.WithLabelValues(spec.Symbol, "buy").Set(buy.InexactFloat64())
			p.room.WithLabelValues(spec.Symbol, "sell").Set(sell.InexactFloat64())
		}
		empty := len(msg.GetBids())+len(msg.GetAsks()) == 0
		key := contentKey(msg)
		last, wasSent := p.sent[spec.Symbol]
		switch {
		case empty && (!wasSent || last.key == key):
			continue // nothing offered, and the engine knows it
		case key == last.key && now.Sub(last.at) < p.cfg.Heartbeat:
			continue
		}
		p.sent[spec.Symbol] = sent{key: key, at: now}
		active := 1.0
		if empty {
			active = 0
		}
		p.active.WithLabelValues(spec.Symbol).Set(active)
		out = append(out, outgoing{contract: spec.Contract, msg: msg, empty: empty})
	}
	return out
}

// prices is the USDT value of one unit of each asset: the mid of its
// USDT pair's reference book; USDT itself is 1.
func (p *Publisher) prices(now time.Time) map[string]decimal.Decimal {
	out := map[string]decimal.Decimal{"USDT": decimal.NewFromInt(1)}
	for _, spec := range p.list {
		if spec.Contract || spec.Quote != "USDT" {
			continue
		}
		if b := p.books[spec.Symbol]; b != nil && !b.gap && now.Sub(b.heard) < p.cfg.Stale {
			if m := midOf(b); m.IsPositive() {
				out[spec.Base] = m
			}
		}
	}
	return out
}

// publish sends the books straight to the engines' reference topics: a
// lost one is replaced by the next.
func (p *Publisher) publish(ctx context.Context, books []outgoing) error {
	if len(books) == 0 {
		return nil
	}
	recs := make([]kafka.Record, 0, len(books))
	for _, o := range books {
		env, err := p.events.New(ctx, o.msg, "symbol", o.msg.GetSymbol())
		if err != nil {
			return err
		}
		raw, err := proto.Marshal(env)
		if err != nil {
			return err
		}
		topic := event.TopicOrderReferences
		if o.contract {
			topic = event.TopicDerivOrderReferences
		}
		recs = append(recs, kafka.Record{Topic: topic, Key: o.msg.GetSymbol(), EventType: env.GetEventType(), Envelope: raw})
	}
	if err := p.pub.Publish(ctx, recs...); err != nil {
		p.mu.Lock()
		for _, o := range books {
			delete(p.sent, o.msg.GetSymbol()) // send again next round
		}
		p.mu.Unlock()
		return err
	}
	for _, o := range books {
		kind := "levels"
		if o.empty {
			kind = "empty"
		}
		p.updates.WithLabelValues(kind).Inc()
	}
	return nil
}

// contentKey identifies what a book offers, whatever its time.
func contentKey(m *orderv1.ReferenceBookUpdate) string {
	var sb strings.Builder
	for _, side := range [][]*orderv1.ReferenceLevel{m.GetBids(), m.GetAsks()} {
		for _, l := range side {
			sb.WriteString(l.GetPrice())
			sb.WriteByte('x')
			sb.WriteString(l.GetQuantity())
			sb.WriteByte(' ')
		}
		sb.WriteByte('|')
	}
	sb.WriteString(m.GetBuyRoom())
	sb.WriteByte('/')
	sb.WriteString(m.GetSellRoom())
	return sb.String()
}

func midOf(b *refBook) decimal.Decimal {
	if len(b.bids) == 0 || len(b.asks) == 0 {
		return decimal.Zero
	}
	return b.bids[0].Price.Add(b.asks[0].Price).Div(decimal.NewFromInt(2))
}

func refLevels(in []domain.Level) []*orderv1.ReferenceLevel {
	out := make([]*orderv1.ReferenceLevel, len(in))
	for i, l := range in {
		out[i] = &orderv1.ReferenceLevel{Price: l.Price.String(), Quantity: l.Quantity.String()}
	}
	return out
}

// levelsOf reads price levels; one that does not parse is left out.
func levelsOf(in []*marketv1.PriceLevel) []domain.Level {
	out := make([]domain.Level, 0, len(in))
	for _, l := range in {
		p, err1 := decimal.NewFromString(l.GetPrice())
		q, err2 := decimal.NewFromString(l.GetQuantity())
		if err1 == nil && err2 == nil {
			out = append(out, domain.Level{Price: p, Quantity: q})
		}
	}
	return out
}

// apply applies changed levels (quantity zero removes one) to a side kept
// best first.
func apply(side, changes []domain.Level, bids bool) []domain.Level {
	for _, l := range changes {
		i, found := slices.BinarySearchFunc(side, l.Price, func(e domain.Level, target decimal.Decimal) int {
			c := e.Price.Cmp(target)
			if bids {
				return -c
			}
			return c
		})
		switch {
		case found && !l.Quantity.IsPositive():
			side = slices.Delete(side, i, i+1)
		case found:
			side[i].Quantity = l.Quantity
		case l.Quantity.IsPositive():
			side = slices.Insert(side, i, l)
		}
	}
	return side
}
