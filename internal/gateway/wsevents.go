package gateway

import (
	"context"

	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	ledgerv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/ledger/v1"
	notificationv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/notification/v1"
)

// WSTopics are the private event topics the hub follows.
var WSTopics = []string{"ledger.events", "notification.events"}

type balanceData struct {
	AccountType string `json:"account_type"`
	Asset       string `json:"asset"`
	Available   string `json:"available"`
	Frozen      string `json:"frozen"`
	EntryType   string `json:"entry_type"`
}

type notificationData struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// WSEvents turns private events into pushes: balance changes on
// "balances", new notifications on "notifications".
func WSEvents(h *Hub) func(context.Context, *eventv1.Envelope) error {
	return func(_ context.Context, env *eventv1.Envelope) error {
		var balance ledgerv1.BalanceChanged
		var notice notificationv1.NotificationCreated
		switch p := env.GetPayload(); {
		case p.MessageIs(&balance):
			if err := p.UnmarshalTo(&balance); err != nil {
				return err
			}
			h.Publish(balance.GetUserId(), "balances", balanceData{
				AccountType: balance.GetAccountType(), Asset: balance.GetAsset(), Available: balance.GetAvailable(),
				Frozen: balance.GetFrozen(), EntryType: balance.GetEntryType(),
			})
		case p.MessageIs(&notice):
			if err := p.UnmarshalTo(&notice); err != nil {
				return err
			}
			h.Publish(notice.GetUserId(), "notifications", notificationData{
				ID: notice.GetNotificationId(), Type: notice.GetType(), Title: notice.GetTitle(), Body: notice.GetBody(),
			})
		}
		return nil
	}
}
