// Package grpcapi serves notification-service's gRPC API.
package grpcapi

import (
	"context"

	notificationv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/notification/v1"
	"github.com/lidp280504357/exchange/internal/notification/application"
	"github.com/lidp280504357/exchange/internal/notification/domain"
)

// Server implements notificationv1.NotificationServiceServer.
type Server struct {
	notificationv1.UnimplementedNotificationServiceServer
	dispatcher *application.Dispatcher
}

// NewServer returns the gRPC API over dispatcher.
func NewServer(d *application.Dispatcher) *Server { return &Server{dispatcher: d} }

var statuses = map[domain.Status]notificationv1.DeliveryStatus{
	domain.StatusQueued:         notificationv1.DeliveryStatus_DELIVERY_STATUS_QUEUED,
	domain.StatusSent:           notificationv1.DeliveryStatus_DELIVERY_STATUS_SENT,
	domain.StatusFailedRetrying: notificationv1.DeliveryStatus_DELIVERY_STATUS_FAILED_RETRYING,
	domain.StatusFailed:         notificationv1.DeliveryStatus_DELIVERY_STATUS_FAILED,
}

// SendOtp delivers a one-time code.
func (s *Server) SendOtp(ctx context.Context, req *notificationv1.SendOtpRequest) (*notificationv1.SendOtpResponse, error) {
	id, status, err := s.dispatcher.SendOTP(ctx, application.OTPRequest{
		ChallengeID: req.GetChallengeId(),
		Channel:     req.GetChannel(),
		Target:      req.GetTarget(),
		Code:        req.GetCode(),
		Scene:       req.GetScene(),
		Language:    req.GetLanguage(),
		TTLSeconds:  int(req.GetTtlSeconds()),
		UserID:      req.GetUserId(),
	})
	if err != nil {
		return nil, err
	}
	return &notificationv1.SendOtpResponse{DeliveryId: id, Status: statuses[status]}, nil
}
