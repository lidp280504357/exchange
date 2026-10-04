package application

import (
	"context"
	"time"

	authv1 "github.com/skill/exchange/api/gen/go/exchange/auth/v1"
	"github.com/skill/exchange/internal/auth/domain"
	"github.com/skill/exchange/internal/auth/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/pii"
)

// Refresh rotates a refresh token (§5.2). A token that was already rotated
// is a replay: the whole session is revoked, unless the rotation happened
// within RefreshRaceWindow, which is two tabs racing.
func (s *AccountService) Refresh(ctx context.Context, token string, c Client) (Tokens, error) {
	if token == "" {
		return Tokens{}, domain.ErrSessionRevoked
	}
	now := s.Now()
	hash := domain.HashToken(token)
	var out Tokens
	var revoked []string
	var after error
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		rt, err := r.Sessions().RefreshForUpdate(ctx, hash)
		if err != nil {
			return err
		}
		if rt == nil {
			return domain.ErrSessionRevoked
		}
		sess, err := r.Sessions().Get(ctx, rt.SessionID)
		if err != nil {
			return err
		}
		if sess == nil || !sess.RevokedAt.IsZero() {
			return domain.ErrSessionRevoked
		}
		if !rt.RotatedAt.IsZero() {
			if now.Sub(rt.RotatedAt) < domain.RefreshRaceWindow {
				return domain.ErrTokenExpired
			}
			s.Log.WarnContext(ctx, "refresh token replayed; revoking the session", "session_id", sess.ID, "user_id", sess.UserID)
			if err := s.revoke(ctx, r, sess.UserID, sess.ID, domain.RevokeReplay, &revoked); err != nil {
				return err
			}
			after = domain.ErrSessionRevoked
			return nil
		}
		if !now.Before(rt.ExpiresAt) {
			return domain.ErrTokenExpired
		}
		info, err := s.Users.Get(ctx, sess.UserID)
		if err != nil {
			return err
		}
		scope, err := scopeFor(info.Status)
		if err != nil {
			if err := s.revoke(ctx, r, sess.UserID, sess.ID, domain.RevokeClosed, &revoked); err != nil {
				return err
			}
			after = domain.ErrSessionRevoked
			return nil
		}
		if err := r.Sessions().MarkRotated(ctx, hash, now); err != nil {
			return err
		}
		refresh, newHash := domain.NewToken()
		next := domain.RefreshToken{Hash: newHash, SessionID: sess.ID, Generation: rt.Generation + 1, ExpiresAt: now.Add(domain.RefreshTTL)}
		if err := r.Sessions().CreateRefresh(ctx, next); err != nil {
			return err
		}
		if err := r.Sessions().Touch(ctx, sess.ID, c.IP, truncateUA(c.UserAgent), now); err != nil {
			return err
		}
		access, exp, err := s.Tokens.Issue(sess.UserID, sess.ID, scope, now)
		if err != nil {
			return err
		}
		out = Tokens{
			UserID: sess.UserID, SessionID: sess.ID, Scope: scope, AccessToken: access, AccessExpiresAt: exp,
			RefreshToken: refresh, RefreshExpiresAt: next.ExpiresAt,
		}
		return nil
	})
	s.markRevoked(ctx, revoked)
	if err != nil {
		return Tokens{}, err
	}
	if after != nil {
		return Tokens{}, after
	}
	return out, nil
}

// revoke ends one session inside r and records it for the gateway.
func (s *AccountService) revoke(ctx context.Context, r ports.Repos, userID, sessionID, reason string, revoked *[]string) error {
	ok, err := r.Sessions().Revoke(ctx, sessionID, reason, s.Now())
	if err != nil || !ok {
		return err
	}
	*revoked = append(*revoked, sessionID)
	return r.Emit(ctx, &authv1.SessionRevoked{UserId: userID, SessionId: sessionID, Reason: reason}, "user", userID)
}

// revokeAll ends the user's sessions except keep (empty: all).
func (s *AccountService) revokeAll(ctx context.Context, r ports.Repos, userID, keep, reason string, revoked *[]string) error {
	active, err := r.Sessions().Active(ctx, userID)
	if err != nil {
		return err
	}
	for _, sess := range active {
		if sess.ID == keep {
			continue
		}
		if err := s.revoke(ctx, r, userID, sess.ID, reason, revoked); err != nil {
			return err
		}
	}
	return nil
}

