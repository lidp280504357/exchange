package application

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
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
	var err error
	if q.Q, err = searchText(q.Q); err != nil {
		return nil, "", err
	}
	if n := utf8.RuneCountInString(q.Q); q.Q != "" && (n < minKeyword || n > maxKeyword) {
		return nil, "", apperr.Invalid("a keyword has 2 to 64 characters")
	}
	if q.Kinds, err = UserKinds(q.Kinds); err != nil {
		return nil, "", err
	}
	q.UserIDs = nil
	if q.Q != "" {
		// A keyword (A93): user-service matches the usernames, and the
		// accounts whose email address or phone number contains it, which
		// auth-service finds, go along (B167).
		if q.UserIDs, err = s.Users.Search(ctx, q.Q, searchMatches); err != nil {
			return nil, "", err
		}
	}
	q.Limit = pageLimit(q.Limit)
	list, next, err := s.Users.List(ctx, q)
	if err != nil {
		return nil, "", err
	}
	list, err = s.withTags(ctx, list)
	return list, next, err
}

// The console's search box (A93): an input of at most maxSearch characters
// (an email address's longest) without control characters; as a list's
// keyword, minKeyword to maxKeyword characters (auth-service's and
// user-service's bounds, B170), and the accounts whose email address or
// phone number match it, at most searchMatches (user-service's bound on
// UserIDs).
const (
	maxSearch     = 254
	minKeyword    = 2
	maxKeyword    = 64
	searchMatches = 500
)

// searchText is a search box's input made ready: trimmed, refused (the
// console's 「输入有误」) only when no account could match it - too long, or
// with control characters.
func searchText(q string) (string, error) {
	q = strings.TrimSpace(q)
	if utf8.RuneCountInString(q) > maxSearch || strings.IndexFunc(q, unicode.IsControl) >= 0 {
		return "", apperr.Invalid("the search is too long or has characters no account has")
	}
	return q, nil
}

// The account kinds (L0), and ALL for every one of them.
const (
	KindHuman  = "HUMAN"
	KindBot    = "BOT"
	KindTest   = "TEST"
	KindSystem = "SYSTEM"
	KindAll    = "ALL"
)

// UserKinds reads a user-dimension list's kinds (L1): none, the humans
// only (the console's default); ALL among them, every kind (nil); else
// those named, once each, whatever their case.
func UserKinds(kinds []string) ([]string, error) {
	if len(kinds) == 0 {
		return []string{KindHuman}, nil
	}
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		k = strings.ToUpper(strings.TrimSpace(k))
		switch k {
		case KindAll:
			return nil, nil
		case KindHuman, KindBot, KindTest, KindSystem:
			if !slices.Contains(out, k) {
				out = append(out, k)
			}
		default:
			return nil, apperr.Invalid("kind must be HUMAN, BOT, TEST, SYSTEM or ALL").WithDetail("kind", k)
		}
	}
	return out, nil
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
	bots, err := s.listBots(ctx, q.Accounts)
	if err != nil {
		return nil, "", err
	}
	q.Bots = slices.Collect(maps.Keys(bots))
	// The accounts' kinds (L1), the humans by default - but for the
	// simulated market's accounts filter (accounts), which says whose.
	if q.Accounts == "" {
		if q.ByKind, err = s.kindFilter(ctx, q.Kinds, q.UserID, false); err != nil {
			return nil, "", err
		}
	}
	list, next, err := s.Records.Orders(ctx, q)
	for i := range list {
		list[i].Bot = bots[list[i].UserID]
	}
	return list, next, err
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
	bots, err := s.listBots(ctx, q.Accounts)
	if err != nil {
		return nil, "", err
	}
	q.Bots = slices.Collect(maps.Keys(bots))
	// As the orders (L1): a trade is kept when one of its sides is of the
	// kinds; HOUSE (SYSTEM) is the other side of nearly every one.
	if q.Accounts == "" {
		if q.ByKind, err = s.kindFilter(ctx, q.Kinds, q.UserID, false); err != nil {
			return nil, "", err
		}
	}
	list, next, err := s.Records.Trades(ctx, q)
	for i := range list {
		list[i].BuyerBot, list[i].SellerBot = bots[list[i].BuyerUserID], bots[list[i].SellerUserID]
	}
	return list, next, err
}

// botMarkWait bounds the wait for the bots when a list only marks them.
const botMarkWait = 2 * time.Second

// listBots returns the simulated market's bots for a list of orders or
// trades: required to keep them (accounts bots) or leave them out
// (users); otherwise only to mark them, and none while market-sim does not
// answer.
func (s *Service) listBots(ctx context.Context, accounts string) (map[string]bool, error) {
	if accounts != "" && accounts != ports.AccountsBots && accounts != ports.AccountsUsers {
		return nil, apperr.Invalid("accounts must be bots or users")
	}
	if s.SimBots == nil {
		if accounts != "" {
			return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "the simulated market's bots are unknown here")
		}
		return nil, nil
	}
	wait := ctx
	if accounts == "" {
		var cancel context.CancelFunc
		wait, cancel = context.WithTimeout(ctx, botMarkWait)
		defer cancel()
	}
	list, err := s.SimBots.BotUsers(wait)
	if err != nil {
		if accounts != "" {
			return nil, err
		}
		s.Log.DebugContext(ctx, "lists: the bots are unknown", "error", err)
		return nil, nil
	}
	out := make(map[string]bool, len(list))
	for _, id := range list {
		out[id] = true
	}
	return out, nil
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
	var err error
	if q.ByKind, err = s.kindFilter(ctx, q.Kinds, q.UserID, false); err != nil {
		return nil, "", err
	}
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
	// feed, and kinds: the figures are every account's); the rest is
	// still shown.
	Partial []string
}

// Dashboard gathers the overview for the last days (default 7, at most
// 90): accounts, the read models' trading and wallet figures, and the
// reference feed. A part that fails is left out and named in Partial. The
// figures are the humans' (L1), the other kinds counted apart.
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
	// Every kind's counts (by_kind) and the humans' accounts per day (the
	// trend, A108): user-service counts only the kinds asked for (B185).
	if out.Users, err = s.Users.Stats(ctx, now.Add(-24*time.Hour), days, nil); err != nil {
		s.Log.WarnContext(ctx, "dashboard: user stats unavailable", "error", err)
		out.Partial = append(out.Partial, "users")
	} else if humans, err := s.Users.Stats(ctx, now.Add(-24*time.Hour), days, []string{KindHuman}); err != nil {
		s.Log.WarnContext(ctx, "dashboard: the humans' stats unavailable", "error", err)
		out.Partial = append(out.Partial, "users")
	} else {
		out.Users.Days = humans.Days
	}
	kinds, err := s.activityKinds(ctx)
	if err != nil {
		s.Log.WarnContext(ctx, "dashboard: the accounts' kinds unavailable", "error", err)
		out.Partial = append(out.Partial, "kinds")
	}
	if out.Activity, err = s.Records.Activity(ctx, days, kinds); err != nil {
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
