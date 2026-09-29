// Package ports declares what the admin console's application layer
// needs: its own storage and the services it acts on.
package ports

import (
	"context"
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/lidp280504357/exchange/internal/admin/domain"
)

// Store is the unit of work over the admin schema.
type Store interface {
	Tx(ctx context.Context, fn func(Repos) error) error
	Read() Repos
}

// Repos groups the repositories of one transaction.
type Repos interface {
	Admins() AdminRepo
	Sessions() SessionRepo
	Approvals() ApprovalRepo
	// Audit queues an administrator's action on audit.events.
	Audit(ctx context.Context, msg proto.Message, actor string) error
}

// AdminRepo stores administrators.
type AdminRepo interface {
	Insert(ctx context.Context, a domain.Admin) error
	Update(ctx context.Context, a domain.Admin) error
	// ByEmail returns the administrator with email, or nil.
	ByEmail(ctx context.Context, email string) (*domain.Admin, error)
	// ByEmailForUpdate is ByEmail with the row locked.
	ByEmailForUpdate(ctx context.Context, email string) (*domain.Admin, error)
	// Get returns an administrator, or nil.
	Get(ctx context.Context, id string) (*domain.Admin, error)
	List(ctx context.Context) ([]domain.Admin, error)
}

// SessionRepo stores sessions by token hash.
type SessionRepo interface {
	Insert(ctx context.Context, s domain.Session) error
	// Get returns a session that is not revoked, or nil.
	Get(ctx context.Context, hash []byte) (*domain.Session, error)
	Touch(ctx context.Context, hash []byte, now time.Time) error
	Revoke(ctx context.Context, hash []byte, now time.Time) error
	// RevokeAll ends every session of an administrator.
	RevokeAll(ctx context.Context, adminID string, now time.Time) error
}

// ApprovalRepo stores two-person requests.
type ApprovalRepo interface {
	Insert(ctx context.Context, a domain.Approval) error
	Update(ctx context.Context, a domain.Approval) error
	GetForUpdate(ctx context.Context, id string) (*domain.Approval, error)
	// List returns the requests in a status ("": all), newest first.
	List(ctx context.Context, status string, limit int) ([]domain.Approval, error)
}

// User is an account as the console shows it.
type User struct {
	ID        string
	Status    string
	Region    string
	Language  string
	KYCLevel  int32
	CreatedAt time.Time
}

// Balance is one of a user's balances.
type Balance struct {
	AccountType string
	Asset       string
	Available   string
	Frozen      string
}

// Users reads and changes accounts (auth-, user- and ledger-service).
type Users interface {
	// Find returns the user of an email address or phone number.
	Find(ctx context.Context, identifier string) (string, error)
	Get(ctx context.Context, userID string) (User, error)
	Balances(ctx context.Context, userID string) ([]Balance, error)
	// ChangeStatus returns the previous status.
	ChangeStatus(ctx context.Context, userID, to, reason, actor, note string) (string, error)
}

// Orders cancels a user's orders (spot-trading-service).
type Orders interface {
	CancelAll(ctx context.Context, userID string) error
}

// Withdrawals lists and reviews withdrawals (wallet-service); the items
// pass through as the wallet renders them.
type Withdrawals interface {
	List(ctx context.Context, status string) (json.RawMessage, error)
	Review(ctx context.Context, id string, approve bool, reviewer, reason string) (json.RawMessage, error)
}

// Instruments lists and changes reference data (instrument-service).
type Instruments interface {
	// List returns the assets and pairs as JSON.
	List(ctx context.Context) (json.RawMessage, error)
	// SetPairStatus returns the previous status.
	SetPairStatus(ctx context.Context, symbol, to, reason, actor string) (string, error)
}

// Flag is a feature switch.
type Flag struct {
	Key         string          `json:"key"`
	Enabled     bool            `json:"enabled"`
	Description string          `json:"description"`
	Rules       json.RawMessage `json:"rules"`
	Version     int64           `json:"version"`
	UpdatedBy   string          `json:"updated_by"`
	// UpdatedAt is nil for a flag never set (off).
	UpdatedAt *time.Time `json:"updated_at"`
}

// Flags reads and switches feature flags (the shared config schema); a
// change is recorded with its audit event.
type Flags interface {
	List(ctx context.Context) ([]Flag, error)
	Switch(ctx context.Context, key string, enabled bool, actor, reason string) (Flag, error)
}

// Ledger books manual adjustments (ledger-service).
type Ledger interface {
	Adjust(ctx context.Context, key, userID, asset string, amount decimal.Decimal, actor, reason string) (journalID string, err error)
}

// AuditEntry is a line of the audit trail.
type AuditEntry struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	Actor      string          `json:"actor"`
	Target     string          `json:"target"`
	OccurredAt time.Time       `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

// AuditLog reads the audit trail (ClickHouse audit_logs).
type AuditLog interface {
	Search(ctx context.Context, actor, target string, limit int) ([]AuditEntry, error)
}
