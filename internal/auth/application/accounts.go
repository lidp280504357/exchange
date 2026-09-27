package application

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	authv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/auth/v1"
	"github.com/lidp280504357/exchange/internal/auth/domain"
	"github.com/lidp280504357/exchange/internal/auth/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/authtoken"
	"github.com/lidp280504357/exchange/internal/platform/pii"
)

// AccountConfig holds the configurable rules.
type AccountConfig struct {
	// Current versions of the documents users accept at registration.
	TermsVersion string
	RiskVersion  string
	// LoginSilence overrides domain.LoginSilence when positive.
	LoginSilence time.Duration
}

// AccountService registers users and runs logins, sessions and
// sensitive-action checks (requirements §6).
type AccountService struct {
	Store       ports.Store
	Users       ports.Users
	Passwords   *domain.PasswordHasher
	Tokens      ports.Tokens
	Revocations ports.Revocations
	Guard       ports.LoginGuard
	Captcha     ports.Captcha
	Config      AccountConfig
	Log         *slog.Logger
	Now         func() time.Time
}

// Client describes the device a request comes from.
type Client struct {
	DeviceID   string
	UserAgent  string
	IP         string
	ClientType string
}

func (c Client) validate() error {
	if !domain.ValidDeviceID(c.DeviceID) {
		return domain.ErrDeviceRequired
	}
	return nil
}

// Tokens is the result of a login: an access token, and a refresh token
// the transport puts in a cookie (WEB) or the body (APP).
type Tokens struct {
	UserID           string
	SessionID        string
	Scope            string
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
}

func (s *AccountService) silence() time.Duration {
	if s.Config.LoginSilence > 0 {
		return s.Config.LoginSilence
	}
	return domain.LoginSilence
}

// scopeFor maps the account status to a token scope; closed accounts get none.
func scopeFor(status string) (string, error) {
	switch status {
	case domain.StatusClosed:
		return "", domain.ErrUserClosed
	case domain.StatusFrozen:
		return authtoken.ScopeRead, nil
	default:
		return authtoken.ScopeFull, nil
	}
}

// RegisterInput is a register/complete call.
type RegisterInput struct {
	Ticket       string
	Password     string
	Country      string
	Language     string
	Timezone     string
	TermsVersion string
	RiskVersion  string
	Client       Client
}

// Register creates the account behind a REGISTER ticket and signs the user
// in. user-service creates the profile first (idempotently), so a failure
// after it leaves at most an orphan profile, never an account without one.
func (s *AccountService) Register(ctx context.Context, in RegisterInput) (Tokens, error) {
	if err := in.Client.validate(); err != nil {
		return Tokens{}, err
	}
	if in.TermsVersion != s.Config.TermsVersion || in.RiskVersion != s.Config.RiskVersion {
		return Tokens{}, domain.ErrTermsOutdated.WithDetail("terms_version", s.Config.TermsVersion).
			WithDetail("risk_disclosure_version", s.Config.RiskVersion)
	}
	now := s.Now()
	var out Tokens
	var revoked []string
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		c, err := Redeem(ctx, r, in.Ticket, domain.SceneRegister, in.Client.DeviceID, now)
		if err != nil {
			return err
		}
		id, err := domain.ParseIdentifier(c.Channel, c.Target)
		if err != nil {
			return err
		}
		if err := domain.CheckPassword(in.Password, id.Value); err != nil {
			return err
		}
		region := id.Region
		if region == "" {
			region = strings.ToUpper(strings.TrimSpace(in.Country))
		}
		if len(region) != 2 || strings.Trim(region, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
			return apperr.Invalid("country must be an ISO 3166-1 alpha-2 code")
		}
		if taken, err := r.Identities().Find(ctx, id.Channel.Kind(), id.Value); err != nil {
			return err
		} else if taken != nil {
			return domain.ErrIdentityTaken
		}

		userID := uuid.Must(uuid.NewV7()).String()
		lang := in.Language
		if lang == "" {
			lang = "zh-CN"
		}
		if err := s.Users.Create(ctx, ports.NewUser{
			ID: userID, Region: region, Language: lang, Timezone: in.Timezone,
			TermsVersion: in.TermsVersion, RiskVersion: in.RiskVersion,
		}); err != nil {
			return err
		}
		if err := r.Identities().Create(ctx, domain.Identity{
			ID: uuid.Must(uuid.NewV7()).String(), UserID: userID, Kind: id.Channel.Kind(), Value: id.Value,
		}, now); err != nil {
			return err
		}
		if err := r.Credentials().Create(ctx, userID, s.Passwords.Hash(in.Password), now); err != nil {
			return err
		}
		if err := r.Emit(ctx, &authv1.UserRegistered{
			UserId: userID, Channel: string(id.Channel), IdentityMask: pii.MaskIdentifier(id.Value), Region: region,
			Language: lang, DeviceId: in.Client.DeviceID, IpMask: pii.MaskIP(in.Client.IP),
		}, "user", userID); err != nil {
			return err
		}
		out, revoked, err = s.startSession(ctx, r, userID, domain.StatusActive, "REGISTER", pii.MaskIdentifier(id.Value), in.Client, now)
		return err
	})
	s.markRevoked(ctx, revoked)
	return out, err
}

