// Package application runs the risk rules on the events risk-service
// consumes (requirements §5.13).
package application

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	riskv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/risk/v1"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/flags"
	"github.com/lidp280504357/exchange/internal/risk/domain"
	"github.com/lidp280504357/exchange/internal/risk/ports"
)

// Consumer names risk-service in inboxes and consumer groups.
const Consumer = "risk-service"

// Service assesses events.
type Service struct {
	Store ports.Store
	Rules []domain.Rule
	Flags ports.Flags
	Log   *slog.Logger
	Now   func() time.Time
}

// Assess runs the rules on an observation once: a redelivered event is
// skipped. An assessment with hits is stored and published as RiskScored;
// a REVIEW or REJECT that risk.enforce allows for the user is also
// published as RiskActionTaken for the owning services to carry out.
// Events without a user are ignored: there is nobody to attach a score to.
func (s *Service) Assess(ctx context.Context, o domain.Observation) error {
	if o.UserID == "" {
		return nil
	}
	_, err := s.Store.Once(ctx, Consumer, o.EventID, func(r ports.Repos) error {
		if o.DeviceID != "" && (o.EventType == domain.EventRegistered || o.EventType == domain.EventLogin) {
			isNew, err := r.Devices().Record(ctx, o.UserID, o.DeviceID, o.At)
			if err != nil {
				return err
			}
			o.NewDevice = isNew && o.EventType == domain.EventLogin
		}
		a, err := domain.Evaluate(s.Rules, o, func(rule domain.Rule, value string) (int, error) {
			return r.Velocity().Tally(ctx, rule.ID, value, o.EventID, o.At, time.Duration(rule.Window))
		})
		if err != nil || len(a.Hits) == 0 {
			return err
		}
		rec := domain.Record{
			ID: uuid.Must(uuid.NewV7()).String(), UserID: o.UserID, SourceEventID: o.EventID, SourceEventType: o.EventType,
			Score: a.Score, Action: a.Action, Hits: a.Hits, CreatedAt: s.Now(),
		}
		rec.Enforced = a.Action >= domain.ActionReview &&
			s.Flags.Enabled(flags.KeyRiskEnforce, flags.Subject{UserID: o.UserID, Region: o.Region})
		if err := r.Assessments().Insert(ctx, rec); err != nil {
			return err
		}
		hits := make([]*riskv1.RuleHit, len(a.Hits))
		rules := make([]string, len(a.Hits))
		for i, h := range a.Hits {
			hits[i] = &riskv1.RuleHit{Rule: h.Rule, Score: int32(h.Score), Detail: h.Detail} //nolint:gosec // scores are 0..100
			rules[i] = h.Rule
		}
		if err := r.Emit(ctx, event.TopicRisk, &riskv1.RiskScored{
			UserId: o.UserID, AssessmentId: rec.ID, SourceEventId: o.EventID, SourceEventType: o.EventType,
			Score: int32(a.Score), Action: toProto(a.Action), Hits: hits, Enforced: rec.Enforced, //nolint:gosec // 0..100
		}, "user", o.UserID); err != nil {
			return err
		}
		s.Log.InfoContext(ctx, "risk assessed", "user_id", o.UserID, "score", a.Score, "action", a.Action.String(),
			"rules", rules, "enforced", rec.Enforced)
		if !rec.Enforced {
			return nil
		}
		return r.Emit(ctx, event.TopicRisk, &riskv1.RiskActionTaken{
			UserId: o.UserID, AssessmentId: rec.ID, Action: toProto(a.Action), Rules: rules,
		}, "user", o.UserID)
	})
	return err
}

// Assessments lists the newest assessments, of one user when userID is set.
func (s *Service) Assessments(ctx context.Context, userID string, limit int) ([]domain.Record, error) {
	return s.Store.Read().Assessments().List(ctx, userID, limit)
}

// Purge drops velocity events older than the longest rule window.
func (s *Service) Purge(ctx context.Context) (int64, error) {
	return s.Store.Read().Velocity().Purge(ctx, s.Now().Add(-domain.Retention(s.Rules)))
}

func toProto(a domain.Action) riskv1.Action {
	switch a {
	case domain.ActionNone:
		return riskv1.Action_ACTION_NONE
	case domain.ActionStepUp:
		return riskv1.Action_ACTION_STEP_UP
	case domain.ActionReview:
		return riskv1.Action_ACTION_REVIEW
	case domain.ActionReject:
		return riskv1.Action_ACTION_REJECT
	}
	return riskv1.Action_ACTION_UNSPECIFIED
}
