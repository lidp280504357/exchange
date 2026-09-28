// Package consumer turns account events into user notifications.
package consumer

import (
	"context"
	"strconv"

	"google.golang.org/protobuf/proto"

	authv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/auth/v1"
	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	userv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/user/v1"
	"github.com/lidp280504357/exchange/internal/notification/application"
	"github.com/lidp280504357/exchange/internal/notification/domain"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
)

// Topics are the topics the handler reads.
var Topics = []string{event.TopicAuth, event.TopicUser}

// Handler notifies users of security-relevant account events; other
// events are skipped.
func Handler(notices *application.Notices) kafka.Handler {
	return func(ctx context.Context, env *eventv1.Envelope) error {
		msg, err := env.GetPayload().UnmarshalNew()
		if err != nil {
			return nil //nolint:nilerr // payload types we do not link are not ours
		}
		e, ok := toEvent(msg)
		if !ok {
			return nil
		}
		e.ID, e.At = env.GetEventId(), env.GetOccurredAt().AsTime()
		return notices.Notify(ctx, e)
	}
}

// toEvent maps a payload to the notice it deserves.
func toEvent(msg proto.Message) (application.Event, bool) {
	switch m := msg.(type) {
	case *authv1.UserRegistered:
		return application.Event{UserID: m.GetUserId(), Type: domain.NoticeWelcome}, true
	case *authv1.LoginSucceeded:
		if !m.GetNewDevice() || m.GetMethod() == "REGISTER" {
			return application.Event{}, false
		}
		return application.Event{UserID: m.GetUserId(), Type: domain.NoticeNewDeviceLogin, Mail: true, Data: map[string]string{
			"ip": m.GetIpMask(), "user_agent": truncate(m.GetUserAgent(), 120), "device_id": m.GetDeviceId(),
		}}, true
	case *authv1.IdentityBound:
		return application.Event{UserID: m.GetUserId(), Type: domain.NoticeIdentityChanged, Mail: true, Data: map[string]string{
			"channel": m.GetChannel(), "new": m.GetIdentityMask(),
		}}, true
	case *authv1.IdentityRebound:
		return application.Event{UserID: m.GetUserId(), Type: domain.NoticeIdentityChanged, Mail: true, Data: map[string]string{
			"channel": m.GetChannel(), "old": m.GetOldMask(), "new": m.GetNewMask(),
		}}, true
	case *authv1.PasswordChanged:
		return application.Event{UserID: m.GetUserId(), Type: domain.NoticePasswordChanged, Mail: true, Data: map[string]string{
			"via_reset": strconv.FormatBool(m.GetViaReset()),
		}}, true
	case *authv1.LoginFailed:
		if !m.GetLocked() {
			return application.Event{}, false
		}
		return application.Event{UserID: m.GetUserId(), Type: domain.NoticeAccountLocked, Mail: true, Data: map[string]string{
			"ip": m.GetIpMask(),
		}}, true
	case *userv1.UserStatusChanged:
		return application.Event{UserID: m.GetUserId(), Type: domain.NoticeStatusChanged, Mail: true, Data: map[string]string{
			"from": m.GetFromStatus(), "to": m.GetToStatus(),
		}}, true
	}
	return application.Event{}, false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
