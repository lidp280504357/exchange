// Package consumer handles the events user-service subscribes to.
package consumer

import (
	"context"

	eventv1 "github.com/skill/exchange/api/gen/go/exchange/event/v1"
	riskv1 "github.com/skill/exchange/api/gen/go/exchange/risk/v1"
	"github.com/skill/exchange/internal/platform/kafka"
	"github.com/skill/exchange/internal/user/application"
)

// Handler carries out the reviews risk-service enforces; other events on
// risk.events are only signals and are skipped.
func Handler(svc *application.Service) kafka.Handler {
	return func(ctx context.Context, env *eventv1.Envelope) error {
		var taken riskv1.RiskActionTaken
		if !env.GetPayload().MessageIs(&taken) {
			return nil
		}
		if err := env.GetPayload().UnmarshalTo(&taken); err != nil {
			return err
		}
		switch taken.GetAction() {
		case riskv1.Action_ACTION_REVIEW, riskv1.Action_ACTION_REJECT:
			return svc.OnRiskAction(ctx, env.GetEventId(), taken.GetUserId(), taken.GetRules())
		}
		return nil
	}
}
