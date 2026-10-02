// Package auth redeems step-up tokens through auth-service.
package auth

import (
	"context"

	authv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/auth/v1"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
)

// Client implements ports.StepUps.
type Client struct{ c authv1.AuthServiceClient }

// New wraps an AuthService client.
func New(c authv1.AuthServiceClient) *Client { return &Client{c: c} }

// Consume redeems a step-up token and returns the user's security context.
func (c *Client) Consume(ctx context.Context, userID, token string) (ports.StepUp, error) {
	resp, err := c.c.ConsumeStepUp(ctx, &authv1.ConsumeStepUpRequest{UserId: userID, Token: token})
	if err != nil {
		return ports.StepUp{}, err
	}
	sec := resp.GetSecurity()
	su := ports.StepUp{
		Channel: resp.GetChannel(), DeviceID: sec.GetDeviceId(), Identities: int(sec.GetIdentities()), TOTPEnabled: sec.GetTotpEnabled(),
	}
	if t := sec.GetDeviceFirstSeenAt(); t != nil {
		su.DeviceFirstSeen = t.AsTime()
	}
	if t := sec.GetIdentityChangedAt(); t != nil {
		su.IdentityChanged = t.AsTime()
	}
	if t := sec.GetPasswordChangedAt(); t != nil {
		su.PasswordChanged = t.AsTime()
	}
	if t := sec.GetTotpChangedAt(); t != nil {
		su.TOTPChanged = t.AsTime()
	}
	return su, nil
}
