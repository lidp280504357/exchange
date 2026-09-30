package consumer

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	orderv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/order/v1"
	tradev1 "github.com/lidp280504357/exchange/api/gen/go/exchange/trade/v1"
	"github.com/lidp280504357/exchange/internal/ledger/application"
	"github.com/lidp280504357/exchange/internal/ledger/domain"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
)

// SettlementGroup is the consumer group that settles trade.events.
const SettlementGroup = "ledger-settlement"

// Settlement books engine trades batch by batch, one transaction each
// (§11.1 step 7). The ledger skips trades it has recorded, so redelivered
// batches are harmless.
type Settlement struct {
	svc      *application.Service
	log      *slog.Logger
	settled  prometheus.Counter
	failed   prometheus.Counter
	duration prometheus.Histogram
}

// NewSettlement registers the settlement metrics with reg.
func NewSettlement(svc *application.Service, log *slog.Logger, reg prometheus.Registerer) *Settlement {
	s := &Settlement{
		svc: svc, log: log,
		settled: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "ledger_trades_settled_total", Help: "Engine trades settled.",
		}),
		failed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "ledger_trades_failed_total", Help: "Engine trades the ledger refused and parked as FAILED; anything above 0 is an incident.",
		}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "ledger_settlement_batch_seconds", Help: "Time to settle one batch of trades.",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		}),
	}
	reg.MustRegister(s.settled, s.failed, s.duration)
	return s
}

// Handle is the kafka.BatchHandler.
func (s *Settlement) Handle(ctx context.Context, batch []kafka.Delivery) error {
	trades := make([]domain.Trade, 0, len(batch))
	for _, d := range batch {
		var m tradev1.TradeExecuted
		if !d.Envelope.GetPayload().MessageIs(&m) {
			continue
		}
		if err := d.Envelope.GetPayload().UnmarshalTo(&m); err != nil {
			// The envelope decoded, so this cannot happen short of corruption.
			s.log.ErrorContext(ctx, "trade not decodable", "event_id", d.Envelope.GetEventId(), "error", err)
			s.failed.Inc()
			continue
		}
		trades = append(trades, s.fromProto(ctx, &m, d))
	}
	if len(trades) == 0 {
		return nil
	}
	started := time.Now()
	res, err := s.svc.Settle(ctx, trades)
	if err != nil {
		return err
	}
	s.duration.Observe(time.Since(started).Seconds())
	s.settled.Add(float64(res.Settled))
	s.failed.Add(float64(res.Failed))
	for _, t := range res.Refused {
		s.log.ErrorContext(ctx, "trade settlement refused", "trade_id", t.ID, "symbol", t.Symbol,
			"code", t.ErrorCode, "error", t.Error)
	}
	return nil
}

// fromProto reads a trade; an amount that does not parse stays zero, which
// settlement refuses (the trade is parked with the reason).
func (s *Settlement) fromProto(ctx context.Context, m *tradev1.TradeExecuted, d kafka.Delivery) domain.Trade {
	amount := func(name, v string) decimal.Decimal {
		if v == "" {
			return decimal.Zero
		}
		out, err := decimal.NewFromString(v)
		if err != nil {
			s.log.ErrorContext(ctx, "trade amount does not parse", "trade_id", m.GetTradeId(), "field", name, "value", v)
		}
		return out
	}
	house := ""
	switch m.GetHouseSide() {
	case orderv1.Side_SIDE_BUY:
		house = domain.HouseBuy
	case orderv1.Side_SIDE_SELL:
		house = domain.HouseSell
	}
	return domain.Trade{
		ID: m.GetTradeId(), Number: m.GetTradeNumber(), Symbol: m.GetSymbol(), HouseSide: house,
		BaseAsset: m.GetBaseAsset(), QuoteAsset: m.GetQuoteAsset(),
		Price: amount("price", m.GetPrice()), Quantity: amount("quantity", m.GetQuantity()),
		Quote:        amount("quote_quantity", m.GetQuoteQuantity()),
		BuyerOrderID: m.GetBuyerOrderId(), BuyerUserID: m.GetBuyerUserId(),
		SellerOrderID: m.GetSellerOrderId(), SellerUserID: m.GetSellerUserId(), BuyerIsMaker: m.GetBuyerIsMaker(),
		BuyerFee: amount("buyer_fee", m.GetBuyerFee()), SellerFee: amount("seller_fee", m.GetSellerFee()),
		BuyerLimit: amount("buyer_limit_price", m.GetBuyerLimitPrice()),
		EventID:    d.Envelope.GetEventId(), ExecutedAt: d.Envelope.GetOccurredAt().AsTime(),
	}
}
