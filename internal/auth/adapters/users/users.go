// Package users talks to user-service over gRPC.
package users

import (
	"context"

	userv1 "github.com/skill/exchange/api/gen/go/exchange/user/v1"
	"github.com/skill/exchange/internal/auth/ports"
)

// Client implements ports.Users.
type Client struct{ c userv1.UserServiceClient }

// New wraps a UserService client.
func New(c userv1.UserServiceClient) *Client { return &Client{c: c} }

// Create creates a profile; user-service ignores a repeated user ID.
func (u *Client) Create(ctx context.Context, n ports.NewUser) error {
	_, err := u.c.CreateUser(ctx, &userv1.CreateUserRequest{
		UserId: n.ID, Region: n.Region, Language: n.Language, Timezone: n.Timezone,
		TermsVersion: n.TermsVersion, RiskDisclosureVersion: n.RiskVersion,
	})
	return err
}

// FindUsername returns the account of a username (B167).
func (u *Client) FindUsername(ctx context.Context, name string) (string, error) {
	resp, err := u.c.FindUsername(ctx, &userv1.FindUsernameRequest{Username: name})
	if err != nil {
		return "", err
	}
	return resp.GetUserId(), nil
}

// Get returns what login needs to know about a user.
func (u *Client) Get(ctx context.Context, userID string) (ports.UserInfo, error) {
	resp, err := u.c.GetUser(ctx, &userv1.GetUserRequest{UserId: userID})
	if err != nil {
		return ports.UserInfo{}, err
	}
	usr := resp.GetUser()
	return ports.UserInfo{ID: usr.GetId(), Status: usr.GetStatus(), Region: usr.GetRegion(), Language: usr.GetLanguage()}, nil
}
