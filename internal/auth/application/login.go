package application

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	authv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/auth/v1"
	"github.com/lidp280504357/exchange/internal/auth/domain"
	"github.com/lidp280504357/exchange/internal/auth/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/pii"
)

// PasswordLogin is a login/password call.
type PasswordLogin struct {
	Identifier   string
	Password     string
	CaptchaToken string
	Client       Client
}

// ChallengeChannel is one way to receive the login-challenge code.
type ChallengeChannel struct {
	Channel string `json:"channel"`
	Target  string `json:"target"` // masked
}

// LoginResult is either tokens or, after 7 silent days, a challenge to
// finish with an OTP (ErrLoginChallengeRequired).
type LoginResult struct {
	Tokens           Tokens
	LoginChallengeID string
	Channels         []ChallengeChannel
}

// LoginPassword checks the password. Failures are counted per identifier,
// known or not, so the captcha and lock rules reveal nothing; unknown
// accounts spend a dummy hash so timing matches too.
func (s *AccountService) LoginPassword(ctx context.Context, in PasswordLogin) (LoginResult, error) {
	if err := in.Client.validate(); err != nil {
		return LoginResult{}, err
	}
	ch := domain.ChannelSMS
	if strings.Contains(in.Identifier, "@") {
		ch = domain.ChannelEmail
	}
	id, err := domain.ParseIdentifier(ch, in.Identifier)
	if err != nil {
		return LoginResult{}, err
	}
	key := "login_fail:" + hashKey(id.Value)
	fails, ttl, err := s.Guard.Failures(ctx, key)
	if err != nil {
		return LoginResult{}, apperr.Unavailable(err)
	}
	if fails >= domain.LockAfterFailures {
		return LoginResult{}, domain.ErrAccountLocked.WithDetail("retry_after_seconds", int(ttl.Seconds())+1)
	}
	if fails >= domain.CaptchaAfterFailures {
		if in.CaptchaToken == "" {
			return LoginResult{}, domain.ErrCaptchaRequired
		}
		if err := s.Captcha.Verify(ctx, in.CaptchaToken, in.Client.IP); err != nil {
			return LoginResult{}, domain.ErrCaptchaFailed
		}
	}

	identity, err := s.Store.Read().Identities().Find(ctx, ch.Kind(), id.Value)
	if err != nil {
		return LoginResult{}, err
	}
	var cred *domain.Credential
	if identity != nil {
		if cred, err = s.Store.Read().Credentials().Get(ctx, identity.UserID); err != nil {
			return LoginResult{}, err
		}
	}
	if cred == nil {
		s.Passwords.VerifyDummy(in.Password)
		return LoginResult{}, s.recordFailure(ctx, key, "", id.Value, in.Client)
	}
	ok, err := s.Passwords.Verify(cred.PasswordHash, in.Password)
	if err != nil {
		return LoginResult{}, err
	}
	if !ok {
		return LoginResult{}, s.recordFailure(ctx, key, cred.UserID, id.Value, in.Client)
	}

	info, err := s.Users.Get(ctx, cred.UserID)
	if err != nil {
		return LoginResult{}, err
	}
	if info.Status == domain.StatusClosed {
		return LoginResult{}, domain.ErrUserClosed
	}
	if err := s.Guard.Clear(ctx, key); err != nil {
		s.Log.WarnContext(ctx, "clearing login failures failed", "error", err)
	}

	now := s.Now()
	if cred.LastLoginAt.IsZero() || now.Sub(cred.LastLoginAt) > s.silence() {
		return s.challengeLogin(ctx, cred.UserID, id.Value, in.Client, now)
	}
	var res LoginResult
	var revoked []string
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		var err error
		res.Tokens, revoked, err = s.startSession(ctx, r, cred.UserID, info.Status, "PASSWORD", pii.MaskIdentifier(id.Value), in.Client, now)
		return err
	})
	s.markRevoked(ctx, revoked)
	return res, err
}

// recordFailure counts a wrong password; the tenth within 15 minutes locks
// the identifier. For a real account the failure is recorded and a lock is
// announced (the user gets notified).
func (s *AccountService) recordFailure(ctx context.Context, key, userID, identifier string, c Client) error {
	n, err := s.Guard.Fail(ctx, key)
	if err != nil {
		s.Log.WarnContext(ctx, "counting login failure failed", "error", err)
	}
	locked := n >= domain.LockAfterFailures
	if userID != "" {
		now := s.Now()
		var lockedUntil time.Time
		result, reason := "FAILED_PASSWORD", "PASSWORD_INVALID"
		if locked {
			lockedUntil, result, reason = now.Add(domain.LockDuration), "LOCKED", "ACCOUNT_LOCKED"
		}
		err := s.Store.Tx(ctx, func(r ports.Repos) error {
			if err := r.Credentials().RecordFailure(ctx, userID, lockedUntil); err != nil {
				return err
			}
			if err := r.History().Add(ctx, domain.LoginEvent{
				UserID: userID, Method: "PASSWORD", Result: result, IdentityMask: pii.MaskIdentifier(identifier),
				DeviceID: c.DeviceID, UserAgent: truncateUA(c.UserAgent), IP: c.IP, CreatedAt: now,
			}); err != nil {
				return err
			}
			return r.Emit(ctx, &authv1.LoginFailed{
				UserId: userID, Method: "PASSWORD", Reason: reason, Locked: locked, IpMask: pii.MaskIP(c.IP),
			}, "user", userID)
		})
		if err != nil {
			return err
		}
	}
	if locked {
		return domain.ErrAccountLocked.WithDetail("retry_after_seconds", int(domain.LockDuration.Seconds()))
	}
	return domain.ErrPasswordInvalid
}

