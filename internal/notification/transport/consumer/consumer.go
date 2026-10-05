// Package consumer turns account events into user notifications.
package consumer

import (
	"context"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

	authv1 "github.com/skill/exchange/api/gen/go/exchange/auth/v1"
	eventv1 "github.com/skill/exchange/api/gen/go/exchange/event/v1"
	marginv1 "github.com/skill/exchange/api/gen/go/exchange/margin/v1"
	userv1 "github.com/skill/exchange/api/gen/go/exchange/user/v1"
	walletv1 "github.com/skill/exchange/api/gen/go/exchange/wallet/v1"
	"github.com/skill/exchange/internal/notification/application"
	"github.com/skill/exchange/internal/notification/domain"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/kafka"
)

// Topics are the topics the handler reads.
var Topics = []string{event.TopicAuth, event.TopicUser, event.TopicWalletDeposit, event.TopicWalletWithdrawal, event.TopicMargin}

// Handler notifies users of security-relevant account events and of
// deposits credited or held; other events are skipped.
func Handler(notices *application.Notices) kafka.Handler {
	return func(ctx context.Context, env *eventv1.Envelope) error {
		msg, err := env.GetPayload().UnmarshalNew()
		if err != nil {
			return nil //nolint:nilerr // payload types we do not link are not ours
		}
		e, ok := toEvent(msg)
		if !ok {
			return nil
		}
		e.ID, e.At = env.GetEventId(), env.GetOccurredAt().AsTime()
		return notices.Notify(ctx, e)
	}
}

// toEvent maps a payload to the notice it deserves.
func toEvent(msg proto.Message) (application.Event, bool) {
	switch m := msg.(type) {
	case *authv1.UserRegistered:
		return application.Event{UserID: m.GetUserId(), Type: domain.NoticeWelcome}, true
	case *authv1.LoginSucceeded:
		if !m.GetNewDevice() || m.GetMethod() == "REGISTER" {
			return application.Event{}, false
		}
		return application.Event{UserID: m.GetUserId(), Type: domain.NoticeNewDeviceLogin, Mail: true, Data: map[string]string{
			"ip": m.GetIpMask(), "user_agent": truncate(m.GetUserAgent(), 120), "device_id": m.GetDeviceId(),
		}}, true
	case *authv1.IdentityBound:
		return application.Event{UserID: m.GetUserId(), Type: domain.NoticeIdentityChanged, Mail: true, Data: map[string]string{
			"channel": m.GetChannel(), "new": m.GetIdentityMask(),
		}}, true
	case *authv1.IdentityRebound:
		return application.Event{UserID: m.GetUserId(), Type: domain.NoticeIdentityChanged, Mail: true, Data: map[string]string{
			"channel": m.GetChannel(), "old": m.GetOldMask(), "new": m.GetNewMask(),
		}}, true
	case *authv1.PasswordChanged:
		return application.Event{UserID: m.GetUserId(), Type: domain.NoticePasswordChanged, Mail: true, Data: map[string]string{
			"via_reset": strconv.FormatBool(m.GetViaReset()),
		}}, true
	case *authv1.TotpEnabled:
		return application.Event{UserID: m.GetUserId(), Type: domain.NoticeTOTPChanged, Mail: true, Data: map[string]string{"enabled": "true"}}, true
	case *authv1.TotpDisabled:
		return application.Event{UserID: m.GetUserId(), Type: domain.NoticeTOTPChanged, Mail: true, Data: map[string]string{"enabled": "false"}}, true
	case *authv1.LoginFailed:
		if !m.GetLocked() {
			return application.Event{}, false
		}
		return application.Event{UserID: m.GetUserId(), Type: domain.NoticeAccountLocked, Mail: true, Data: map[string]string{
			"ip": m.GetIpMask(),
		}}, true
	case *walletv1.DepositCredited:
		d := m.GetDeposit()
		if d.GetUnclaimed() {
			return depositEvent(d, domain.NoticeDepositUnclaimed, true), true
		}
		return depositEvent(d, domain.NoticeDepositCredited, false), true
	case *walletv1.DepositRejected:
		return depositEvent(m.GetDeposit(), domain.NoticeDepositUnclaimed, true), true
	case *walletv1.WithdrawalRequested:
		return withdrawalEvent(m.GetWithdrawal(), domain.NoticeWithdrawalRequested, true), true
	case *walletv1.WithdrawalConfirmed:
		return withdrawalEvent(m.GetWithdrawal(), domain.NoticeWithdrawalCompleted, true), true
	case *walletv1.WithdrawalRejected:
		return withdrawalEvent(m.GetWithdrawal(), domain.NoticeWithdrawalRejected, true), true
	case *walletv1.WithdrawalCanceled:
		return withdrawalEvent(m.GetWithdrawal(), domain.NoticeWithdrawalCanceled, false), true
	case *walletv1.WithdrawalFailed:
		return withdrawalEvent(m.GetWithdrawal(), domain.NoticeWithdrawalFailed, true), true
	case *userv1.UserStatusChanged:
		return application.Event{UserID: m.GetUserId(), Type: domain.NoticeStatusChanged, Mail: true, Data: map[string]string{
			"from": m.GetFromStatus(), "to": m.GetToStatus(),
		}}, true
	case *marginv1.MarginLevelWarned:
		return application.Event{UserID: m.GetUserId(), Type: domain.NoticeMarginWarned, Mail: true, Data: map[string]string{
			"account_type": m.GetAccountType(), "symbol": m.GetSymbol(), "margin_level": m.GetMarginLevel(),
			"warn_level": m.GetWarnLevel(), "liquidation_level": m.GetLiquidationLevel(),
		}}, true
	case *marginv1.MarginLiquidationStarted:
		return application.Event{UserID: m.GetUserId(), Type: domain.NoticeMarginLiquidating, Mail: true, Data: map[string]string{
			"account_type": m.GetAccountType(), "symbol": m.GetSymbol(), "margin_level": m.GetMarginLevel(), "liquidation_id": m.GetLiquidationId(),
		}}, true
	case *marginv1.MarginLiquidationCompleted:
		return application.Event{UserID: m.GetUserId(), Type: domain.NoticeMarginLiquidated, Mail: true, Data: map[string]string{
			"account_type": m.GetAccountType(), "symbol": m.GetSymbol(), "liquidation_id": m.GetLiquidationId(),
			"repaid": amounts(m.GetRepaid()), "remaining": amounts(m.GetRemaining()), "fee": m.GetFee(), "insurance_covered": m.GetInsuranceCovered(),
		}}, true
	}
	return application.Event{}, false
}

