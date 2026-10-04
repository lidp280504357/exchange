// Package consumer feeds HOUSE's liquidity publisher from the tail of
// market.depth and derivatives.market.depth (the public books) and of
// instrument.events (pairs and contracts leaving or starting trading).
package consumer

import (
	"context"

	eventv1 "github.com/skill/exchange/api/gen/go/exchange/event/v1"
	instrumentv1 "github.com/skill/exchange/api/gen/go/exchange/instrument/v1"
	marketv1 "github.com/skill/exchange/api/gen/go/exchange/market/v1"
	"github.com/skill/exchange/internal/marketmaker/application"
	"github.com/skill/exchange/internal/platform/kafka"
)

// Depth is the kafka.Handler for the public book topics and the
// instrument events.
func Depth(p *application.Publisher) kafka.Handler {
	return func(_ context.Context, env *eventv1.Envelope) error {
		var snap marketv1.DepthSnapshot
		var up marketv1.DepthUpdate
		var pair instrumentv1.TradingPairStatusChanged
		var contract instrumentv1.ContractStatusChanged
		switch {
		case env.GetPayload().MessageIs(&snap):
			if err := env.GetPayload().UnmarshalTo(&snap); err != nil {
				return err
			}
			p.OnSnapshot(&snap)
		case env.GetPayload().MessageIs(&up):
			if err := env.GetPayload().UnmarshalTo(&up); err != nil {
				return err
			}
			p.OnUpdate(&up)
		case env.GetPayload().MessageIs(&pair):
			if err := env.GetPayload().UnmarshalTo(&pair); err != nil {
				return err
			}
			p.OnStatus(pair.GetSymbol(), pair.GetToStatus())
		case env.GetPayload().MessageIs(&contract):
			if err := env.GetPayload().UnmarshalTo(&contract); err != nil {
				return err
			}
			p.OnStatus(contract.GetSymbol(), contract.GetToStatus())
		}
		return nil
	}
}
