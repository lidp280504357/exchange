// Package domain holds notification-service's core types: messages,
// deliveries and their outcomes, and the templates of what users receive.
package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Channel is how a message reaches the user.
type Channel string

// Channels.
const (
	ChannelEmail Channel = "EMAIL"
	ChannelSMS   Channel = "SMS"
)

// ParseChannel accepts EMAIL or SMS.
func ParseChannel(s string) (Channel, error) {
	switch c := Channel(s); c {
	case ChannelEmail, ChannelSMS:
		return c, nil
	default:
		return "", apperr.Invalid(fmt.Sprintf("unknown channel %q", s))
	}
}

// Kind separates one-time codes from notices.
type Kind string

// Kinds.
const (
	KindOTP    Kind = "OTP"
	KindNotice Kind = "NOTICE"
)

// Status is the state of a delivery (requirements §6.1).
type Status string

// Delivery states.
const (
	StatusQueued         Status = "QUEUED"
	StatusSent           Status = "SENT"
	StatusFailedRetrying Status = "FAILED_RETRYING"
	StatusFailed         Status = "FAILED"
)

// FailureClass sorts provider failures for the operators (§6.2).
type FailureClass string

// Failure classes.
const (
	FailureTimeout             FailureClass = "TIMEOUT"
	FailureRejected            FailureClass = "REJECTED"
	FailureInvalidTarget       FailureClass = "INVALID_TARGET"
	FailureInsufficientBalance FailureClass = "INSUFFICIENT_BALANCE"
	FailureCircuitOpen         FailureClass = "CIRCUIT_OPEN"
	FailureUnknown             FailureClass = "UNKNOWN"
)

// SendError is a provider failure. Retryable failures are retried and then
// failed over to the next provider; others fail over at once.
type SendError struct {
	Class     FailureClass
	Retryable bool
	Err       error
}

func (e *SendError) Error() string { return fmt.Sprintf("%s: %v", e.Class, e.Err) }

func (e *SendError) Unwrap() error { return e.Err }

// ClassOf returns the failure class of err.
func ClassOf(err error) FailureClass {
	var se *SendError
	if errors.As(err, &se) {
		return se.Class
	}
	return FailureUnknown
}

// Message is what a provider sends.
type Message struct {
	Channel Channel
	To      string
	Subject string // mail only
	Text    string
	// IdempotencyKey lets providers drop duplicates of a retried send.
	IdempotencyKey string
}

// Delivery records one message handed to the providers.
type Delivery struct {
	ID         string
	Kind       Kind
	Channel    Channel
	Template   string
	TargetMask string
	UserID     string
	CreatedAt  time.Time
	// Attempts counts the provider attempts so far (a queued delivery's).
	Attempts int
}

// ErrProviderUnavailable is returned when no provider accepts the message
// (NOTIFY_PROVIDER_UNAVAILABLE, §6.2); the provider names stay internal.
var ErrProviderUnavailable = apperr.New(apperr.KindUnavailable, "NOTIFY_PROVIDER_UNAVAILABLE",
	"message delivery is temporarily unavailable")
