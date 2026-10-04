// Package auth redeems step-up tokens through auth-service.
package auth

import (
	"context"

	authv1 "github.com/skill/exchange/api/gen/go/exchange/auth/v1"
	"github.com/skill/exchange/internal/wallet/ports"
)

// Client implements ports.StepUps and ports.Securities.
type Client struct{ c authv1.AuthServiceClient }

// New wraps an AuthService client.
func New(c authv1.AuthServiceClient) *Client { return &Client{c: c} }

// Consume redeems a step-up token and returns the user's security context.
func (c *Client) Consume(ctx context.Context, userID, token string) (ports.StepUp, error) {
	resp, err := c.c.ConsumeStepUp(ctx, &authv1.ConsumeStepUpRequest{UserId: userID, Token: token})
	if err != nil {
		return ports.StepUp{}, err
	}
	su := stepUp(resp.GetSecurity())
	su.Channel = resp.GetChannel()
	return su, nil
}

// Security reads the user's security context without a step-up.
func (c *Client) Security(ctx context.Context, userID string) (ports.StepUp, error) {
	resp, err := c.c.GetSecurityContext(ctx, &authv1.GetSecurityContextRequest{UserId: userID})
	if err != nil {
		return ports.StepUp{}, err
	}
	return stepUp(resp.GetSecurity()), nil
}

func stepUp(sec *authv1.SecurityContext) ports.StepUp {
	su := ports.StepUp{DeviceID: sec.GetDeviceId(), Identities: int(sec.GetIdentities()), TOTPEnabled: sec.GetTotpEnabled()}
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
	if t := sec.GetTotpActivatedAt(); t != nil {
		su.TOTPActivated = t.AsTime()
	}
	return su
}
