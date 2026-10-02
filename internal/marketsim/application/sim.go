// Package application runs the simulated market of the platform coin
// (ASTRA design §4): every Tick it advances the model's target price and
// lets the bots act on it — makers keep ladders of limit orders around the
// target, takers and trend followers trade at the market — within the
// throttle, and it saves the model's state so that a restart goes on from
// where it stopped. The bots trade through the platform's own paths, as
// users; the market data the sites show is what they print.
package application

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketsim/domain"
	"github.com/lidp280504357/exchange/internal/marketsim/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

// Config is the simulation's fixed setup.
type Config struct {
	// Symbol is the pair the bots trade (ASTRA-USDT); Quote its quote
	// asset, which the bots keep about BotUSDT of; Perp its perpetual
	// (empty: none).
	Symbol, Quote, Perp string
	// Tick is how often the model steps and the bots act (250 ms).
	Tick time.Duration
	// Seed seeds a first start's random source; 0 takes the clock.
	Seed uint64
}

// How often the chores run, and how many makers requote in a round at
// most (the rest wait their turn, which spreads the orders out).
const (
	pairEvery      = 30 * time.Second
	refsEvery      = time.Second
	saveEvery      = 5 * time.Second
	botsEvery      = time.Minute
	inventoryEvery = 10 * time.Minute
	trendEvery     = 30 * time.Second
	makersPerRound = 2
	// makerReserve is the share of the order rate the makers leave the
	// takers, trend followers and executors.
	makerReserve = 0.25
)

// The reference pairs the market factor follows.
const (
	btcPair = "BTC-USDT"
	ethPair = "ETH-USDT"
)

// Sim is the simulated market.
type Sim struct {
	// Derivatives trades the perpetual; nil leaves it alone.
	Derivatives ports.Derivatives

	cfg     Config
	trading ports.Trading
	prices  ports.Prices
	pairs   ports.Pairs
	store   ports.Store
	flags   ports.Flags
	log     *slog.Logger
	m       *metrics
	now     func() time.Time

	mu        sync.Mutex
	params    domain.Params
	version   int64
	model     *domain.Model
	bots      []*bot
	pair      domain.Pair
	pairAt    time.Time
	btc, eth  float64
	refsAt    time.Time
	last      decimal.Decimal // the pair's last trade
	history   []domain.Mark   // the target a minute apart, for the trend followers
	orders    domain.Bucket
	cancels   domain.Bucket
	running   bool
	round     time.Time
	savedAt   time.Time
	botsAt    time.Time
	checkedAt time.Time
	trendAt   time.Time
	turn      int
	guards    map[domain.Guard]int
	events    []*domain.Event // scheduled and running
	movedAt   time.Time       // when an event last moved the price
	executeAt time.Time       // the executors' next turn
	samples   []Sample

	// The price band (band.go): the anchor as read lately, the last
	// trade's time, where the makers quote and whether the quotes walk,
	// the watchdog's count.
	anchorReads  []domain.Mark
	anchorAt     time.Time
	lastTradeAt  time.Time
	reportedAt   time.Time
	center       float64
	walking      bool
	watchFrom    time.Time
	refusedSince time.Time
	deadlocks    int
	deadlockAt   time.Time

	perpPair      domain.Pair
	perpPairAt    time.Time
	perpMark      float64 // the contract's mark price as last read
	perpMarkAt    time.Time
	perpRunning   bool
	perpTurn      int
	perpBots      map[string]*perpBot
	perpCheckedAt time.Time
}

// bot is a bot account as the simulation runs it.
type bot struct {
	ports.Bot
	phase     int       // a maker's grid phase
	quotedP   float64   // the target when it last requoted
	nextQuote time.Time // when it requotes at the latest
	known     bool      // its balances were read
	usdt      decimal.Decimal
	coin      decimal.Decimal
	err       string
	errAt     time.Time
	// After a refused order the bot waits (domain.Backoff) until retryAt.
	wait    time.Duration
	retryAt time.Time
}

// ready reports whether the bot may place orders: not waiting after a
// refusal.
func (b *bot) ready(now time.Time) bool { return !now.Before(b.retryAt) }

// backOff makes the bot wait after a refusal; done ends the wait.
func (b *bot) backOff(now time.Time) {
	b.wait = domain.Backoff(b.wait)
	b.retryAt = now.Add(b.wait)
}