// challengeLogin opens the OTP step after a correct password when the
// account has been silent for 7 days.
func (s *AccountService) challengeLogin(ctx context.Context, userID, identifier string, c Client, now time.Time) (LoginResult, error) {
	ids, err := s.Store.Read().Identities().ByUser(ctx, userID)
	if err != nil {
		return LoginResult{}, err
	}
	lc := domain.LoginChallenge{ID: uuid.Must(uuid.NewV7()).String(), UserID: userID, DeviceID: c.DeviceID, ExpiresAt: now.Add(domain.LoginChallTTL)}
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LoginChallenges().Create(ctx, lc); err != nil {
			return err
		}
		return r.History().Add(ctx, domain.LoginEvent{
			UserID: userID, Method: "PASSWORD", Result: "CHALLENGE_REQUIRED", IdentityMask: pii.MaskIdentifier(identifier),
			DeviceID: c.DeviceID, UserAgent: truncateUA(c.UserAgent), IP: c.IP, CreatedAt: now,
		})
	})
	if err != nil {
		return LoginResult{}, err
	}
	res := LoginResult{LoginChallengeID: lc.ID}
	for _, id := range ids {
		ch := domain.ChannelEmail
		if id.Kind == "PHONE" {
			ch = domain.ChannelSMS
		}
		res.Channels = append(res.Channels, ChallengeChannel{Channel: string(ch), Target: pii.MaskIdentifier(id.Value)})
	}
	return res, domain.ErrLoginChallengeRequired.
		WithDetail("login_challenge_id", lc.ID).
		WithDetail("expires_at", lc.ExpiresAt.UTC().Format(time.RFC3339)).
		WithDetail("channels", res.Channels)
}

// LoginOTP signs in with a LOGIN ticket (the backup method).
func (s *AccountService) LoginOTP(ctx context.Context, ticket string, c Client) (Tokens, error) {
	if err := c.validate(); err != nil {
		return Tokens{}, err
	}
	return s.loginWithTicket(ctx, ticket, domain.SceneLogin, "OTP", "", c)
}

// CompleteLoginChallenge finishes a password login with a LOGIN_CHALLENGE
// ticket issued for the same challenge, user and device.
func (s *AccountService) CompleteLoginChallenge(ctx context.Context, loginChallengeID, ticket string, c Client) (Tokens, error) {
	if err := c.validate(); err != nil {
		return Tokens{}, err
	}
	if _, err := uuid.Parse(loginChallengeID); err != nil {
		return Tokens{}, domain.ErrLoginChallengeInvalid
	}
	return s.loginWithTicket(ctx, ticket, domain.SceneLoginChallenge, "LOGIN_CHALLENGE", loginChallengeID, c)
}

func (s *AccountService) loginWithTicket(ctx context.Context, ticket string, scene domain.Scene, method, loginChallengeID string, c Client) (Tokens, error) {
	now := s.Now()
	var out Tokens
	var revoked []string
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		ch, err := Redeem(ctx, r, ticket, scene, c.DeviceID, now)
		if err != nil {
			return err
		}
		if ch.UserID == "" {
			return domain.ErrTicketInvalid
		}
		if scene == domain.SceneLoginChallenge {
			lc, err := r.LoginChallenges().Get(ctx, loginChallengeID)
			if err != nil {
				return err
			}
			if lc == nil || lc.ID != ch.LoginChallengeID || lc.UserID != ch.UserID || lc.DeviceID != c.DeviceID {
				return domain.ErrLoginChallengeInvalid
			}
			if ok, err := r.LoginChallenges().Consume(ctx, lc.ID, now); err != nil {
				return err
			} else if !ok {
				return domain.ErrLoginChallengeInvalid
			}
		}
		info, err := s.Users.Get(ctx, ch.UserID)
		if err != nil {
			return err
		}
		out, revoked, err = s.startSession(ctx, r, ch.UserID, info.Status, method, pii.MaskIdentifier(ch.Target), c, now)
		return err
	})
	s.markRevoked(ctx, revoked)
	return out, err
}
