package gateway

import (
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	marketv1 "github.com/skill/exchange/api/gen/go/exchange/market/v1"
)

// The public channel liquidations:{contract} (design 2026-10-06 §3.3,
// api/openapi/market.yaml Liquidation): the reference market's
// liquidation orders on the contract, from market-data-service's
// market.liquidations, as they come (nothing kept for new subscribers:
// GET /v1/market/{symbol}/liquidations has the recent ones).

// liquidationData is a liquidation on "liquidations:{symbol}".
type liquidationData struct {
	Symbol       string `json:"symbol"`
	PositionSide string `json:"position_side"`
	Price        string `json:"price"`
	AveragePrice string `json:"average_price"`
	Quantity     string `json:"quantity"`
	ValueUSD     string `json:"value_usd"`
	TradedAt     string `json:"traded_at"`
}

// liquidationsRemembered bounds the event IDs a hub remembers to push a
// liquidation once: market-data publishes a batch again under the same IDs
// when its first try failed, which may have reached Kafka in part (C40 ⑥;
// review FD, A68 ①). Seconds of liquidations at Binance's busiest.
const liquidationsRemembered = 4096

// recentIDs remembers the last event IDs seen, at most a fixed number.
type recentIDs struct {
	mu   sync.Mutex
	ring []string
	next int
	seen map[string]struct{}
}

// first reports whether id was not seen yet, and remembers it.
func (r *recentIDs) first(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.seen[id]; ok {
		return false
	}
	if old := r.ring[r.next]; old != "" {
		delete(r.seen, old)
	}
	r.ring[r.next] = id
	r.next = (r.next + 1) % len(r.ring)
	r.seen[id] = struct{}{}
	return true
}

// liquidationIDs are each hub's recent liquidation event IDs.
var liquidationIDs sync.Map // *Hub -> *recentIDs

// liquidationsOf pushes a LiquidationOccurred to its contract's channel,
// once per event ID (eventID empty: always); false for any other payload.
func liquidationsOf(h *Hub, p interface {
	MessageIs(proto.Message) bool
	UnmarshalTo(proto.Message) error
}, eventID string,
) (bool, error) {
	var l marketv1.LiquidationOccurred
	if !p.MessageIs(&l) {
		return false, nil
	}
	if err := p.UnmarshalTo(&l); err != nil {
		return true, err
	}
	if eventID != "" {
		ids, _ := liquidationIDs.LoadOrStore(h, &recentIDs{ring: make([]string, liquidationsRemembered), seen: map[string]struct{}{}})
		if !ids.(*recentIDs).first(eventID) {
			return true, nil
		}
	}
	h.OnMarket(wsMarket{Channel: "liquidations:" + l.GetSymbol(), Data: liquidationData{
		Symbol: l.GetSymbol(), PositionSide: l.GetPositionSide(), Price: l.GetPrice(), AveragePrice: l.GetAveragePrice(),
		Quantity: l.GetQuantity(), ValueUSD: l.GetValueUsd(), TradedAt: l.GetTradedAt().AsTime().UTC().Format(time.RFC3339Nano),
	}}, false)
	return true, nil
}
