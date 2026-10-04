package auth

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/skill/exchange/api/gen/go/exchange/auth/v1"
)

// stepUps answers ConsumeStepUp with resp; the rest of the service is
// not called.
type stepUps struct {
	authv1.AuthServiceClient
	resp *authv1.ConsumeStepUpResponse
}

func (s stepUps) ConsumeStepUp(context.Context, *authv1.ConsumeStepUpRequest, ...grpc.CallOption) (*authv1.ConsumeStepUpResponse, error) {
	return s.resp, nil
}

// TestConsumeCarriesTheSecurityChanges checks the step-up's security
// context reaches the withdrawal's risk input: the authenticator's removal
// above all, which holds withdrawals for review for a day (C5.5 ⑤,
// review ⑭).
func TestConsumeCarriesTheSecurityChanges(t *testing.T) {
	seen, rebound := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC), time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	reset, removed := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC), time.Date(2026, 10, 4, 11, 0, 0, 0, time.UTC)
	c := New(stepUps{resp: &authv1.ConsumeStepUpResponse{Channel: "EMAIL", Security: &authv1.SecurityContext{
		Identities: 2, TotpEnabled: false, DeviceId: "d1", DeviceFirstSeenAt: timestamppb.New(seen), IdentityChangedAt: timestamppb.New(rebound),
		PasswordChangedAt: timestamppb.New(reset), TotpChangedAt: timestamppb.New(removed),
	}}})
	su, err := c.Consume(context.Background(), "u1", "token")
	if err != nil || su.Channel != "EMAIL" || su.DeviceID != "d1" || su.Identities != 2 || su.TOTPEnabled || !su.DeviceFirstSeen.Equal(seen) ||
		!su.IdentityChanged.Equal(rebound) || !su.PasswordChanged.Equal(reset) || !su.TOTPChanged.Equal(removed) {
		t.Fatalf("security context %+v %v", su, err)
	}

	// Never changed: zero times, not the epoch.
	c = New(stepUps{resp: &authv1.ConsumeStepUpResponse{Channel: "TOTP", Security: &authv1.SecurityContext{Identities: 1, TotpEnabled: true}}})
	su, err = c.Consume(context.Background(), "u1", "token")
	if err != nil || !su.TOTPEnabled || !su.TOTPChanged.IsZero() || !su.PasswordChanged.IsZero() || !su.DeviceFirstSeen.IsZero() {
		t.Fatalf("nothing changed %+v %v", su, err)
	}
}
