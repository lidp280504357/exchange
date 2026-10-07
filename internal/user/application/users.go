// Package application holds user-service's use cases.
package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	userv1 "github.com/skill/exchange/api/gen/go/exchange/user/v1"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/pagecursor"
	"github.com/skill/exchange/internal/user/domain"
	"github.com/skill/exchange/internal/user/ports"
)

// Service manages profiles, account status and eligibility.
type Service struct {
	Store   ports.Store
	Flags   ports.Flags
	StepUps ports.StepUps
	// Avatars keeps the avatars' files (nil: uploads are refused).
	Avatars ports.Avatars
	Now     func() time.Time
}

// CreateInput is a CreateUser call from auth-service.
type CreateInput struct {
	UserID       string
	Region       string
	Language     string
	Timezone     string
	TermsVersion string
	RiskVersion  string
}

// Create creates the profile of a newly registered user; repeating the
// call returns the existing profile.
func (s *Service) Create(ctx context.Context, in CreateInput) (domain.User, error) {
	u, err := domain.NewUser(in.UserID, in.Region, in.Language, in.Timezone)
	if err != nil {
		return domain.User{}, err
	}
	if in.TermsVersion == "" || in.RiskVersion == "" {
		return domain.User{}, apperr.Invalid("the accepted terms and risk disclosure versions are required")
	}
	// A drawn username that is taken is drawn again, in a new transaction
	// (the clash ends the one it happened in).
	var out domain.User
	for range drawAttempts {
		u.Username = domain.DrawUsername()
		err = s.Store.Tx(ctx, func(r ports.Repos) error {
			if _, err := r.Users().Create(ctx, u, []domain.Consent{
				{Document: domain.DocumentTerms, Version: in.TermsVersion},
				{Document: domain.DocumentRiskDisclosure, Version: in.RiskVersion},
			}); err != nil {
				return err
			}
			out, err = r.Users().Get(ctx, u.ID)
			return err
		})
		if !errors.Is(err, domain.ErrUsernameTaken) {
			break
		}
	}
	return out, err
}

// drawAttempts is how many usernames a sign-up or reset draws before it
// gives up (36^8 of them: a clash is already rare).
const drawAttempts = 5

// Get returns a profile.
func (s *Service) Get(ctx context.Context, id string) (domain.User, error) {
	return s.Store.Read().Users().Get(ctx, id)
}

// UpdateProfile applies a patch; changing the anti-phishing code needs a
// step-up token.
func (s *Service) UpdateProfile(ctx context.Context, userID string, p domain.ProfilePatch, stepUp string) (domain.User, error) {
	// Validate before spending the step-up token.
	cur, err := s.Get(ctx, userID)
	if err != nil {
		return domain.User{}, err
	}
	if _, err := p.Apply(&cur); err != nil {
		return domain.User{}, err
	}
	if p.SensitiveChange() {
		if err := s.StepUps.Consume(ctx, userID, stepUp); err != nil {
			return domain.User{}, err
		}
	}
	var out domain.User
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		u, err := r.Users().GetForUpdate(ctx, userID)
		if err != nil {
			return err
		}
		changed, err := p.Apply(&u)
		if err != nil {
			return err
		}
		if len(changed) == 0 {
			out = u
			return nil
		}
		if out, err = r.Users().Update(ctx, u); err != nil {
			return err
		}
		return r.Emit(ctx, event.TopicUser, &userv1.ProfileUpdated{UserId: userID, Fields: changed}, "user", userID)
	})
	return out, err
}

// ChangeStatus moves an account along the status machine, recording the
// change and emitting UserStatusChanged plus an audit event (§5.4).
func (s *Service) ChangeStatus(ctx context.Context, userID, to, reason, actor, note string) (domain.StatusChange, error) {
	c, err := domain.NewStatusChange(userID, to, reason, actor, note)
	if err != nil {
		return domain.StatusChange{}, err
	}
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		u, err := r.Users().GetForUpdate(ctx, userID)
		if err != nil {
			return err
		}
		if err := domain.CheckTransition(u.Status, c.To); err != nil {
			return err
		}
		return s.applyStatus(ctx, r, u, &c)
	})
	return c, err
}

// Consumer names user-service in inboxes and consumer groups.
const Consumer = "user-service"

// ReasonRiskRule is the reason code of reviews that risk rules start.
const ReasonRiskRule = "RISK_RULE"

// OnRiskAction carries out a review that risk-service enforces (§5.13):
// an ACTIVE account moves to RISK_REVIEW; accounts in any other status
// keep it. Each event is handled once, so a redelivery cannot reopen a
// review an operator has closed.
func (s *Service) OnRiskAction(ctx context.Context, eventID, userID string, rules []string) error {
	_, err := s.Store.Once(ctx, Consumer, eventID, func(r ports.Repos) error {
		u, err := r.Users().GetForUpdate(ctx, userID)
		if errors.Is(err, domain.ErrUserNotFound) {
			return nil
		}
		if err != nil || u.Status != domain.StatusActive {
			return err
		}
		c := domain.StatusChange{
			UserID: userID, To: domain.StatusRiskReview, Reason: ReasonRiskRule, Actor: "risk-service",
			Note: "rules: " + strings.Join(rules, ", "),
		}
		return s.applyStatus(ctx, r, u, &c)
	})
	return err
}

