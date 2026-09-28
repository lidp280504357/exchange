// Package recipients looks users up in user-service (language, time zone,
// anti-phishing code) and auth-service (verified contacts).
package recipients

import (
	"context"

	authv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/auth/v1"
	userv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/user/v1"
	"github.com/lidp280504357/exchange/internal/notification/domain"
	"github.com/lidp280504357/exchange/internal/notification/ports"
)

// Client implements ports.Recipients.
type Client struct {
	users userv1.UserServiceClient
	auth  authv1.AuthServiceClient
}

// New returns the lookup over the two services.
func New(users userv1.UserServiceClient, auth authv1.AuthServiceClient) *Client {
	return &Client{users: users, auth: auth}
}

// Recipient returns the user's message preferences.
func (c *Client) Recipient(ctx context.Context, userID string) (ports.Recipient, error) {
	resp, err := c.users.GetUser(ctx, &userv1.GetUserRequest{UserId: userID})
	if err != nil {
		return ports.Recipient{}, err
	}
	u := resp.GetUser()
	return ports.Recipient{Language: u.GetLanguage(), Timezone: u.GetTimezone(), AntiPhishingCode: u.GetAntiPhishingCode()}, nil
}

// Contacts returns the user's verified addresses.
func (c *Client) Contacts(ctx context.Context, userID string) ([]ports.Contact, error) {
	resp, err := c.auth.GetContacts(ctx, &authv1.GetContactsRequest{UserId: userID})
	if err != nil {
		return nil, err
	}
	out := make([]ports.Contact, 0, len(resp.GetContacts()))
	for _, ct := range resp.GetContacts() {
		out = append(out, ports.Contact{Channel: domain.Channel(ct.GetChannel()), Value: ct.GetValue()})
	}
	return out, nil
}