// startSession opens a session for a user who just proved who they are:
// it enforces the session cap, issues both tokens, records the device and
// the login, and emits LoginSucceeded. It returns sessions it had to evict.
func (s *AccountService) startSession(ctx context.Context, r ports.Repos, userID, status, method, identityMask string, c Client, now time.Time) (Tokens, []string, error) {
	scope, err := scopeFor(status)
	if err != nil {
		return Tokens{}, nil, err
	}
	active, err := r.Sessions().Active(ctx, userID)
	if err != nil {
		return Tokens{}, nil, err
	}
	var evicted []string
	for i := len(active) - 1; i >= domain.MaxSessions-1; i-- {
		if ok, err := r.Sessions().Revoke(ctx, active[i].ID, domain.RevokeLimit, now); err != nil {
			return Tokens{}, nil, err
		} else if ok {
			evicted = append(evicted, active[i].ID)
			if err := r.Emit(ctx, &authv1.SessionRevoked{UserId: userID, SessionId: active[i].ID, Reason: domain.RevokeLimit}, "user", userID); err != nil {
				return Tokens{}, nil, err
			}
		}
	}

	clientType := c.ClientType
	if clientType != domain.ClientApp {
		clientType = domain.ClientWeb
	}
	sess := domain.Session{
		ID: uuid.Must(uuid.NewV7()).String(), UserID: userID, DeviceID: c.DeviceID, ClientType: clientType,
		UserAgent: truncateUA(c.UserAgent), IP: c.IP, CreatedAt: now,
	}
	if err := r.Sessions().Create(ctx, sess); err != nil {
		return Tokens{}, nil, err
	}
	refresh, hash := domain.NewToken()
	rt := domain.RefreshToken{Hash: hash, SessionID: sess.ID, Generation: 1, ExpiresAt: now.Add(domain.RefreshTTL)}
	if err := r.Sessions().CreateRefresh(ctx, rt); err != nil {
		return Tokens{}, nil, err
	}
	newDevice, err := r.Devices().Seen(ctx, userID, c.DeviceID, now)
	if err != nil {
		return Tokens{}, nil, err
	}
	if err := r.Credentials().RecordLogin(ctx, userID, now); err != nil {
		return Tokens{}, nil, err
	}
	if err := r.History().Add(ctx, domain.LoginEvent{
		UserID: userID, Method: method, Result: "SUCCESS", IdentityMask: identityMask, DeviceID: c.DeviceID,
		UserAgent: sess.UserAgent, IP: c.IP, NewDevice: newDevice, CreatedAt: now,
	}); err != nil {
		return Tokens{}, nil, err
	}
	if err := r.Emit(ctx, &authv1.LoginSucceeded{
		UserId: userID, SessionId: sess.ID, Method: method, DeviceId: c.DeviceID, NewDevice: newDevice,
		IpMask: pii.MaskIP(c.IP), UserAgent: sess.UserAgent,
	}, "user", userID); err != nil {
		return Tokens{}, nil, err
	}
	access, exp, err := s.Tokens.Issue(userID, sess.ID, scope, now)
	if err != nil {
		return Tokens{}, nil, err
	}
	return Tokens{
		UserID: userID, SessionID: sess.ID, Scope: scope, AccessToken: access, AccessExpiresAt: exp,
		RefreshToken: refresh, RefreshExpiresAt: rt.ExpiresAt,
	}, evicted, nil
}

// markRevoked tells the gateway about ended sessions; best effort, since
// the access tokens expire within 15 minutes anyway.
func (s *AccountService) markRevoked(ctx context.Context, sessionIDs []string) {
	if len(sessionIDs) == 0 {
		return
	}
	if err := s.Revocations.Revoke(context.WithoutCancel(ctx), sessionIDs...); err != nil {
		s.Log.WarnContext(ctx, "marking revoked sessions failed", "error", err)
	}
}

// maxUserAgent bounds the stored User-Agent.
const maxUserAgent = 512

func truncateUA(s string) string {
	if len(s) <= maxUserAgent {
		return s
	}
	return s[:maxUserAgent]
}