// applyStatus stores a checked transition of u, with its history row, a
// UserStatusChanged event and an audit event.
func (s *Service) applyStatus(ctx context.Context, r ports.Repos, u domain.User, c *domain.StatusChange) error {
	c.From, c.At = u.Status, s.Now()
	u.Status = c.To
	if _, err := r.Users().Update(ctx, u); err != nil {
		return err
	}
	if err := r.Users().AddStatusChange(ctx, *c); err != nil {
		return err
	}
	if err := r.Emit(ctx, event.TopicUser, &userv1.UserStatusChanged{
		UserId: u.ID, FromStatus: c.From, ToStatus: c.To, ReasonCode: c.Reason, Actor: c.Actor,
	}, "user", u.ID); err != nil {
		return err
	}
	details, _ := json.Marshal(map[string]string{"from": c.From, "to": c.To, "note": c.Note})
	return r.Emit(ctx, event.TopicAudit, &auditv1.AdminActionPerformed{
		Target: "user:" + u.ID, Action: "user.status_changed", Actor: c.Actor, Reason: c.Reason, Details: string(details),
	}, "actor", c.Actor)
}

// StatusHistory lists the recent status changes of a user.
func (s *Service) StatusHistory(ctx context.Context, userID string) ([]domain.StatusChange, error) {
	return s.Store.Read().Users().StatusHistory(ctx, userID, 50)
}

// History returns a user's recent status changes and accepted document
// versions (the admin console's user page).
func (s *Service) History(ctx context.Context, userID string) ([]domain.StatusChange, []domain.Consent, error) {
	if _, err := s.Get(ctx, userID); err != nil {
		return nil, nil, err
	}
	r := s.Store.Read().Users()
	changes, err := r.StatusHistory(ctx, userID, 50)
	if err != nil {
		return nil, nil, err
	}
	consents, err := r.Consents(ctx, userID)
	return changes, consents, err
}

// CheckEligibility decides whether a user may use a feature now.
func (s *Service) CheckEligibility(ctx context.Context, userID, feature, asset, symbol string) (bool, string, error) {
	feature, err := domain.ParseFeature(feature)
	if err != nil {
		return false, "", err
	}
	u, err := s.Get(ctx, userID)
	if err != nil {
		return false, "", err
	}
	allowed, reason := domain.Eligibility(u, feature, asset, symbol, s.Flags.Get)
	return allowed, reason, nil
}

// Favorites returns the user's favorite markets and when they were last
// set (zero when never).
func (s *Service) Favorites(ctx context.Context, userID string) ([]string, time.Time, error) {
	if _, err := s.Store.Read().Users().Get(ctx, userID); err != nil {
		return nil, time.Time{}, err
	}
	return s.Store.Read().Favorites().Get(ctx, userID)
}

// SetFavorites replaces the user's favorite markets (checked by
// domain.Favorites) and returns the stored list.
func (s *Service) SetFavorites(ctx context.Context, userID string, symbols []string) ([]string, time.Time, error) {
	list, err := domain.Favorites(symbols)
	if err != nil {
		return nil, time.Time{}, err
	}
	var at time.Time
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		if _, err := r.Users().Get(ctx, userID); err != nil {
			return err
		}
		at, err = r.Favorites().Set(ctx, userID, list)
		return err
	})
	return list, at, err
}

// UserPage is a page of accounts and the cursor of the next ("" on the
// last).
type UserPage struct {
	Users []domain.User
	Next  string
}

// ListUsers pages through accounts newest first for the admin console.
func (s *Service) ListUsers(ctx context.Context, f ports.UserFilter, cursor string) (UserPage, error) {
	if f.Status != "" && !domain.ValidStatus(f.Status) {
		return UserPage{}, apperr.Invalid("unknown status " + f.Status)
	}
	f.Region = strings.ToUpper(strings.TrimSpace(f.Region))
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	at, id, err := pagecursor.Decode(cursor)
	if err != nil {
		return UserPage{}, apperr.Invalid("bad cursor")
	}
	f.AfterTime, f.AfterID = at, id
	want := f.Limit
	f.Limit++
	list, err := s.Store.Read().Users().List(ctx, f)
	if err != nil {
		return UserPage{}, err
	}
	page := UserPage{Users: list}
	if len(list) > want {
		page.Users = list[:want]
		last := page.Users[want-1]
		page.Next = pagecursor.Encode(last.CreatedAt, last.ID)
	}
	return page, nil
}

// UserStats counts accounts for the admin console's overview: all of
// them, those created at or after since, and per UTC day for the last
// days (0 to 90).
func (s *Service) UserStats(ctx context.Context, since time.Time, days int) (ports.UserStats, error) {
	return s.Store.Read().Users().Stats(ctx, since, max(0, min(days, 90)))
}