// Logout ends the caller's session.
func (s *AccountService) Logout(ctx context.Context, userID, sessionID string) error {
	var revoked []string
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		return s.revoke(ctx, r, userID, sessionID, domain.RevokeLogout, &revoked)
	})
	s.markRevoked(ctx, revoked)
	return err
}

// LogoutOthers ends every other session; it needs a step-up (§6.5).
func (s *AccountService) LogoutOthers(ctx context.Context, userID, sessionID, stepUp string) error {
	var revoked []string
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		if _, err := s.consumeStepUp(ctx, r, userID, stepUp); err != nil {
			return err
		}
		return s.revokeAll(ctx, r, userID, sessionID, domain.RevokeLogoutAll, &revoked)
	})
	s.markRevoked(ctx, revoked)
	return err
}

// SessionView is a session in the device list.
type SessionView struct {
	domain.Session
	Current bool
}

// Sessions lists the caller's live sessions.
func (s *AccountService) Sessions(ctx context.Context, userID, currentID string) ([]SessionView, error) {
	active, err := s.Store.Read().Sessions().Active(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]SessionView, 0, len(active))
	for _, sess := range active {
		sess.IP = pii.MaskIP(sess.IP)
		out = append(out, SessionView{Session: sess, Current: sess.ID == currentID})
	}
	return out, nil
}

// RevokeSession ends one of the caller's sessions. Ending another device's
// session needs a step-up; ending the current one is a logout.
func (s *AccountService) RevokeSession(ctx context.Context, userID, currentID, targetID, stepUp string) error {
	if targetID == currentID {
		return s.Logout(ctx, userID, currentID)
	}
	var revoked []string
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		sess, err := r.Sessions().Get(ctx, targetID)
		if err != nil {
			return err
		}
		if sess == nil || sess.UserID != userID || !sess.RevokedAt.IsZero() {
			return apperr.NotFound("no such session")
		}
		if _, err := s.consumeStepUp(ctx, r, userID, stepUp); err != nil {
			return err
		}
		return s.revoke(ctx, r, userID, targetID, domain.RevokeUser, &revoked)
	})
	s.markRevoked(ctx, revoked)
	return err
}

// History returns a page of the login history, newest first, and the
// cursor of the next page (0 when there is none).
func (s *AccountService) History(ctx context.Context, userID string, before int64, limit int) ([]domain.LoginEvent, int64, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	events, err := s.Store.Read().History().List(ctx, userID, before, limit+1)
	if err != nil {
		return nil, 0, err
	}
	var next int64
	if len(events) > limit {
		events = events[:limit]
		next = events[limit-1].ID
	}
	for i := range events {
		events[i].IP = pii.MaskIP(events[i].IP)
	}
	return events, next, nil
}

// consumeStepUp redeems a step-up token of userID inside r.
func (s *AccountService) consumeStepUp(ctx context.Context, r ports.Repos, userID, token string) (*domain.StepUp, error) {
	if token == "" {
		return nil, domain.ErrStepUpRequired
	}
	su, err := r.StepUps().Consume(ctx, domain.HashToken(token), userID, s.Now())
	if err != nil {
		return nil, err
	}
	if su == nil {
		return nil, domain.ErrStepUpRequired
	}
	return su, nil
}

// Consumer names auth-service's inbox entries.
const Consumer = "auth-service"

// StaleMargin extends the stale window past the status change: a refresh
// that read the old status just before user-service committed must not
// keep the old scope.
const StaleMargin = time.Second

// OnUserStatusChanged reacts to user-service's status changes: a closed
// account loses every session; any change makes the user's access tokens
// issued up to the change stale, so the next request refreshes them into
// the new scope (read-only for FROZEN). at is when the change happened,
// which keeps redeliveries harmless.
func (s *AccountService) OnUserStatusChanged(ctx context.Context, eventID, userID, to string, at time.Time) error {
	if err := s.Revocations.MarkStale(ctx, userID, at.Add(StaleMargin)); err != nil {
		return err // retried by the consumer
	}
	if to != domain.StatusClosed {
		return nil
	}
	var revoked []string
	_, err := s.Store.Once(ctx, Consumer, eventID, func(r ports.Repos) error {
		return s.revokeAll(ctx, r, userID, "", domain.RevokeClosed, &revoked)
	})
	s.markRevoked(ctx, revoked)
	return err
}
