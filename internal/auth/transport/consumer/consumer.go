// Package consumer handles the events auth-service subscribes to.
package consumer

import (
	"context"

	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	userv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/user/v1"
	"github.com/lidp280504357/exchange/internal/auth/application"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
)

// Handler routes user.events to the account service; other event types
// are not auth-service's concern and are skipped.
func Handler(accounts *application.AccountService) kafka.Handler {
	return func(ctx context.Context, env *eventv1.Envelope) error {
		var changed userv1.UserStatusChanged
		if env.GetPayload().MessageIs(&changed) {
			if err := env.GetPayload().UnmarshalTo(&changed); err != nil {
				return err
			}
			return accounts.OnUserStatusChanged(ctx, env.GetEventId(), changed.GetUserId(), changed.GetToStatus(), env.GetOccurredAt().AsTime())
		}
		return nil
	}
}
