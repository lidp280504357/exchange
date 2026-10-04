package postgres_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/admin/adapters/postgres"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/migrations"
)

// TestOneLiveRequestPerDepositOfNobody checks the index of admin 00012:
// while a request to credit a deposit of nobody waits or once one was
// carried out, another for it is refused (review ㉕).
func TestOneLiveRequestPerDepositOfNobody(t *testing.T) {
	db := testenv.Postgres(t)
	ctx, log := context.Background(), slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Admin(), log); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(db, event.NewFactory("admin-test", "t"))
	now := time.Now().UTC().Truncate(time.Microsecond)
	fin, err := domain.NewAdmin(uuid.Must(uuid.NewV7()).String(), "fin-"+uuid.NewString()[:8]+"@example.com", "Test", domain.RoleFinance, now)
	if err != nil {
		t.Fatal(err)
	}
	fin.PasswordHash, fin.TOTPSealed = "h", []byte{1}
	if err := store.Read().Admins().Insert(ctx, fin); err != nil {
		t.Fatal(err)
	}
	deposit, another := uuid.NewString(), uuid.NewString()
	request := func(kind, depositID string) domain.Approval {
		return domain.Approval{
			ID: uuid.Must(uuid.NewV7()).String(), Kind: kind, Status: domain.ApprovalPending, RequestedBy: fin.ID, CreatedAt: now,
			Mode: domain.ModeTwoPerson, Reason: "the sender's ticket",
			Payload: map[string]string{"deposit_id": depositID, "user_id": uuid.NewString(), "asset": "USDT", "amount": "1"},
		}
	}
	approvals := store.Read().Approvals()

	first := request(domain.KindDepositAssign, deposit)
	if err := approvals.Insert(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := approvals.Insert(ctx, request(domain.KindDepositAssign, deposit)); !errors.Is(err, domain.ErrDepositAssignOpen) {
		t.Fatalf("a second request while one waits: %v", err)
	}
	if err := approvals.Insert(ctx, request(domain.KindDepositAssign, another)); err != nil {
		t.Fatalf("another deposit's: %v", err)
	}
	if err := approvals.Insert(ctx, request(domain.KindLedgerAdjustment, deposit)); err != nil {
		t.Fatalf("another kind: %v", err)
	}

	// Rejected, it leaves the deposit to a new request; carried out, it does not.
	first.Status, first.DecidedBy, first.DecidedAt, first.Result = domain.ApprovalRejected, fin.ID, now, "no transfer shown"
	if err := approvals.Update(ctx, first); err != nil {
		t.Fatal(err)
	}
	again := request(domain.KindDepositAssign, deposit)
	again.Mode = domain.ModeSingle
	if err := approvals.Insert(ctx, again); err != nil {
		t.Fatalf("after a rejection: %v", err)
	}
	again.Status, again.JournalID, again.DecidedBy, again.DecidedAt = domain.ApprovalExecuted, uuid.NewString(), fin.ID, now
	if err := approvals.Update(ctx, again); err != nil {
		t.Fatal(err)
	}
	if err := approvals.Insert(ctx, request(domain.KindDepositAssign, deposit)); !errors.Is(err, domain.ErrDepositAssignOpen) {
		t.Fatalf("once carried out: %v", err)
	}
}
