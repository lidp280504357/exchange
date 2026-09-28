package consumer

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	authv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/auth/v1"
	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/risk/domain"
)

func envelope(t *testing.T, msg proto.Message) *eventv1.Envelope {
	t.Helper()
	env, err := event.NewFactory("auth-service", "test").New(context.Background(), msg, "user", "u1")
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func TestObserveMapsTheAuthEvents(t *testing.T) {
	for name, tc := range map[string]struct {
		msg  proto.Message
		want domain.Observation
	}{
		"registration": {
			&authv1.UserRegistered{UserId: "u1", DeviceId: "d1", IpMask: "203.0.113.*", Region: "SG"},
			domain.Observation{EventType: domain.EventRegistered, UserID: "u1", DeviceID: "d1", Network: "203.0.113.*", Region: "SG"},
		},
		"login": {
			&authv1.LoginSucceeded{UserId: "u1", DeviceId: "d2", IpMask: "2001:db8:1::*", NewDevice: true},
			domain.Observation{EventType: domain.EventLogin, UserID: "u1", DeviceID: "d2", Network: "2001:db8:1::*"},
		},
		"failed login": {
			&authv1.LoginFailed{UserId: "u1", IpMask: "203.0.113.*"},
			domain.Observation{EventType: domain.EventLoginFailed, UserID: "u1", Network: "203.0.113.*"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			env := envelope(t, tc.msg)
			got, ok, err := observe(env)
			if err != nil || !ok {
				t.Fatalf("observe: %v %v", ok, err)
			}
			tc.want.EventID, tc.want.At = env.GetEventId(), env.GetOccurredAt().AsTime()
			// NewDevice is decided from risk-service's own device history,
			// not from auth's flag.
			if got != tc.want {
				t.Fatalf("got %+v\nwant %+v", got, tc.want)
			}
		})
	}
	if _, ok, err := observe(envelope(t, &authv1.PasswordChanged{UserId: "u1"})); ok || err != nil {
		t.Fatalf("other events are skipped: %v %v", ok, err)
	}
}