func (b *bot) done() { b.wait, b.retryAt = 0, time.Time{} }

// New returns a simulation; Start it before Run.
func New(cfg Config, trading ports.Trading, prices ports.Prices, pairs ports.Pairs, store ports.Store, fl ports.Flags, log *slog.Logger,
	reg prometheus.Registerer,
) *Sim {
	if cfg.Tick <= 0 {
		cfg.Tick = 250 * time.Millisecond
	}
	return &Sim{
		cfg: cfg, trading: trading, prices: prices, pairs: pairs, store: store, flags: fl, log: log, m: newMetrics(reg), now: time.Now,
		guards: map[domain.Guard]int{},
	}
}

// Start loads the settings (saving the defaults on a first start), the
// model's state and the bots.
func (s *Sim) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, version, ok, err := s.store.Settings(ctx)
	if err != nil {
		return err
	}
	if !ok {
		p = domain.DefaultParams()
		if version, err = s.store.SaveSettings(ctx, p, ports.ParamChange{At: s.now(), Actor: "market-sim"}, nil); err != nil {
			return err
		}
	}
	st, _, err := s.store.State(ctx)
	if err != nil {
		return err
	}
	seed := s.cfg.Seed
	if seed == 0 {
		seed = uint64(s.now().UnixNano()) //nolint:gosec // a seed, not a secret
	}
	s.model = domain.NewModel(p, st, seed)
	s.apply(p, version)
	if err := s.loadBots(ctx); err != nil {
		return err
	}
	open, err := s.store.Events(ctx, true, 0)
	if err != nil {
		return err
	}
	for i := range open {
		s.events = append(s.events, &open[i])
	}
	s.log.InfoContext(ctx, "simulated market loaded", "symbol", s.cfg.Symbol, "bots", len(s.bots), "settings_version", version,
		"target", st.P)
	return nil
}

// apply takes new settings: the model's and the throttle's.
func (s *Sim) apply(p domain.Params, version int64) {
	s.params, s.version = p, version
	s.model.Params = p
	s.orders = domain.Bucket{Rate: p.OrdersPerSecond, Burst: p.OrdersPerSecond}
	s.cancels = domain.Bucket{Rate: p.CancelsPerSecond, Burst: p.CancelsPerSecond}
}

// loadBots reads the bots, keeping what the running ones learned, and
// spreads the makers over the grid's phases.
func (s *Sim) loadBots(ctx context.Context) error {
	list, err := s.store.Bots(ctx)
	if err != nil {
		return err
	}
	byID := map[string]*bot{}
	for _, b := range s.bots {
		byID[b.UserID] = b
	}
	bots := make([]*bot, 0, len(list))
	for _, x := range list {
		b, ok := byID[x.UserID]
		if !ok {
			b = &bot{}
		}
		b.Bot = x
		bots = append(bots, b)
	}
	sort.Slice(bots, func(i, j int) bool { return bots[i].Label < bots[j].Label })
	makers := 0
	for _, b := range bots {
		if b.Role == domain.RoleMaker {
			makers++
		}
	}
	i := 0
	for _, b := range bots {
		if b.Role == domain.RoleMaker {
			b.phase = i * max(s.params.LevelTicks, 1) / max(makers, 1)
			i++
		}
	}
	s.bots = bots
	return nil
}

// Run steps the simulation every Tick until ctx ends, then saves its state.
func (s *Sim) Run(ctx context.Context) error {
	t := time.NewTicker(s.cfg.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			defer s.mu.Unlock()
			s.save(context.WithoutCancel(ctx))
			return nil
		case <-t.C:
		}
		s.Round(ctx)
	}
}

