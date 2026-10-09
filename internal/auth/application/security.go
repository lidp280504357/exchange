package application

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	authv1 "github.com/skill/exchange/api/gen/go/exchange/auth/v1"
	"github.com/skill/exchange/internal/auth/domain"
	"github.com/skill/exchange/internal/auth/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/pii"
)

// ResetPassword sets a new password with a PASSWORD_RESET ticket and ends
// every session; withdrawals are held for review for 24 hours after
// (§6.4), which PasswordChanged{reset} tells the wallet.
func (s *AccountService) ResetPassword(ctx context.Context, ticket, password string, c Client) error {
	if err := c.validate(); err != nil {
		return err
	}
	now := s.Now()
	var revoked []string
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		ch, err := Redeem(ctx, r, ticket, domain.ScenePasswordReset, c.DeviceID, now)
		if err != nil {
			return err
		}
		if ch.UserID == "" {
			return domain.ErrTicketInvalid
		}
		if err := s.setPassword(ctx, r, ch.UserID, password, now); err != nil {
			return err
		}
		if err := s.revokeAll(ctx, r, ch.UserID, "", domain.RevokePasswordReset, &revoked); err != nil {
			return err
		}
		return r.Emit(ctx, &authv1.PasswordChanged{UserId: ch.UserID, ViaReset: true}, "user", ch.UserID)
	})
	s.markRevoked(ctx, revoked)
	return err
}

// ChangePassword replaces the password of a signed-in user after a step-up
// and ends the other sessions.
func (s *AccountService) ChangePassword(ctx context.Context, userID, sessionID, current, password, stepUp string) error {
	now := s.Now()
	var revoked []string
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		if _, err := s.consumeStepUp(ctx, r, userID, stepUp); err != nil {
			return err
		}
		cred, err := r.Credentials().Get(ctx, userID)
		if err != nil {
			return err
		}
		if cred == nil {
			return domain.ErrPasswordInvalid
		}
		if ok, err := s.Passwords.Verify(cred.PasswordHash, current); err != nil {
			return err
		} else if !ok {
			return domain.ErrPasswordInvalid
		}
		if err := s.setPassword(ctx, r, userID, password, now); err != nil {
			return err
		}
		if err := s.revokeAll(ctx, r, userID, sessionID, domain.RevokePasswordChange, &revoked); err != nil {
			return err
		}
		return r.Emit(ctx, &authv1.PasswordChanged{UserId: userID}, "user", userID)
	})
	s.markRevoked(ctx, revoked)
	return err
}

func (s *AccountService) setPassword(ctx context.Context, r ports.Repos, userID, password string, now time.Time) error {
	ids, err := r.Identities().ByUser(ctx, userID)
	if err != nil {
		return err
	}
	values := make([]string, len(ids))
	for i, id := range ids {
		values[i] = id.Value
	}
	if err := domain.CheckPassword(password, values...); err != nil {
		return err
	}
	return r.Credentials().SetPassword(ctx, userID, s.Passwords.Hash(password), now)
}

// StepUp turns a STEP_UP ticket of the caller into a step-up token, valid
// 10 minutes for one sensitive action (§6.5).
func (s *AccountService) StepUp(ctx context.Context, userID, sessionID, ticket string, c Client) (string, time.Time, error) {
	if err := c.validate(); err != nil {
		return "", time.Time{}, err
	}
	now := s.Now()
	plain, hash := domain.NewToken()
	expires := now.Add(domain.StepUpTokenTTL)
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		// With an authenticator app bound, a code by mail or SMS is not
		// enough (§6.5: TOTP first).
		if t, _, err := s.activeTOTP(ctx, r, userID); err != nil {
			return err
		} else if t != nil {
			return domain.ErrTOTPRequired
		}
		ch, err := Redeem(ctx, r, ticket, domain.SceneStepUp, c.DeviceID, now)
		if err != nil {
			return err
		}
		if ch.UserID != userID {
			return domain.ErrTicketInvalid
		}
		return r.StepUps().Create(ctx, domain.StepUp{Hash: hash, UserID: userID, SessionID: sessionID, Channel: ch.Channel, ExpiresAt: expires})
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return plain, expires, nil
}

// BindIdentity adds a second identity, proven by a BIND_IDENTITY ticket,
// after a step-up.
func (s *AccountService) BindIdentity(ctx context.Context, userID, ticket, stepUp string, c Client) error {
	if err := c.validate(); err != nil {
		return err
	}
	now := s.Now()
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		if _, err := s.consumeStepUp(ctx, r, userID, stepUp); err != nil {
			return err
		}
		ch, err := Redeem(ctx, r, ticket, domain.SceneBindIdentity, c.DeviceID, now)
		if err != nil {
			return err
		}
		if ch.UserID != userID && ch.UserID != "" {
			return domain.ErrTicketInvalid
		}
		kind := ch.Channel.Kind()
		if err := r.Identities().Create(ctx, domain.Identity{
			ID: uuid.Must(uuid.NewV7()).String(), UserID: userID, Kind: kind, Value: ch.Target,
		}, now); err != nil {
			return err
		}
		return r.Emit(ctx, &authv1.IdentityBound{UserId: userID, Channel: string(ch.Channel), IdentityMask: pii.MaskIdentifier(ch.Target)}, "user", userID)
	})
}

// RebindResult says whether a rebind took effect or awaits review.
type RebindResult string

