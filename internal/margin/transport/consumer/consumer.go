// Package consumer feeds margin-service from Kafka: the ledger's journals
// on ledger.events that touch margin accounts — the monitor values those
// accounts again — among them the automatic repayments the trades'
// settlement books.
package consumer

import (
	"context"
	"slices"

	"github.com/shopspring/decimal"

	eventv1 "github.com/skill/exchange/api/gen/go/exchange/event/v1"
	ledgerv1 "github.com/skill/exchange/api/gen/go/exchange/ledger/v1"
	"github.com/skill/exchange/internal/margin/application"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/platform/kafka"
)

// Group is the consumer group of ledger.events.
const Group = "margin-service-ledger"

// Ledger hands the journals that touch margin accounts to the service.
func Ledger(svc *application.Service) kafka.Handler {
	return func(ctx context.Context, env *eventv1.Envelope) error {
		posted := &ledgerv1.EntryPosted{}
		if !env.GetPayload().MessageIs(posted) {
			return nil // BalanceChanged, or a newer type this build does not know
		}
		if err := env.GetPayload().UnmarshalTo(posted); err != nil {
			return err
		}
		if !slices.ContainsFunc(posted.GetLines(), func(l *ledgerv1.EntryLine) bool { return domain.MarginRow(l.GetAccountType()) }) {
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