// Round advances the model one step and lets the bots act. With
// sim.enabled off, or the pair not trading, the bots' orders are canceled
// once and nothing else happens.
func (s *Sim) Round(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	dt := s.cfg.Tick
	if !s.round.IsZero() {
		dt = min(max(now.Sub(s.round), 0), 5*time.Second)
	}
	s.round = now
	if now.Sub(s.botsAt) >= botsEvery { // also while off: the operators see them
		s.botsAt = now
		if err := s.loadBots(ctx); err != nil {
			s.m.errors.WithLabelValues("bots").Inc()
		}
	}
	s.refreshPair(ctx, now)
	if !s.flags.Enabled(flags.KeySimEnabled, flags.Subject{Symbol: s.cfg.Symbol}) || !s.pair.Trading {
		if s.running {
			s.stop(ctx)
		}
		if s.perpRunning {
			s.stopPerp(ctx)
		}
		s.m.running.Set(0)
		return
	}
	if !s.running {
		s.running, s.watchFrom = true, now
		s.checkInventory(ctx, now) // the takers lean by what they hold
	}
	s.m.running.Set(1)
	s.refreshRefs(ctx, now)
	s.startDue(ctx, now)
	sh, ended := domain.ShapeOf(s.runningEvents(), now)
	p, guard := s.model.Step(now, s.btc, s.eth, sh)
	s.finish(ctx, now, ended, p)
	s.m.target.Set(p)
	if guard != domain.GuardNone {
		s.guards[guard]++
		s.m.guards.WithLabelValues(string(guard)).Inc()
	}
	if n := len(s.history); n == 0 || now.Sub(s.history[n-1].At) >= time.Minute {
		s.history = append(s.history, domain.Mark{At: now, P: p})
		if keep := s.params.TrendMinutes + 2; len(s.history) > keep {
			s.history = s.history[len(s.history)-keep:]
		}
	}
	s.sample(now, p)
	s.refreshAnchor(ctx, now)
	s.report(ctx, now, p)
	s.watch(ctx, now, sh)
	if sh.Halted {
		if s.perpRunning {
			s.stopPerp(ctx)
		}
		return // the halt canceled the makers' orders; the pair waits
	}
	// The quotes stand within the price band around its anchor; beyond
	// it they walk toward the target (band.go).
	center, walking := domain.QuoteCenter(p, s.anchors(), s.pair.Band)
	s.center, s.walking = center, walking
	s.m.walking.Set(map[bool]float64{false: 0, true: 1}[walking])
	s.quote(ctx, now, center)
	if !s.runs(domain.EventPause) { // a pause keeps the makers only
		s.take(ctx, now, center, dt)
		s.follow(ctx, now, center)
	}
	s.execute(ctx, now, center)
	s.perp(ctx, now, p, dt)
	s.chores(ctx, now)
}

func isFunds(err error) bool { return errors.Is(err, ports.ErrFunds) }

func (s *Sim) refreshPair(ctx context.Context, now time.Time) {
	if !s.pairAt.IsZero() && now.Sub(s.pairAt) < pairEvery {
		return
	}
	pair, err := s.trading.Pair(ctx, s.cfg.Symbol)
	if err != nil {
		s.m.errors.WithLabelValues("pair").Inc()
		s.log.WarnContext(ctx, "simulated market: the pair not read", "symbol", s.cfg.Symbol, "error", err)
		if s.pairAt.IsZero() {
			return // never read: not trading
		}
		pair = s.pair
	}
	s.pair, s.pairAt = pair, now
}

// refreshRefs reads BTC's and ETH's reference prices, 0 when not fresh
// (the market factor then holds).
func (s *Sim) refreshRefs(ctx context.Context, now time.Time) {
	if !s.refsAt.IsZero() && now.Sub(s.refsAt) < refsEvery {
		return
	}
	s.refsAt = now
	read := func(symbol string) float64 {
		p, fresh, err := s.prices.Reference(ctx, symbol)
		if err != nil {
			s.m.errors.WithLabelValues("reference").Inc()
			return 0
		}
		if !fresh {
			return 0
		}
		return p.InexactFloat64()
	}
	s.btc, s.eth = read(btcPair), read(ethPair)
	fresh := 0.0
	if s.btc > 0 && s.eth > 0 {
		fresh = 1
	}
	s.m.refsFresh.Set(fresh)
}

func (s *Sim) botsOf(role domain.Role) []*bot {
	var out []*bot
	for _, b := range s.bots {
		if b.Enabled && b.Role == role {
			out = append(out, b)
		}
	}
	return out
}

