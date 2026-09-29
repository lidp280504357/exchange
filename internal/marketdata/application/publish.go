package application

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	"github.com/lidp280504357/exchange/internal/marketdata/domain"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
)

// PushInterval is how often open candles and tickers are pushed (§11.8).
const PushInterval = 500 * time.Millisecond

// Update is a message for market.candle.events.
type Update struct {
	Symbol  string
	Message proto.Message
}

// Updates returns what changed since the last call, in this order per
// symbol: a candle whose interval ended (CandleClosed, once), the flat
// candle of an interval that has not traded yet (CandleUpdated, once), open
// candles that took trades (CandleUpdated), and the ticker if it changed.
func (s *Service) Updates(now time.Time) []Update {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Update
	for symbol, st := range s.symbols {
		for _, i := range domain.Intervals {
			c, ok := st.current[i]
			if !ok {
				continue
			}
			start := i.Start(now)
			if c.OpenTime.Before(start) {
				if !st.pushedClosed[i].Equal(c.OpenTime) {
					out = append(out, Update{symbol, &marketv1.CandleClosed{Candle: candleProto(c, true)}})
					st.pushedClosed[i] = c.OpenTime
				}
				if !st.pushedFlat[i].Equal(start) {
					out = append(out, Update{symbol, &marketv1.CandleUpdated{Candle: candleProto(domain.Flat(symbol, i, start, c.Close), false)}})
					st.pushedFlat[i] = start
				}
			} else if st.updated[i] {
				out = append(out, Update{symbol, &marketv1.CandleUpdated{Candle: candleProto(c, false)}})
			}
		}
		clear(st.updated)
		t := s.ticker(symbol, now)
		if st.pushedTicker == nil || !sameTicker(*st.pushedTicker, t) {
			out = append(out, Update{symbol, &marketv1.TickerUpdated{Ticker: TickerProto(t, now)}})
			st.pushedTicker = &t
		}
	}
	return out
}

func sameTicker(a, b domain.Ticker) bool {
	return a.Last.Equal(b.Last) && a.Open.Equal(b.Open) && a.High.Equal(b.High) && a.Low.Equal(b.Low) &&
		a.Volume.Equal(b.Volume) && a.QuoteVolume.Equal(b.QuoteVolume) && a.Trades == b.Trades &&
		a.Bid.Equal(b.Bid) && a.Ask.Equal(b.Ask)
}

func candleProto(c domain.Candle, closed bool) *marketv1.Candle {
	return &marketv1.Candle{
		Symbol: c.Symbol, Interval: string(c.Interval), OpenTime: timestamppb.New(c.OpenTime),
		Open: c.Open.String(), High: c.High.String(), Low: c.Low.String(), Close: c.Close.String(),
		Volume: c.Volume.String(), QuoteVolume: c.QuoteVolume.String(), TradeCount: c.Trades, Closed: closed,
	}
}

// TickerProto renders a ticker; unknown prices are empty.
func TickerProto(t domain.Ticker, now time.Time) *marketv1.Ticker {
	price := func(d decimal.Decimal) string {
		if d.IsZero() {
			return ""
		}
		return d.String()
	}
	out := &marketv1.Ticker{
		Symbol: t.Symbol, Last: price(t.Last), Open: price(t.Open), High: price(t.High), Low: price(t.Low),
		Volume: t.Volume.String(), QuoteVolume: t.QuoteVolume.String(), TradeCount: t.Trades,
		Bid: price(t.Bid), Ask: price(t.Ask), UpdatedAt: timestamppb.New(now),
	}
	if t.Open.IsPositive() {
		out.Change = t.Change.String()
	}
	return out
}

// Pusher publishes Updates to market.candle.events every PushInterval,
// straight to Kafka: they are derived and the next push supersedes them.
type Pusher struct {
	svc    *Service
	pub    kafka.Publisher
	events *event.Factory
	ref    *ReferenceCandles
	pushed prometheus.Counter
	failed prometheus.Counter
}

// UseReference swaps the candles of symbols in reference mode for their
// reference K-lines (before Run starts).
func (p *Pusher) UseReference(rc *ReferenceCandles) { p.ref = rc }

// NewPusher registers the push metrics with reg.
func NewPusher(svc *Service, pub kafka.Publisher, events *event.Factory, reg prometheus.Registerer) *Pusher {
	p := &Pusher{
		svc: svc, pub: pub, events: events,
		pushed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "market_updates_published_total", Help: "Candle and ticker updates published to market.candle.events.",
		}),
		failed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "market_update_publish_failures_total", Help: "Pushes that failed; the next one supersedes them.",
		}),
	}
	reg.MustRegister(p.pushed, p.failed)
	return p
}

// Run pushes until ctx ends (an app.Loop body).
func (p *Pusher) Run(ctx context.Context) error {
	tick := time.NewTicker(PushInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-tick.C:
			updates := p.svc.Updates(now)
			if p.ref != nil {
				updates = p.ref.Push(ctx, updates)
			}
			if err := p.Push(ctx, updates); err != nil && ctx.Err() == nil {
				p.failed.Inc()
				p.svc.log.WarnContext(ctx, "market update push failed", "error", err)
			}
		}
	}
}

// Push publishes updates, keyed by symbol.
func (p *Pusher) Push(ctx context.Context, updates []Update) error {
	if len(updates) == 0 {
		return nil
	}
	recs := make([]kafka.Record, 0, len(updates))
	for _, u := range updates {
		env, err := p.events.New(ctx, u.Message, "symbol", u.Symbol)
		if err != nil {
			return err
		}
		raw, err := proto.Marshal(env)
		if err != nil {
			return fmt.Errorf("market update %s: %w", u.Symbol, err)
		}
		recs = append(recs, kafka.Record{Topic: event.TopicMarketCandle, Key: u.Symbol, EventType: env.GetEventType(), Envelope: raw})
	}
	if err := p.pub.Publish(ctx, recs...); err != nil {
		return err
	}
	p.pushed.Add(float64(len(recs)))
	return nil
}
