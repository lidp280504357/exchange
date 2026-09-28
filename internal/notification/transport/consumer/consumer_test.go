package consumer

import (
	"testing"

	"google.golang.org/protobuf/proto"

	authv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/auth/v1"
	userv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/user/v1"
	walletv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/wallet/v1"
	"github.com/lidp280504357/exchange/internal/notification/domain"
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
		{&walletv1.DepositCredited{Deposit: &walletv1.Deposit{UserId: "u", Asset: "ETH"}}, domain.NoticeDepositCredited, false},
		{&walletv1.DepositCredited{Deposit: &walletv1.Deposit{UserId: "u", Unclaimed: true}}, domain.NoticeDepositUnclaimed, true},
		{&walletv1.DepositRejected{Deposit: &walletv1.Deposit{UserId: "u"}}, domain.NoticeDepositUnclaimed, true},
		{&walletv1.DepositDetected{Deposit: &walletv1.Deposit{UserId: "u"}}, "", false},
		{&authv1.OtpRequested{}, "", false},
	} {
		e, ok := toEvent(tc.msg)
		if ok != (tc.want != "") || e.Type != tc.want || e.Mail != tc.mail || (ok && e.UserID != "u") {
			t.Errorf("%T: %+v %v", tc.msg, e, ok)
		}
	}
}
