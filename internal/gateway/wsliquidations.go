package gateway

import (
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

// liquidationsOf pushes a LiquidationOccurred to its contract's channel;
// false for any other payload.
func liquidationsOf(h *Hub, p interface {
	MessageIs(proto.Message) bool
	UnmarshalTo(proto.Message) error
},
) (bool, error) {
	var l marketv1.LiquidationOccurred
	if !p.MessageIs(&l) {
		return false, nil
	}
	if err := p.UnmarshalTo(&l); err != nil {
		return true, err
	}
	h.OnMarket(wsMarket{Channel: "liquidations:" + l.GetSymbol(), Data: liquidationData{
		Symbol: l.GetSymbol(), PositionSide: l.GetPositionSide(), Price: l.GetPrice(), AveragePrice: l.GetAveragePrice(),
		Quantity: l.GetQuantity(), ValueUSD: l.GetValueUsd(), TradedAt: l.GetTradedAt().AsTime().UTC().Format(time.RFC3339Nano),
	}}, false)
	return true, nil
}