// quote lets the makers whose turn it is requote around center (the
// target within the price band): one whose last quote is RequoteTick
// ticks off it, or whose time is up (every one to three seconds), at most
// makersPerRound a round, in turn; a maker waiting after a refusal skips.
func (s *Sim) quote(ctx context.Context, now time.Time, center float64) {
	makers := s.botsOf(domain.RoleMaker)
	if len(makers) == 0 {
		return
	}
	tick := s.pair.Tick.InexactFloat64()
	done := 0
	for i := range makers {
		if done == makersPerRound {
			break
		}
		b := makers[(s.turn+i)%len(makers)]
		if !b.ready(now) {
			continue
		}
		moved := tick > 0 && math.Abs(center-b.quotedP)/tick >= float64(s.params.RequoteTick)
		if !moved && now.Before(b.nextQuote) {
			continue
		}
		s.requote(ctx, now, b, center)
		done++
	}
	s.turn = (s.turn + 1) % len(makers)
}

// requote brings a maker's orders to its ladder around center, within the
// price band: the orders no longer wanted canceled, the missing levels
// placed nearest first, bids and asks in turn, as far as the throttle
// lets (leaving the other roles their share). A side the bot cannot fund
// waits for the next requote; another refusal makes the bot wait.
func (s *Sim) requote(ctx context.Context, now time.Time, b *bot, center float64) {
	rng := s.model.Rand()
	b.nextQuote = now.Add(time.Second + time.Duration(rng.Int64N(int64(2*time.Second))))
	open, err := s.trading.Open(ctx, b.UserID, s.cfg.Symbol)
	if err != nil {
		s.fail(ctx, b, "open", err)
		return
	}
	bids, asks := domain.LadderPrices(center, b.phase, s.params, s.pair)
	a := s.anchors()
	bids, asks = domain.InBand(bids, a, s.pair.Band), domain.InBand(asks, a, s.pair.Band)
	cancel, placeBids, placeAsks := domain.Diff(open, bids, asks)
	for _, o := range cancel {
		if !s.cancels.Take(now) {
			s.m.throttled.WithLabelValues("cancel").Inc()
			break
		}
		if err := s.trading.Cancel(ctx, b.UserID, o.ID); err != nil {
			s.fail(ctx, b, "cancel", err)
			continue
		}
		s.m.cancels.WithLabelValues(string(b.Role)).Inc()
	}
	placed, outOfBand, stop := 0, 0, false
	unfunded := map[domain.Side]bool{}
	place := func(side domain.Side, price decimal.Decimal) {
		if stop || unfunded[side] {
			return
		}
		if !s.orders.TakeLeaving(now, s.params.OrdersPerSecond*makerReserve) {
			s.m.throttled.WithLabelValues("order").Inc()
			stop = true
			return
		}
		qty := domain.Quantity(domain.Worth(rng, s.params.LevelSize, 0.5), price, s.pair)
		_, err := s.trading.Limit(ctx, b.UserID, s.cfg.Symbol, side, price, qty)
		switch {
		case err == nil:
			placed++
		case isFunds(err):
			unfunded[side] = true // the side waits; the bot does not
		case errors.Is(err, ports.ErrOutOfBand):
			outOfBand++
			stop = true
		default:
			stop = true
		}
		s.placed(ctx, now, b, err, !isFunds(err))
	}
	for i := 0; i < max(len(placeBids), len(placeAsks)); i++ {
		if i < len(placeBids) {
			place(domain.Buy, placeBids[i])
		}
		if i < len(placeAsks) {
			place(domain.Sell, placeAsks[i])
		}
	}
	s.refused(now, len(placeBids)+len(placeAsks), placed, outOfBand)
	b.quotedP = center
}

// take sends the takers' market orders that arrived in dt, each by a taker
// not waiting after a refusal.
func (s *Sim) take(ctx context.Context, now time.Time, p float64, dt time.Duration) {
	takers := s.botsOf(domain.RoleTaker)
	if len(takers) == 0 {
		return
	}
	rng := s.model.Rand()
	for range domain.Arrivals(rng, s.params, dt, now) {
		b := pick(rng, takers, now)
		if b == nil {
			continue
		}
		lean := 0.0
		if b.known {
			lean = domain.Lean(b.usdt.InexactFloat64(), s.params.BotUSDT)
		}
		s.market(ctx, now, b, domain.TakerSide(rng, s.params, lean), domain.Worth(rng, s.params.OrderSize, 0.8), p)
	}
}

