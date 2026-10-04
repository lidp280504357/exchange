// Package ports declares what the risk application needs from the outside.
package ports

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/risk/domain"
)

// Store is the unit of work over the risk schema.
type Store interface {
	// Once runs fn in a transaction unless consumer already handled the
	// event; it reports whether fn ran.
	Once(ctx context.Context, consumer, eventID string, fn func(Repos) error) (bool, error)
	// Read returns repositories outside any transaction.
	Read() Repos
}

// Repos groups the repositories of one transaction.
type Repos interface {
	Devices() DeviceRepo
	Velocity() VelocityRepo
	Assessments() AssessmentRepo
	// Emit adds an event to the outbox of the transaction.
	Emit(ctx context.Context, topic string, msg proto.Message, aggregateType, aggregateID string) error
}

// DeviceRepo remembers the devices each user has used.
type DeviceRepo interface {
	// Record notes that the user used the device at at. It reports
	// whether the device is new for a user who had used another before.
	Record(ctx context.Context, userID, deviceID string, at time.Time) (newAfterOthers bool, err error)
}

// VelocityRepo counts events for velocity rules.
type VelocityRepo interface {
	// Tally records the event under (rule, value) and returns how many
	// events of (rule, value) happened in (at-window, at], this one
	// included; recording an event twice counts it once.
	Tally(ctx context.Context, rule, value, eventID string, at time.Time, window time.Duration) (int, error)
	// Purge deletes events that happened before cutoff.
	Purge(ctx context.Context, cutoff time.Time) (int64, error)
}

// AssessmentRepo stores assessments.
type AssessmentRepo interface {
	Insert(ctx context.Context, r domain.Record) error
	// List returns the newest assessments, of one user when userID is set.
	List(ctx context.Context, userID string, limit int) ([]domain.Record, error)
}

// Flags reads feature flags.
type Flags interface {
	Enabled(key string, s flags.Subject) bool
}