// depositEvent describes a deposit; the amount of an unsupported token is
// left out, since its decimals are unknown.
func depositEvent(d *walletv1.Deposit, notice string, mail bool) application.Event {
	amount := d.GetAmount()
	if d.GetAsset() == "" {
		amount = ""
	}
	return application.Event{UserID: d.GetUserId(), Type: notice, Mail: mail, Data: map[string]string{
		"asset": d.GetAsset(), "amount": amount, "network": d.GetNetwork(), "tx": shortHash(d.GetTxHash()), "reason": d.GetReason(),
	}}
}

// withdrawalEvent describes a withdrawal; the address is shortened.
func withdrawalEvent(w *walletv1.Withdrawal, notice string, mail bool) application.Event {
	return application.Event{UserID: w.GetUserId(), Type: notice, Mail: mail, Data: map[string]string{
		"id": w.GetWithdrawalId(), "asset": w.GetAsset(), "amount": w.GetAmount(), "fee": w.GetFee(), "network": w.GetNetwork(),
		"address": shortHash(w.GetAddress()), "tx": shortHash(w.GetTxHash()), "reason": w.GetRejectReason(),
		"internal": strconv.FormatBool(w.GetInternal()),
	}}
}

// amounts lists amounts of assets: "100 USDT, 0.01 BTC".
func amounts(list []*marginv1.AssetAmount) string {
	parts := make([]string, 0, len(list))
	for _, a := range list {
		parts = append(parts, a.GetAmount()+" "+a.GetAsset())
	}
	return strings.Join(parts, ", ")
}

// shortHash keeps the ends of a transaction hash.
func shortHash(h string) string {
	if len(h) <= 18 {
		return h
	}
	return h[:10] + "…" + h[len(h)-6:]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