// Rebind outcomes.
const (
	RebindDone          RebindResult = "DONE"
	RebindPendingReview RebindResult = "PENDING_REVIEW"
)

// RebindIdentity replaces an identity with the one proven by a
// REBIND_IDENTITY ticket (§6.4). A user with both identities must have
// stepped up through the other one; a user with a single identity cannot
// rebind alone, so the request goes to two-person review.
func (s *AccountService) RebindIdentity(ctx context.Context, userID, ticket, stepUp string, c Client) (RebindResult, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	now := s.Now()
	var result RebindResult
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		su, err := s.consumeStepUp(ctx, r, userID, stepUp)
		if err != nil {
			return err
		}
		ch, err := Redeem(ctx, r, ticket, domain.SceneRebindIdentity, c.DeviceID, now)
		if err != nil {
			return err
		}
		if ch.UserID != userID && ch.UserID != "" {
			return domain.ErrTicketInvalid
		}
		ids, err := r.Identities().ByUser(ctx, userID)
		if err != nil {
			return err
		}
		kind := ch.Channel.Kind()
		var current *domain.Identity
		for i := range ids {
			if ids[i].Kind == kind {
				current = &ids[i]
			}
		}
		if current == nil {
			return apperr.Invalid("no identity of this kind to rebind; bind one instead")
		}
		if taken, err := r.Identities().Find(ctx, kind, ch.Target); err != nil {
			return err
		} else if taken != nil {
			return domain.ErrIdentityTaken
		}
		if len(ids) < 2 {
			result = RebindPendingReview
			return r.RebindRequests().Create(ctx, domain.RebindRequest{
				ID: uuid.Must(uuid.NewV7()).String(), UserID: userID, Kind: kind, NewValue: ch.Target,
			})
		}
		if su.Channel == ch.Channel {
			return domain.ErrStepUpRequired.WithDetail("reason", "step up with your other identity")
		}
		if err := r.Identities().UpdateValue(ctx, current.ID, ch.Target, now); err != nil {
			return err
		}
		result = RebindDone
		return r.Emit(ctx, &authv1.IdentityRebound{
			UserId: userID, Channel: string(ch.Channel), OldMask: pii.MaskIdentifier(current.Value), NewMask: pii.MaskIdentifier(ch.Target),
		}, "user", userID)
	})
	return result, err
}

// Contacts returns a user's verified identities (for notification-service).
func (s *AccountService) Contacts(ctx context.Context, userID string) ([]domain.Identity, error) {
	return s.Store.Read().Identities().ByUser(ctx, userID)
}

// ConsumeStepUp redeems a step-up token for another service's action and
// returns it with the user's security context.
func (s *AccountService) ConsumeStepUp(ctx context.Context, userID, token string) (domain.StepUp, domain.SecurityContext, error) {
	var (
		su  *domain.StepUp
		sec domain.SecurityContext
	)
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		var err error
		if su, err = s.consumeStepUp(ctx, r, userID, token); err != nil {
			return err
		}
		sec, err = r.Security().Context(ctx, userID, su.SessionID)
		return err
	})
	if err != nil {
		return domain.StepUp{}, domain.SecurityContext{}, err
	}
	return *su, sec, nil
}

// SecurityContext returns a user's security context without a step-up
// and without a device (the wallet's limits in effect).
func (s *AccountService) SecurityContext(ctx context.Context, userID string) (domain.SecurityContext, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return domain.SecurityContext{}, apperr.NotFound("no such user")
	}
	return s.Store.Read().Security().Context(ctx, userID, uuid.Nil.String())
}

// errNoSuchUser answers FindUser when it finds no user (B167: also for
// what is neither an email address, a phone number nor a username).
var errNoSuchUser = apperr.NotFound("no user has this email address, phone number or username")

// FindUser returns the user an email address, a phone number (E.164) or
// a username belongs to, for the admin console's lookup (B167: its search
// box takes all three; the username through user-service, whatever its
// case); anything else, and no such user, is NOT_FOUND.
func (s *AccountService) FindUser(ctx context.Context, identifier string) (string, error) {
	identifier = strings.TrimSpace(identifier)
	ch := domain.ChannelEmail
	switch {
	case strings.HasPrefix(identifier, "+"):
		ch = domain.ChannelSMS
	case !strings.Contains(identifier, "@"):
		return s.Users.FindUsername(ctx, identifier)
	}
	id, err := domain.ParseIdentifier(ch, identifier)
	if err != nil {
		return "", errNoSuchUser
	}
	found, err := s.Store.Read().Identities().Find(ctx, ch.Kind(), id.Value)
	if err != nil {
		return "", err
	}
	if found == nil {
		return "", errNoSuchUser
	}
	return found.UserID, nil
}

// SearchUsers returns the users whose email address or phone number
// contains q (2 to 64 characters, B170), whatever the case, newest first,
// at most limit (1 to 500; default 200): the admin console's user list
// matches them with the usernames (B167).
func (s *AccountService) SearchUsers(ctx context.Context, q string, limit int) ([]string, error) {
	if n := utf8.RuneCountInString(strings.TrimSpace(q)); n < 2 || n > 64 {
		return nil, apperr.Invalid("q must be 2 to 64 characters")
	}
	q = strings.TrimSpace(q)
	if limit <= 0 {
		limit = 200
	}
	return s.Store.Read().Identities().SearchUsers(ctx, q, min(limit, 500))
}
