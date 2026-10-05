// Package consumer feeds margin-service from Kafka: the ledger's journals
// on ledger.events, for the automatic repayments the trades' settlement
// books.
package consumer

import (
	"context"
	"errors"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/reflect/protoregistry"

	eventv1 "github.com/skill/exchange/api/gen/go/exchange/event/v1"
	ledgerv1 "github.com/skill/exchange/api/gen/go/exchange/ledger/v1"
	"github.com/skill/exchange/internal/margin/application"
	"github.com/skill/exchange/internal/platform/kafka"
)

// Group is the consumer group of ledger.events.
const Group = "margin-service-ledger"

// Ledger hands the journals that touch margin accounts to the service.
func Ledger(svc *application.Service) kafka.Handler {
	return func(ctx context.Context, env *eventv1.Envelope) error {
		msg, err := env.GetPayload().UnmarshalNew()
		if err != nil {
			if errors.Is(err, protoregistry.NotFound) {
				return nil // a newer event type this build does not know
			}
			return err
		}
		posted, ok := msg.(*ledgerv1.EntryPosted)
		if !ok || posted.GetEntryType() != "MARGIN_REPAY" {
			return nil
		}
		e := application.Entry{
			JournalID: posted.GetJournalId(), EntryType: posted.GetEntryType(), IdemKey: posted.GetIdempotencyKey(), Memo: posted.GetMemo(),
			At: env.GetOccurredAt().AsTime(),
		}
		for _, l := range posted.GetLines() {
			amount, err := decimal.NewFromString(l.GetAmount())
			if err != nil {
				return err
			}
			e.Lines = append(e.Lines, application.EntryLine{
				UserID: l.GetOwnerId(), AccountType: l.GetAccountType(), Scope: l.GetScope(), Asset: l.GetAsset(), Amount: amount,
			})
		}
		return svc.OnEntry(ctx, e)
	}
}
