// Package consumer handles the events ledger-service subscribes to.
package consumer

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	authv1 "github.com/skill/exchange/api/gen/go/exchange/auth/v1"
	eventv1 "github.com/skill/exchange/api/gen/go/exchange/event/v1"
	walletv1 "github.com/skill/exchange/api/gen/go/exchange/wallet/v1"
	"github.com/skill/exchange/internal/ledger/application"
	"github.com/skill/exchange/internal/ledger/domain"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/kafka"
)

// Group is the consumer group.
const Group = "ledger-service"

// Topics are the topics the group reads.
var Topics = []string{event.TopicAuth, event.TopicWalletDeposit}

// Handler gives new users their simulated funds and books confirmed
// deposits; the journals' keys make redeliveries harmless, so no inbox is
// needed.
func Handler(ledger *application.Service) kafka.Handler {
	return func(ctx context.Context, env *eventv1.Envelope) error {
		var (
			registered authv1.UserRegistered
			confirmed  walletv1.DepositConfirmed
		)
		p := env.GetPayload()
		switch {
		case p.MessageIs(&registered):
			if err := p.UnmarshalTo(&registered); err != nil {
				return err
			}
			return ledger.OnUserRegistered(ctx, env.GetEventId(), registered.GetUserId(), registered.GetRegion())
		case p.MessageIs(&confirmed):
			if err := p.UnmarshalTo(&confirmed); err != nil {
				return err
			}
			d := confirmed.GetDeposit()
			amount, err := decimal.NewFromString(d.GetAmount())
			if err != nil {
				return fmt.Errorf("deposit %s: bad amount %q: %w", d.GetDepositId(), d.GetAmount(), err)
			}
			_, err = ledger.CreditDeposit(ctx, env.GetEventId(), domain.Deposit{
				ID: d.GetDepositId(), UserID: d.GetUserId(), Asset: d.GetAsset(), Amount: amount, Network: d.GetNetwork(),
				TxHash: d.GetTxHash(), Unclaimed: d.GetUnclaimed(), Reason: d.GetReason(),
			})
			return err
		}
		return nil
	}
}
