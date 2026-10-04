// Package users asks user-service about eligibility and accounts.
package users

import (
	"context"
	"time"

	userv1 "github.com/skill/exchange/api/gen/go/exchange/user/v1"
)

// Client implements ports.Eligibility and ports.Profiles.
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

// Created returns when the account was created.
func (c *Client) Created(ctx context.Context, userID string) (time.Time, error) {
	resp, err := c.c.GetUser(ctx, &userv1.GetUserRequest{UserId: userID})
	if err != nil {
		return time.Time{}, err
	}
	if t := resp.GetUser().GetCreatedAt(); t != nil {
		return t.AsTime(), nil
	}
	return time.Time{}, nil
}
