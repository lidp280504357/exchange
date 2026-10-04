// Package consumer handles the events wallet-service follows.
package consumer

import (
	"context"
	"strings"

	"github.com/google/uuid"

	eventv1 "github.com/skill/exchange/api/gen/go/exchange/event/v1"
	ledgerv1 "github.com/skill/exchange/api/gen/go/exchange/ledger/v1"
	"github.com/skill/exchange/internal/platform/kafka"
	"github.com/skill/exchange/internal/wallet/application"
)

// Group is the consumer group.
const Group = "wallet-service"

// entryDepositCredit is the ledger's entry type for deposits.
const entryDepositCredit = "DEPOSIT_CREDIT"

// Ledger marks a deposit credited when its DEPOSIT_CREDIT journal (key
// deposit:<id>) appears on ledger.events; other journals are skipped. The
// deposit's status makes a redelivery harmless.
func Ledger(svc *application.Service) kafka.Handler {
	return func(ctx context.Context, env *eventv1.Envelope) error {
		var posted ledgerv1.EntryPosted
		if !env.GetPayload().MessageIs(&posted) {
			return nil
		}
		if err := env.GetPayload().UnmarshalTo(&posted); err != nil {
			return err
		}
		if posted.GetEntryType() != entryDepositCredit {
			return nil
		}
		id, ok := strings.CutPrefix(posted.GetIdempotencyKey(), "deposit:")
		if _, err := uuid.Parse(id); !ok || err != nil {
			return nil
		}
		return svc.OnCredited(ctx, id, posted.GetJournalId())
	}
}