// follow lets the trend followers trade the target's direction over the
// window, every half minute or so, each with TrendStrength's chance.
func (s *Sim) follow(ctx context.Context, now time.Time, p float64) {
	if now.Before(s.trendAt) {
		return
	}
	rng := s.model.Rand()
	s.trendAt = now.Add(trendEvery/2 + time.Duration(rng.Int64N(int64(trendEvery))))
	side, ok := domain.TrendSide(s.history, now, time.Duration(s.params.TrendMinutes)*time.Minute)
	if !ok {
		return
	}
	for _, b := range s.botsOf(domain.RoleTrend) {
		if rng.Float64() < s.params.TrendStrength && b.ready(now) {
			s.market(ctx, now, b, side, domain.Worth(rng, s.params.OrderSize, 0.8), p)
		}
	}
}

// pick draws a bot of bots that is not waiting after a refusal, nil when
// a few draws found none.
func pick(rng *rand.Rand, bots []*bot, now time.Time) *bot {
	for range 3 {
		if b := bots[rng.IntN(len(bots))]; b.ready(now) {
			return b
		}
	}
	return nil
}

// market sends a market order worth about worth USDT: a buy spends it, a
// sell sells its worth at the target.
func (s *Sim) market(ctx context.Context, now time.Time, b *bot, side domain.Side, worth, p float64) {
	if !s.orders.Take(now) {
		s.m.throttled.WithLabelValues("order").Inc()
		return
	}
	var err error
	if side == domain.Buy {
		quote := decimal.Max(decimal.NewFromFloat(worth).Round(2), s.pair.MinNotional)
		err = s.trading.Market(ctx, b.UserID, s.cfg.Symbol, side, quote, decimal.Zero)
	} else {
		err = s.trading.Market(ctx, b.UserID, s.cfg.Symbol, side, decimal.Zero, domain.Quantity(worth, decimal.NewFromFloat(p), s.pair))
	}
	s.placed(ctx, now, b, err, true)
}

// placed counts an order's result. A refused one gives its token back
// (only orders that reach the book count against the throttle) and, with
// backOff, makes the bot wait.
func (s *Sim) placed(ctx context.Context, now time.Time, b *bot, err error, backOff bool) {
	result := "placed"
	switch {
	case err == nil:
		b.done()
	case isFunds(err):
		result = "unfunded"
	case errors.Is(err, ports.ErrOutOfBand):
		result = "out_of_band"
	default:
		result = "failed"
		s.fail(ctx, b, "order", err)
	}
	s.m.orders.WithLabelValues(string(b.Role), result).Inc()
	if err == nil {
		return
	}
	s.orders.Return()
	if backOff {
		b.backOff(now)
	}
}

func (s *Sim) fail(ctx context.Context, b *bot, op string, err error) {
	s.m.errors.WithLabelValues(op).Inc()
	b.err, b.errAt = op+": "+err.Error(), s.now()
	s.log.WarnContext(ctx, "simulated market: a bot's request failed", "bot", b.Label, "op", op, "error", err)
}

// stop cancels the makers' orders: the bots leave the book when the
// simulation is switched off or the pair stops trading.
func (s *Sim) stop(ctx context.Context) {
	for _, b := range s.botsOf(domain.RoleMaker) {
		if err := s.trading.CancelAll(ctx, b.UserID, s.cfg.Symbol); err != nil {
			s.fail(ctx, b, "cancel_all", err)
		}
		b.quotedP, b.nextQuote = 0, time.Time{}
	}
	s.running, s.watchFrom, s.walking = false, time.Time{}, false
	s.m.walking.Set(0)
	s.save(ctx)
	s.log.InfoContext(ctx, "simulated market stopped: the makers' orders are canceled", "symbol", s.cfg.Symbol)
}

func (s *Sim) chores(ctx context.Context, now time.Time) {
	if now.Sub(s.savedAt) >= saveEvery {
		s.save(ctx)
	}
	if now.Sub(s.checkedAt) >= inventoryEvery {
		s.checkInventory(ctx, now)
	}
}

func (s *Sim) save(ctx context.Context) {
	s.savedAt = s.now()
	if err := s.store.SaveState(ctx, s.model.Snapshot()); err != nil {
		s.m.errors.WithLabelValues("save").Inc()
		s.log.WarnContext(ctx, "simulated market: the state not saved", "error", err)
	}
}

