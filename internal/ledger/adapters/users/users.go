// Package users asks user-service about eligibility.
package users

import (
	"context"

	userv1 "github.com/skill/exchange/api/gen/go/exchange/user/v1"
)

// Client implements ports.Eligibility.
type Client struct{ c userv1.UserServiceClient }

// New wraps a UserService client.
func New(c userv1.UserServiceClient) *Client { return &Client{c: c} }

// Check asks whether userID may use feature now.
func (c *Client) Check(ctx context.Context, userID, feature string) (bool, string, error) {
	resp, err := c.c.CheckEligibility(ctx, &userv1.CheckEligibilityRequest{UserId: userID, Feature: feature})
	if err != nil {
		return false, "", err
	}
	return resp.GetAllowed(), resp.GetReasonCode(), nil
}
