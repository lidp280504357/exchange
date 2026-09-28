// Package application holds user-service's use cases.
package application

import (
	"context"
	"encoding/json"
	"time"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	userv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/user/v1"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/user/domain"
	"github.com/lidp280504357/exchange/internal/user/ports"
)

// Service manages profiles, account status and eligibility.
type Service struct {
	Store   ports.Store
	Flags   ports.Flags
	StepUps ports.StepUps
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
	var out domain.User
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
	return out, err
}

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
		c.From, c.At = u.Status, s.Now()
		u.Status = c.To
		if _, err := r.Users().Update(ctx, u); err != nil {
			return err
		}
		if err := r.Users().AddStatusChange(ctx, c); err != nil {
			return err
		}
		if err := r.Emit(ctx, event.TopicUser, &userv1.UserStatusChanged{
			UserId: userID, FromStatus: c.From, ToStatus: c.To, ReasonCode: c.Reason, Actor: c.Actor,
		}, "user", userID); err != nil {
			return err
		}
		details, _ := json.Marshal(map[string]string{"from": c.From, "to": c.To, "note": c.Note})
		return r.Emit(ctx, event.TopicAudit, &auditv1.AdminActionPerformed{
			Target: "user:" + userID, Action: "user.status_changed", Actor: c.Actor, Reason: c.Reason, Details: string(details),
		}, "actor", c.Actor)
	})
	return c, err
}

// StatusHistory lists the recent status changes of a user.
func (s *Service) StatusHistory(ctx context.Context, userID string) ([]domain.StatusChange, error) {
	return s.Store.Read().Users().StatusHistory(ctx, userID, 50)
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
