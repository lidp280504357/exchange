// Package notifier hands OTP codes to notification-service over gRPC.
package notifier

import (
	"context"

	notificationv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/notification/v1"
	"github.com/lidp280504357/exchange/internal/auth/ports"
)

// Client implements ports.Notifier.
type Client struct {
	c notificationv1.NotificationServiceClient
}

// New wraps a NotificationService client.
func New(c notificationv1.NotificationServiceClient) *Client { return &Client{c: c} }

// SendOTP delivers a code.
func (n *Client) SendOTP(ctx context.Context, d ports.OTPDelivery) error {
	_, err := n.c.SendOtp(ctx, &notificationv1.SendOtpRequest{
		ChallengeId: d.ChallengeID,
		Channel:     string(d.Channel),
		Target:      d.Target,
		Code:        d.Code,
		Scene:       string(d.Scene),
		Language:    d.Language,
		TtlSeconds:  int32(d.TTL.Seconds()),
		UserId:      d.UserID,
	})
	return err
}
