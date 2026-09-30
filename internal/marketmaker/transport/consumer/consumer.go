// Package consumer feeds HOUSE's liquidity publisher the public books from
// the tail of market.depth and derivatives.market.depth.
package consumer

import (
	"context"

	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
	"github.com/lidp280504357/exchange/internal/marketmaker/application"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
)

// Depth is the kafka.Handler for the public book topics.
func Depth(p *application.Publisher) kafka.Handler {
	return func(_ context.Context, env *eventv1.Envelope) error {
		var snap marketv1.DepthSnapshot
		var up marketv1.DepthUpdate
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
		}
		return nil
	}
}
