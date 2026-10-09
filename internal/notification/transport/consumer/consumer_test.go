package consumer

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	authv1 "github.com/skill/exchange/api/gen/go/exchange/auth/v1"
	derivv1 "github.com/skill/exchange/api/gen/go/exchange/derivatives/v1"
	marginv1 "github.com/skill/exchange/api/gen/go/exchange/margin/v1"
	userv1 "github.com/skill/exchange/api/gen/go/exchange/user/v1"
	walletv1 "github.com/skill/exchange/api/gen/go/exchange/wallet/v1"
	"github.com/skill/exchange/internal/notification/application"
	"github.com/skill/exchange/internal/notification/domain"
)

func TestToEvent(t *testing.T) {
	for _, tc := range []struct {
		msg  proto.Message
		want string
		mail bool
	}{
		{&authv1.UserRegistered{UserId: "u"}, domain.NoticeWelcome, false},
		{&authv1.LoginSucceeded{UserId: "u", NewDevice: true, Method: "PASSWORD"}, domain.NoticeNewDeviceLogin, true},
		{&authv1.LoginSucceeded{UserId: "u", NewDevice: false, Method: "PASSWORD"}, "", false},
		{&authv1.LoginSucceeded{UserId: "u", NewDevice: true, Method: "REGISTER"}, "", false},
		{&authv1.IdentityBound{UserId: "u", Channel: "SMS"}, domain.NoticeIdentityChanged, true},
		{&authv1.IdentityRebound{UserId: "u", Channel: "EMAIL"}, domain.NoticeIdentityChanged, true},
		{&authv1.PasswordChanged{UserId: "u", ViaReset: true}, domain.NoticePasswordChanged, true},
		{&authv1.LoginFailed{UserId: "u", Locked: true}, domain.NoticeAccountLocked, true},
		{&authv1.LoginFailed{UserId: "u", Locked: false}, "", false},
		{&userv1.UserStatusChanged{UserId: "u", ToStatus: "FROZEN"}, domain.NoticeStatusChanged, true},
		{&authv1.TotpEnabled{UserId: "u"}, domain.NoticeTOTPChanged, true},
		{&authv1.TotpDisabled{UserId: "u"}, domain.NoticeTOTPChanged, true},
		{&userv1.ProfileUpdated{UserId: "u"}, "", false},
		{&userv1.ProfileReset{UserId: "u", Field: "USERNAME", Username: "user_k3x9q2m7"}, domain.NoticeUsernameReset, false},
		{&userv1.ProfileReset{UserId: "u", Field: "AVATAR"}, domain.NoticeAvatarReset, false},
		{&userv1.ProfileReset{UserId: "u", Field: "BIO"}, "", false},
		{&walletv1.DepositCredited{Deposit: &walletv1.Deposit{UserId: "u", Asset: "ETH"}}, domain.NoticeDepositCredited, false},
		{&walletv1.DepositCredited{Deposit: &walletv1.Deposit{UserId: "u", Unclaimed: true}}, domain.NoticeDepositUnclaimed, true},
		{&walletv1.DepositRejected{Deposit: &walletv1.Deposit{UserId: "u"}}, domain.NoticeDepositUnclaimed, true},
		{&walletv1.DepositDetected{Deposit: &walletv1.Deposit{UserId: "u"}}, "", false},
		{&walletv1.WithdrawalRequested{Withdrawal: &walletv1.Withdrawal{UserId: "u"}}, domain.NoticeWithdrawalRequested, true},
		{&walletv1.WithdrawalConfirmed{Withdrawal: &walletv1.Withdrawal{UserId: "u"}}, domain.NoticeWithdrawalCompleted, true},
		{&walletv1.WithdrawalCanceled{Withdrawal: &walletv1.Withdrawal{UserId: "u"}}, domain.NoticeWithdrawalCanceled, false},
		{&walletv1.WithdrawalBroadcast{Withdrawal: &walletv1.Withdrawal{UserId: "u"}}, "", false},
		{&authv1.OtpRequested{}, "", false},
		{&marginv1.MarginLevelWarned{UserId: "u", AccountType: "MARGIN_CROSS", MarginLevel: "1.25"}, domain.NoticeMarginWarned, true},
		{&marginv1.MarginLiquidationStarted{UserId: "u", AccountType: "MARGIN_CROSS"}, domain.NoticeMarginLiquidating, true},
		{&marginv1.MarginLiquidationCompleted{UserId: "u", Repaid: []*marginv1.AssetAmount{{Asset: "USDT", Amount: "100"}}}, domain.NoticeMarginLiquidated, true},
		{&marginv1.MarginBorrowed{UserId: "u"}, "", false},
		{&derivv1.LiquidationWarning{UserId: "u", Cross: true, SettleAsset: "BTC"}, domain.NoticeContractWarned, true},
		{&derivv1.LiquidationStarted{Position: &derivv1.Position{UserId: "u", Symbol: "BTC-USD-PERP"}}, domain.NoticeContractLiquidating, true},
		{&derivv1.AdlExecuted{UserId: "u", Symbol: "BTC-USDT-PERP"}, domain.NoticeContractDeleveraged, true},
		{&derivv1.CrossLiquidationCompleted{UserId: "u", SettleAsset: "USDT", ClearanceFee: "897.98"}, domain.NoticeContractLiquidated, true},
		{&derivv1.LiquidationFilled{UserId: "u"}, "", false},
	} {
		e, ok := toEvent(tc.msg)
		if ok != (tc.want != "") || e.Type != tc.want || e.Mail != tc.mail || (ok && e.UserID != "u") {
			t.Errorf("%T: %+v %v", tc.msg, e, ok)
		}
	}
	e, _ := toEvent(&marginv1.MarginLiquidationCompleted{UserId: "u", Repaid: []*marginv1.AssetAmount{{Asset: "USDT", Amount: "100"}, {Asset: "BTC", Amount: "0.01"}}})
	if e.Data["repaid"] != "100 USDT, 0.01 BTC" || e.Data["remaining"] != "" {
		t.Fatalf("liquidation data: %v", e.Data)
	}
	// A reset names the new username, never the operator or the reason
	// (they stay in the audit log).
	e, _ = toEvent(&userv1.ProfileReset{UserId: "u", Field: "USERNAME", Username: "user_k3x9q2m7", Actor: "admin:1", Reason: "abusive"})
	if len(e.Data) != 1 || e.Data["username"] != "user_k3x9q2m7" {
		t.Fatalf("username reset data: %v", e.Data)
	}
	// A one-way position's direction is its quantity's sign; the notice
	// shows the size without it.
	e, _ = toEvent(&derivv1.LiquidationStarted{
		Position:  &derivv1.Position{UserId: "u", Symbol: "BTC-USD-PERP", PositionSide: "BOTH", Quantity: "-3", SettleAsset: "BTC"},
		MarkPrice: "85000.1",
	})
	if e.Data["side"] != "SHORT" || e.Data["quantity"] != "3" || e.Data["settle_asset"] != "BTC" || e.Data["mark_price"] != "85000.1" {
		t.Fatalf("liquidation started: %v", e.Data)
	}
	e, _ = toEvent(&derivv1.LiquidationStarted{Position: &derivv1.Position{UserId: "u", PositionSide: "LONG", Quantity: "0.5"}})
	if e.Data["side"] != "LONG" {
		t.Fatalf("a hedge-mode long: %v", e.Data)
	}
	// The unit follows the face value the event carries.
	e, _ = toEvent(&derivv1.AdlExecuted{UserId: "u", Symbol: "BTC-USD-PERP", PositionSide: "SHORT", Quantity: "4", ContractSize: "100", SettleAsset: "BTC"})
	if e.Data["contract_size"] != "100" || e.Data["side"] != "SHORT" || e.Data["quantity"] != "4" {
		t.Fatalf("adl: %v", e.Data)
	}
	// One-way mode's BOTH: the direction the event names since C49.
	e, _ = toEvent(&derivv1.AdlExecuted{UserId: "u", Symbol: "BTC-USD-PERP", PositionSide: "BOTH", Direction: "LONG", Quantity: "4", ContractSize: "100"})
	if e.Data["side"] != "LONG" {
		t.Fatalf("adl in one-way mode: %v", e.Data)
	}
	e, _ = toEvent(&derivv1.LiquidationWarning{UserId: "u", Symbol: "ETH-USDT-PERP", PositionSide: "BOTH", Direction: "SHORT"})
	if e.Data["side"] != "SHORT" {
		t.Fatalf("warning in one-way mode: %v", e.Data)
	}
}

