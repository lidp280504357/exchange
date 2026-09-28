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

// NoticeStore keeps the in-app inbox.
type NoticeStore interface {
	// CreateNotice stores n and queues NotificationCreated in one
	// transaction, unless consumer already handled eventID; it reports
	// whether it stored n.
	CreateNotice(ctx context.Context, consumer, eventID string, n domain.Notice) (bool, error)
	// ListNotices returns up to limit notices older than beforeID (""
	// for the newest), newest first.
	ListNotices(ctx context.Context, userID, beforeID string, limit int) ([]domain.Notice, error)
	UnreadCount(ctx context.Context, userID string) (int, error)
	// MarkRead marks the given notices, or all when ids is empty, and
	// returns how many changed.
	MarkRead(ctx context.Context, userID string, ids []string) (int64, error)
}

// Recipient is what messages to a user need to know.
type Recipient struct {
	Language         string
	Timezone         string
	AntiPhishingCode string
}

// Contact is a verified address of a user.
type Contact struct {
	Channel domain.Channel
	Value   string
}

// Recipients looks users up in user-service and auth-service.
type Recipients interface {
	// Recipient returns the user's preferences; unknown users fail with
	// COMMON_NOT_FOUND.
	Recipient(ctx context.Context, userID string) (Recipient, error)
	Contacts(ctx context.Context, userID string) ([]Contact, error)
}
