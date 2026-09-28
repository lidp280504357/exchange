// Package consumer turns the events risk-service subscribes to into
// observations for the rules.
package consumer

import (
	"context"

	"google.golang.org/protobuf/proto"

	authv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/auth/v1"
	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
	"github.com/lidp280504357/exchange/internal/risk/application"
	"github.com/lidp280504357/exchange/internal/risk/domain"
)

// Handler assesses registrations, logins and failed logins; other events
// are skipped. The service's inbox makes redeliveries harmless.
func Handler(svc *application.Service) kafka.Handler {
	return func(ctx context.Context, env *eventv1.Envelope) error {
		o, ok, err := observe(env)
		if err != nil || !ok {
			return err
		}
		return svc.Assess(ctx, o)
	}
}

func observe(env *eventv1.Envelope) (domain.Observation, bool, error) {
	o := domain.Observation{EventID: env.GetEventId(), EventType: env.GetEventType(), At: env.GetOccurredAt().AsTime()}
	var msg proto.Message
	switch o.EventType {
	case domain.EventRegistered:
		msg = &authv1.UserRegistered{}
	case domain.EventLogin:
		msg = &authv1.LoginSucceeded{}
	case domain.EventLoginFailed:
		msg = &authv1.LoginFailed{}
	default:
		return o, false, nil
	}
	if err := env.GetPayload().UnmarshalTo(msg); err != nil {
		return o, false, err
	}
	switch m := msg.(type) {
	case *authv1.UserRegistered:
		o.UserID, o.DeviceID, o.Network, o.Region = m.GetUserId(), m.GetDeviceId(), m.GetIpMask(), m.GetRegion()
	case *authv1.LoginSucceeded:
		o.UserID, o.DeviceID, o.Network = m.GetUserId(), m.GetDeviceId(), m.GetIpMask()
	case *authv1.LoginFailed:
		o.UserID, o.Network = m.GetUserId(), m.GetIpMask()
	}
	return o, true, nil
}