func TestMergeKey(t *testing.T) {
	at := time.Date(2026, 10, 7, 5, 40, 12, 0, time.UTC)
	cross := func(symbol string, at time.Time) application.Event {
		e, _ := toEvent(&derivv1.LiquidationStarted{Cross: true, Position: &derivv1.Position{UserId: "u", Symbol: symbol, SettleAsset: "BTC"}})
		e.At = at
		return e
	}
	// One cross takeover: every position's event has the account's key,
	// a UUID as the inbox wants (review R10, B138).
	a, b := mergeKey(cross("BTC-USD-PERP", at)), mergeKey(cross("BTC-USD-PERP", at.Add(300*time.Millisecond)))
	if a == "" || a != b {
		t.Fatalf("one takeover, two keys: %q %q", a, b)
	}
	if _, err := uuid.Parse(a); err != nil {
		t.Fatalf("the merge key %q is no UUID: %v", a, err)
	}
	// An account-wide notice names no single position's side, size or mark.
	if d := cross("BTC-USD-PERP", at).Data; d["side"] != "" || d["quantity"] != "" || d["mark_price"] != "" || d["symbol"] != "BTC-USD-PERP" {
		t.Fatalf("cross data: %v", d)
	}
	if mergeKey(cross("BTC-USD-PERP", at.Add(time.Minute))) == a {
		t.Fatal("a takeover a minute later merged into the first")
	}
	isolated, _ := toEvent(&derivv1.LiquidationStarted{Position: &derivv1.Position{UserId: "u", Symbol: "BTC-USD-PERP"}})
	if mergeKey(isolated) != "" {
		t.Fatal("an isolated takeover merged")
	}
}
