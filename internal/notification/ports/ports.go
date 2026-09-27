// Package ports declares what notification-service's application layer
// needs from the outside world.
package ports

import (
	"context"

	"github.com/lidp280504357/exchange/internal/notification/domain"
)

// Provider sends messages through one vendor on one channel. Business code
// never sees vendor SDKs (§5.3).
type Provider interface {
	Name() string
	// Send returns the vendor's message ID. Failures are *domain.SendError.
	Send(ctx context.Context, m domain.Message) (string, error)
}

// DeliveryStore persists deliveries and their outcomes.
type DeliveryStore interface {
	CreateDelivery(ctx context.Context, d domain.Delivery) error
	// RecordAttempt stores the outcome of one provider attempt.
	RecordAttempt(ctx context.Context, id string, status domain.Status, provider string, class domain.FailureClass, providerMessageID string) error
	// FailDelivery marks d FAILED and queues its DeliveryFailed event in the
	// same transaction.
	FailDelivery(ctx context.Context, d domain.Delivery, class domain.FailureClass) error
}