// checkInventory reads every bot's balances: the takers lean by them, and
// the totals are watched.
func (s *Sim) checkInventory(ctx context.Context, now time.Time) {
	s.checkedAt = now
	base := baseOf(s.cfg.Symbol)
	var usdt, coin decimal.Decimal
	for _, b := range s.bots {
		bal, err := s.trading.Balances(ctx, b.UserID)
		if err != nil {
			s.fail(ctx, b, "balances", err)
			continue
		}
		b.usdt, b.coin, b.known = bal[s.cfg.Quote], bal[base], true
		usdt, coin = usdt.Add(b.usdt), coin.Add(b.coin)
	}
	s.m.inventory.WithLabelValues(s.cfg.Quote).Set(usdt.InexactFloat64())
	s.m.inventory.WithLabelValues(base).Set(coin.InexactFloat64())
}

// baseOf is a pair's base asset: ASTRA of ASTRA-USDT.
func baseOf(symbol string) string {
	for i := range symbol {
		if symbol[i] == '-' {
			return symbol[:i]
		}
	}
	return symbol
}

// Status is what the operators see of the simulation.
type Status struct {
	Symbol    string
	Enabled   bool
	Running   bool
	Target    float64
	Last      decimal.Decimal
	RefsFresh bool
	Params    domain.Params
	Version   int64
	Guards    map[domain.Guard]int
	Bots      []BotStatus
	Events    []domain.Event // scheduled and running
	Perp      string
	PerpOn    bool // the bots trade the perpetual
	// The price band: its anchor as last read (0: none), the band (a
	// share; 0: none), where the makers quote, whether the quotes walk
	// toward a target beyond the band, the pair's last trade, and how
	// often the watchdog found the market locked and when last.
	Anchor      float64
	Band        float64
	Center      float64
	Walking     bool
	LastTradeAt time.Time
	Deadlocks   int
	DeadlockAt  time.Time
	At          time.Time
}

// BotStatus is one bot as the operators see it.
type BotStatus struct {
	ports.Bot
	USDT, Coin decimal.Decimal
	Known      bool
	// Position and Futures are its signed position on the perpetual and
	// its available FUTURES balance, as last read.
	Position, Futures decimal.Decimal
	Error             string
	ErrorAt           time.Time
	// RetryAt is when the bot places orders again after a refusal (zero:
	// it does).
	RetryAt time.Time
}

// Status reports the simulation's state.
func (s *Sim) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{
		Symbol: s.cfg.Symbol, Enabled: s.flags.Enabled(flags.KeySimEnabled, flags.Subject{Symbol: s.cfg.Symbol}), Running: s.running,
		Last: s.last, RefsFresh: s.btc > 0 && s.eth > 0, Params: s.params, Version: s.version, Guards: map[domain.Guard]int{}, At: s.round,
	}
	if s.model != nil { // started
		st.Target = s.model.State.P
	}
	for g, n := range s.guards {
		st.Guards[g] = n
	}
	st.Perp, st.PerpOn = s.cfg.Perp, s.perpRunning
	st.Anchor, st.Band, st.Center, st.Walking = s.anchor(), s.pair.Band, s.center, s.walking
	st.LastTradeAt, st.Deadlocks, st.DeadlockAt = s.lastTradeAt, s.deadlocks, s.deadlockAt
	for _, b := range s.bots {
		bs := BotStatus{Bot: b.Bot, USDT: b.usdt, Coin: b.coin, Known: b.known, Error: b.err, ErrorAt: b.errAt}
		if s.round.Before(b.retryAt) {
			bs.RetryAt = b.retryAt
		}
		if pb, ok := s.perpBots[b.UserID]; ok {
			bs.Position, bs.Futures = pb.position, pb.futures
		}
		st.Bots = append(st.Bots, bs)
	}
	for _, e := range s.events {
		st.Events = append(st.Events, *e)
	}
	return st
}

