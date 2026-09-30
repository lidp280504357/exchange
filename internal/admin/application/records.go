package application

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Account statuses the user list filters by (appendix B).
var accountStatuses = []string{"", "ACTIVE", "RISK_REVIEW", "FROZEN", "CLOSED"}

// ListUsers returns a page of accounts, newest first, and the cursor of
// the next ("" on the last).
func (s *Service) ListUsers(ctx context.Context, p Principal, q ports.UserQuery) ([]ports.User, string, error) {
	if err := p.require(domain.PermUsersRead); err != nil {
		return nil, "", err
	}
	q.Status, q.Region = strings.ToUpper(q.Status), strings.ToUpper(strings.TrimSpace(q.Region))
	if !slices.Contains(accountStatuses, q.Status) {
		return nil, "", apperr.Invalid("status must be ACTIVE, RISK_REVIEW, FROZEN or CLOSED")
	}
	q.Limit = pageLimit(q.Limit)
	return s.Users.List(ctx, q)
}

func checkUserID(id string) error {
	if id == "" {
		return nil
	}
	if _, err := uuid.Parse(id); err != nil {
		return apperr.Invalid("user_id must be a user ID")
	}
	return nil
}

// OrderList returns a page of spot orders from the read model, newest
// first.
func (s *Service) OrderList(ctx context.Context, p Principal, q ports.OrderQuery) ([]ports.Order, string, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return nil, "", err
	}
	if err := checkUserID(q.UserID); err != nil {
		return nil, "", err
	}
	q.Symbol, q.Status, q.Side = strings.ToUpper(q.Symbol), strings.ToUpper(q.Status), strings.ToUpper(q.Side)
	q.Limit = pageLimit(q.Limit)
	return s.Records.Orders(ctx, q)
}

// TradeList returns a page of spot trades from the read model, newest
// first.
func (s *Service) TradeList(ctx context.Context, p Principal, q ports.TradeQuery) ([]ports.Trade, string, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return nil, "", err
	}
	if err := checkUserID(q.UserID); err != nil {
		return nil, "", err
	}
	q.Symbol = strings.ToUpper(q.Symbol)
	q.Limit = pageLimit(q.Limit)
	return s.Records.Trades(ctx, q)
}

// DepositList returns a page of deposits from the read model, newest
// first.
func (s *Service) DepositList(ctx context.Context, p Principal, q ports.DepositQuery) ([]ports.Deposit, string, error) {
	if err := p.require(domain.PermWithdrawalsRead); err != nil {
		return nil, "", err
	}
	if err := checkUserID(q.UserID); err != nil {
		return nil, "", err
	}
	q.Asset, q.Network, q.Status = strings.ToUpper(q.Asset), strings.ToUpper(q.Network), strings.ToUpper(q.Status)
	q.Limit = pageLimit(q.Limit)
	return s.Records.Deposits(ctx, q)
}

// Dashboard is the overview page.
type Dashboard struct {
	Users    ports.UserStats
	Activity ports.Activity
	// Feed is nil when market-data-service could not be asked.
	Feed *ports.FeedStatus
	Days []string
	// Partial lists the parts that could not be read (users, activity,
	// feed); the rest is still shown.
	Partial []string
}

// Dashboard gathers the overview for the last days (default 7, at most
// 90): accounts, the read models' trading and wallet figures, and the
// reference feed. A part that fails is left out and named in Partial.
func (s *Service) Dashboard(ctx context.Context, p Principal, days int) (Dashboard, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return Dashboard{}, err
	}
	days = reportDays(days)
	now := s.Now().UTC()
	out := Dashboard{}
	for i := days - 1; i >= 0; i-- {
		out.Days = append(out.Days, now.AddDate(0, 0, -i).Format(time.DateOnly))
	}
	var err error
	if out.Users, err = s.Users.Stats(ctx, now.Add(-24*time.Hour), days); err != nil {
		s.Log.WarnContext(ctx, "dashboard: user stats unavailable", "error", err)
		out.Partial = append(out.Partial, "users")
	}
	if out.Activity, err = s.Records.Activity(ctx, days); err != nil {
		s.Log.WarnContext(ctx, "dashboard: activity unavailable", "error", err)
		out.Partial = append(out.Partial, "activity")
	}
	if s.Market != nil {
		if feed, err := s.Market.Feed(ctx); err != nil {
			s.Log.WarnContext(ctx, "dashboard: feed status unavailable", "error", err)
			out.Partial = append(out.Partial, "feed")
		} else {
			out.Feed = &feed
		}
	}
	return out, nil
}
