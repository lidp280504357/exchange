// Package consumer handles the events ledger-service subscribes to.
package consumer

import (
	"context"

	authv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/auth/v1"
	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	"github.com/lidp280504357/exchange/internal/ledger/application"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
)

// Group is the consumer group.
const Group = "ledger-service"

// Handler gives new users their simulated funds; the journal's key makes
// redeliveries harmless, so no inbox is needed.
func Handler(ledger *application.Service) kafka.Handler {
	return func(ctx context.Context, env *eventv1.Envelope) error {
		var registered authv1.UserRegistered
		if !env.GetPayload().MessageIs(&registered) {
			return nil
		}
		if err := env.GetPayload().UnmarshalTo(&registered); err != nil {
			return err
		}
		return ledger.OnUserRegistered(ctx, env.GetEventId(), registered.GetUserId(), registered.GetRegion())
	}
}