// UpdateParams validates and stores new settings for actor, which take
// effect from the next round, and returns their version. A change takes
// from the operators' hourly budget like an event (§6.2): how far it
// moves the price (domain.ParamsMove) and the day's turnover
// (domain.VolumeMove); beyond one operator's share another one approves
// (approvedBy).
func (s *Sim) UpdateParams(ctx context.Context, p domain.Params, actor, approvedBy string) (int64, error) {
	if err := p.Validate(); err != nil {
		return 0, apperr.Invalid(err.Error())
	}
	if actor == "" {
		return 0, apperr.Invalid("the operator is required")
	}
	now := s.now()
	spent, err := s.budget(ctx, now)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	target := s.model.State.P
	change := ports.ParamChange{
		At: now, Actor: actor, ApprovedBy: approvedBy, Move: domain.ParamsMove(s.params, p, target), Volume: domain.VolumeMove(s.params, p),
	}
	if domain.NeedsApproval(domain.Spend{At: now, Move: change.Move, Volume: change.Volume}, spent.spends(target, s.params.Sigma)) &&
		(approvedBy == "" || approvedBy == actor) {
		return 0, ErrParamsNeedApproval.WithDetail("move", math.Round(change.Move*1e4)/1e4).
			WithDetail("volume", math.Round(change.Volume*1e4)/1e4)
	}
	details, _ := json.Marshal(map[string]any{"from": s.params, "to": p, "approved_by": approvedBy, "move": change.Move, "volume": change.Volume})
	version, err := s.store.SaveSettings(ctx, p, change, &ports.Audit{
		Action: "market.sim.params_changed", Target: "sim:" + s.cfg.Symbol, Actor: actor, Reason: "settings", Details: string(details),
	})
	if err != nil {
		return 0, err
	}
	s.apply(p, version)
	s.log.InfoContext(ctx, "simulated market settings changed", "by", actor, "approved_by", approvedBy, "version", version)
	return version, nil
}

// AddBot registers a user as a bot of role, which trades from the next
// round on.
func (s *Sim) AddBot(ctx context.Context, b ports.Bot) error {
	switch b.Role {
	case domain.RoleMaker, domain.RoleTaker, domain.RoleTrend, domain.RoleExecutor:
	default:
		return apperr.Invalid("role must be MAKER, TAKER, TREND or EXECUTOR")
	}
	if b.UserID == "" || b.Label == "" {
		return apperr.Invalid("the user and a label are required")
	}
	b.Enabled = true
	if err := s.store.AddBot(ctx, b); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadBots(ctx)
}

// metrics are the simulation's gauges and counters.
type metrics struct {
	target, last, running, refsFresh prometheus.Gauge
	walking                          prometheus.Gauge
	inventory                        *prometheus.GaugeVec
	orders                           *prometheus.CounterVec
	cancels, guards, throttled       *prometheus.CounterVec
	errors                           *prometheus.CounterVec
	deadlocks                        prometheus.Counter
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		target:    prometheus.NewGauge(prometheus.GaugeOpts{Name: "market_sim_target_price", Help: "The model's target price of the simulated pair."}),
		last:      prometheus.NewGauge(prometheus.GaugeOpts{Name: "market_sim_last_price", Help: "The simulated pair's last traded price."}),
		running:   prometheus.NewGauge(prometheus.GaugeOpts{Name: "market_sim_running", Help: "1 while the bots trade, 0 while switched off or the pair is not trading."}),
		refsFresh: prometheus.NewGauge(prometheus.GaugeOpts{Name: "market_sim_references_fresh", Help: "1 while BTC's and ETH's reference prices are fresh (the market factor follows them)."}),
		inventory: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "market_sim_inventory", Help: "What the bots hold together (available SPOT), by asset, at the last check.",
		}, []string{"asset"}),
		walking: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "market_sim_walking", Help: "1 while the target is beyond the price band and the quotes walk toward it.",
		}),
		orders: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_sim_orders_total", Help: "The bots' orders by role and result (placed, unfunded, out_of_band, failed).",
		}, []string{"role", "result"}),
		cancels: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_sim_cancels_total", Help: "The bots' cancels by role.",
		}, []string{"role"}),
		guards: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_sim_guards_total", Help: "Steps the guards bounded the target in (minute, floor, ceiling).",
		}, []string{"guard"}),
		throttled: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_sim_throttled_total", Help: "Orders and cancels the throttle held back.",
		}, []string{"kind"}),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "market_sim_errors_total", Help: "Failed requests to the platform's services, by operation.",
		}, []string{"op"}),
		deadlocks: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "market_sim_band_deadlocks_total",
			Help: "Times the watchdog found the market locked (three minutes without a trade, or every level refused for the price band) and rebased the model at the band's anchor.",
		}),
	}
	reg.MustRegister(m.target, m.last, m.running, m.refsFresh, m.walking, m.inventory, m.orders, m.cancels, m.guards, m.throttled, m.errors,
		m.deadlocks)
	return m
}
