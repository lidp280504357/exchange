// Package application holds auth-service's use cases.
package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	authv1 "github.com/skill/exchange/api/gen/go/exchange/auth/v1"
	"github.com/skill/exchange/internal/auth/domain"
	"github.com/skill/exchange/internal/auth/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/pii"
	"github.com/skill/exchange/internal/platform/ratelimit"
)

// OTP quotas (requirements §5.3, §6.3).
var (
	RuleResend       = ratelimit.Rule{Name: "otp_resend", Limit: 1, Window: 60 * time.Second}
	RuleTargetHourly = ratelimit.Rule{Name: "otp_target_h", Limit: 5, Window: time.Hour}
	RuleTargetDaily  = ratelimit.Rule{Name: "otp_target_d", Limit: 10, Window: 24 * time.Hour}
	RuleIPHourly     = ratelimit.Rule{Name: "otp_ip_h", Limit: 20, Window: time.Hour}
	RuleDeviceHourly = ratelimit.Rule{Name: "otp_device_h", Limit: 10, Window: time.Hour}
)

// SMSBudget caps all SMS sends; past it SMS pauses and alerts (§6.3).
type SMSBudget struct {
	Hourly int
	Daily  int
}

// OTPService issues and checks one-time codes.
type OTPService struct {
	Store    ports.Store
	Notifier ports.Notifier
	Captcha  ports.Captcha
	Limiter  ports.Limiter
	Flags    ports.Flags
	Hasher   domain.CodeHasher
	SMS      SMSBudget
	Log      *slog.Logger
	// Metrics counts requests and verifications; nil counts nothing.
	Metrics ports.OTPMetrics
	// Tester reports the captcha token of trusted test clients (the
	// non-production bypass token). Their requests skip the per-IP quota,
	// so end-to-end suites can run repeatedly from one machine; the
	// per-target, per-device and SMS budget quotas still apply.
	Tester func(captchaToken string) bool
	// Now and Dispatch are replaced in tests.
	Now      func() time.Time
	Dispatch func(func(context.Context))
}

// RequestOTP is an otp/request call.
type RequestOTP struct {
	Scene            string
	Channel          string
	Identifier       string
	LoginChallengeID string
	CaptchaToken     string
	DeviceID         string
	Language         string
	IP               string
	// UserID is the signed-in user, required by authenticated scenes.
	UserID string
}

// ChallengeView is the otp/request response. Delivery is always QUEUED:
// codes go out in the background, so neither the status nor the timing
// tells whether the account exists.
type ChallengeView struct {
	ChallengeID string
	ExpiresAt   time.Time
	Delivery    string
}

// Request creates a challenge and sends its code. Unknown accounts (and
// known ones asking to register) get a decoy challenge that no code
// verifies, so the response is the same either way (§5.2, §6.1). Every
// request is counted by outcome for the operators.
func (s *OTPService) Request(ctx context.Context, req RequestOTP) (ChallengeView, error) {
	view, decoy, err := s.requestChallenge(ctx, req)
	if s.Metrics != nil {
		scene, channel := "invalid", "invalid"
		if sc, perr := domain.ParseScene(req.Scene); perr == nil {
			scene = string(sc)
		}
		if ch, perr := domain.ParseChannel(req.Channel); perr == nil {
			channel = string(ch)
		}
		s.Metrics.Requested(scene, channel, requestOutcome(decoy, err))
	}
	return view, err
}

// requestOutcome is the metric label of a request's result.
func requestOutcome(decoy bool, err error) string {
	switch {
	case err == nil && decoy:
		return "decoy"
	case err == nil:
		return "queued"
	case apperr.Is(err, "AUTH_CAPTCHA_REQUIRED"), apperr.Is(err, "AUTH_CAPTCHA_FAILED"):
		return "captcha_rejected"
	case apperr.Is(err, "AUTH_OTP_RESEND_TOO_SOON"), apperr.Is(err, apperr.CodeRateLimited):
		return "rate_limited"
	case apperr.Is(err, "AUTH_CHANNEL_UNAVAILABLE"):
		return "channel_unavailable"
	case apperr.Is(err, apperr.CodeUnavailable), apperr.Is(err, apperr.CodeInternal):
		return "error"
	default:
		return "invalid"
	}
}

func (s *OTPService) requestChallenge(ctx context.Context, req RequestOTP) (ChallengeView, bool, error) {
	scene, err := domain.ParseScene(req.Scene)
	if err != nil {
		return ChallengeView{}, false, err
	}
	ch, err := domain.ParseChannel(req.Channel)
	if err != nil {
		return ChallengeView{}, false, err
	}
	if !domain.ValidDeviceID(req.DeviceID) {
		return ChallengeView{}, false, domain.ErrDeviceRequired
	}
	if scene.Authenticated() && req.UserID == "" {
		return ChallengeView{}, false, apperr.Unauthorized("sign in first")
	}
	if req.CaptchaToken == "" {
		return ChallengeView{}, false, domain.ErrCaptchaRequired
	}
	if err := s.Captcha.Verify(ctx, req.CaptchaToken, req.IP); err != nil {
		return ChallengeView{}, false, domain.ErrCaptchaFailed
	}

	target, userID, err := s.resolveTarget(ctx, scene, ch, req)
	if err != nil {
		return ChallengeView{}, false, err
	}
	if ch == domain.ChannelSMS && !s.Flags.Enabled(flags.KeyRegistrationSMS, flags.Subject{Region: target.Region, UserID: userID}) {
		return ChallengeView{}, false, domain.ErrChannelUnavailable
	}
	if err := s.checkQuotas(ctx, ch, target.Value, req); err != nil {
		return ChallengeView{}, false, err
	}

	decoy, err := s.isDecoy(ctx, scene, target, &userID)
	if err != nil {
		return ChallengeView{}, false, err
	}
	now := s.Now()
	c := &domain.Challenge{
		ID:               uuid.Must(uuid.NewV7()).String(),
		Scene:            scene,
		Channel:          ch,
		Target:           target.Value,
		UserID:           userID,
		LoginChallengeID: req.LoginChallengeID,
		DeviceID:         req.DeviceID,
		Status:           domain.ChallengePending,
		ExpiresAt:        now.Add(domain.CodeTTL),
		CreatedAt:        now,
	}
	code := domain.NewCode()
	c.CodeHash = s.Hasher.Hash(c.ID, code)
	if decoy {
		c.CodeHash = domain.RandomBytes(32)
	}
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.Challenges().Create(ctx, c); err != nil {
			return err
		}
		return r.Emit(ctx, &authv1.OtpRequested{
			ChallengeId: c.ID, Scene: string(scene), Channel: string(ch), TargetMask: pii.MaskIdentifier(target.Value),
			UserId: userID, DeviceId: req.DeviceID, IpMask: pii.MaskIP(req.IP),
		}, "user", aggregateKey(userID, c.ID))
	})
	if err != nil {
		return ChallengeView{}, false, err
	}
	if !decoy {
		delivery := ports.OTPDelivery{
			ChallengeID: c.ID, Channel: ch, Target: target.Value, Code: code, Scene: scene,
			Language: req.Language, UserID: userID, TTL: domain.CodeTTL,
		}
		s.Dispatch(func(ctx context.Context) {
			if err := s.Notifier.SendOTP(ctx, delivery); err != nil {
				s.Log.WarnContext(ctx, "otp delivery failed", "challenge_id", c.ID, "error", err)
			}
		})
	}
	return ChallengeView{ChallengeID: c.ID, ExpiresAt: c.ExpiresAt, Delivery: "QUEUED"}, decoy, nil
}

// resolveTarget finds where the code goes: the given identifier, or for a
// signed-in user or pending login challenge, the bound identity of the
// channel.
func (s *OTPService) resolveTarget(ctx context.Context, scene domain.Scene, ch domain.Channel, req RequestOTP) (domain.Identifier, string, error) {
	switch scene {
	case domain.SceneStepUp, domain.SceneWithdrawConfirm:
		return s.boundIdentity(ctx, req.UserID, ch)
	case domain.SceneLoginChallenge:
		if _, err := uuid.Parse(req.LoginChallengeID); err != nil {
			return domain.Identifier{}, "", domain.ErrLoginChallengeInvalid
		}
		lc, err := s.Store.Read().LoginChallenges().Get(ctx, req.LoginChallengeID)
		if err != nil {
			return domain.Identifier{}, "", err
		}
		if lc == nil || !lc.ConsumedAt.IsZero() || !s.Now().Before(lc.ExpiresAt) || lc.DeviceID != req.DeviceID {
			return domain.Identifier{}, "", domain.ErrLoginChallengeInvalid
		}
		return s.boundIdentity(ctx, lc.UserID, ch)
	default:
		id, err := domain.ParseIdentifier(ch, req.Identifier)
		return id, req.UserID, err
	}
}

func (s *OTPService) boundIdentity(ctx context.Context, userID string, ch domain.Channel) (domain.Identifier, string, error) {
	ids, err := s.Store.Read().Identities().ByUser(ctx, userID)
	if err != nil {
		return domain.Identifier{}, "", err
	}
	for _, id := range ids {
		if id.Kind == ch.Kind() {
			parsed, _ := domain.ParseIdentifier(ch, id.Value)
			return parsed, userID, nil
		}
	}
	return domain.Identifier{}, "", apperr.Invalid("no identity is bound for this channel")
}

// isDecoy decides whether the challenge must not be verifiable, and fills
// in the user of anonymous scenes that name an existing account.
func (s *OTPService) isDecoy(ctx context.Context, scene domain.Scene, target domain.Identifier, userID *string) (bool, error) {
	switch scene {
	case domain.SceneRegister, domain.SceneLogin, domain.ScenePasswordReset,
		domain.SceneBindIdentity, domain.SceneRebindIdentity:
	default:
		return false, nil
	}
	existing, err := s.Store.Read().Identities().Find(ctx, target.Channel.Kind(), target.Value)
	if err != nil {
		return false, err
	}
	switch scene {
	case domain.SceneRegister, domain.SceneBindIdentity, domain.SceneRebindIdentity:
		return existing != nil, nil // the identity is already taken
	default:
		if existing == nil {
			return true, nil // no such account
		}
		*userID = existing.UserID
		return false, nil
	}
}

// checkQuotas applies the per-target, per-IP and per-device quotas, and
// the global SMS budget.
func (s *OTPService) checkQuotas(ctx context.Context, ch domain.Channel, target string, req RequestOTP) error {
	key := hashKey(target)
	checks := []ratelimit.Check{
		{Rule: RuleResend, Key: key},
		{Rule: RuleTargetHourly, Key: key},
		{Rule: RuleTargetDaily, Key: key},
		{Rule: RuleDeviceHourly, Key: hashKey(req.DeviceID)},
	}
	if req.IP != "" && (s.Tester == nil || !s.Tester(req.CaptchaToken)) {
		checks = append(checks, ratelimit.Check{Rule: RuleIPHourly, Key: hashKey(req.IP)})
	}
	if ch == domain.ChannelSMS {
		checks = append(checks,
			ratelimit.Check{Rule: ratelimit.Rule{Name: "sms_all_h", Limit: s.SMS.Hourly, Window: time.Hour}, Key: "all"},
			ratelimit.Check{Rule: ratelimit.Rule{Name: "sms_all_d", Limit: s.SMS.Daily, Window: 24 * time.Hour}, Key: "all"},
		)
	}
	res, err := s.Limiter.Allow(ctx, checks...)
	if err != nil {
		return apperr.Unavailable(err)
	}
	switch {
	case res.Allowed:
		return nil
	case res.Rule.Name == RuleResend.Name:
		return domain.ErrOTPResendTooSoon.WithDetail("retry_after_seconds", int(res.RetryAfter.Seconds())+1)
	case res.Rule.Name == "sms_all_h" || res.Rule.Name == "sms_all_d":
		s.Log.ErrorContext(ctx, "SMS budget exhausted; SMS paused", "rule", res.Rule.Name)
		return domain.ErrChannelUnavailable
	default:
		return apperr.New(apperr.KindRateLimited, apperr.CodeRateLimited, "too many requests").
			WithDetail("retry_after_seconds", int(res.RetryAfter.Seconds())+1)
	}
}

// VerifyOTP is an otp/verify call.
type VerifyOTP struct {
	ChallengeID string
	Code        string
	DeviceID    string
}

// TicketView is the otp/verify response.
type TicketView struct {
	Ticket    string
	Scene     domain.Scene
	ExpiresAt time.Time
}

// Verify checks a code, counting the outcome for the operators.
func (s *OTPService) Verify(ctx context.Context, req VerifyOTP) (TicketView, error) {
	view, err := s.verify(ctx, req)
	if s.Metrics != nil {
		s.Metrics.Verified(verifyOutcome(err))
	}
	return view, err
}

// verifyOutcome is the metric label of a verification's result.
func verifyOutcome(err error) string {
	switch {
	case err == nil:
		return "verified"
	case apperr.Is(err, "AUTH_OTP_INVALID"), apperr.Is(err, "AUTH_OTP_EXPIRED"), apperr.Is(err, "AUTH_OTP_ATTEMPTS_EXCEEDED"):
		return strings.ToLower(failureReason(err))
	default:
		return "error"
	}
}

// verify checks a code. The attempt counts even when the code is wrong,
// so the transaction commits before the error is returned.
func (s *OTPService) verify(ctx context.Context, req VerifyOTP) (TicketView, error) {
	if _, err := uuid.Parse(req.ChallengeID); err != nil {
		return TicketView{}, domain.ErrOTPInvalid
	}
	var view TicketView
	var verr error
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		c, err := r.Challenges().GetForUpdate(ctx, req.ChallengeID)
		if err != nil {
			return err
		}
		if c == nil {
			verr = domain.ErrOTPInvalid
			return nil
		}
		now := s.Now()
		if verr = c.Attempt(req.Code, req.DeviceID, now, s.Hasher); verr != nil {
			if err := r.Challenges().Update(ctx, c); err != nil {
				return err
			}
			return r.Emit(ctx, &authv1.OtpFailed{
				ChallengeId: c.ID, Scene: string(c.Scene), UserId: c.UserID, Reason: failureReason(verr), Attempts: int32(c.Attempts), //nolint:gosec // at most MaxAttempts
			}, "user", aggregateKey(c.UserID, c.ID))
		}
		if err := r.Challenges().Update(ctx, c); err != nil {
			return err
		}
		plain, hash := domain.NewToken()
		t := domain.Ticket{Hash: hash, ChallengeID: c.ID, Scene: c.Scene, DeviceID: c.DeviceID, ExpiresAt: now.Add(domain.TicketTTL)}
		if err := r.Tickets().Create(ctx, t); err != nil {
			return err
		}
		view = TicketView{Ticket: plain, Scene: c.Scene, ExpiresAt: t.ExpiresAt}
		return r.Emit(ctx, &authv1.OtpVerified{ChallengeId: c.ID, Scene: string(c.Scene), UserId: c.UserID},
			"user", aggregateKey(c.UserID, c.ID))
	})
	if err != nil {
		return TicketView{}, err
	}
	if verr != nil {
		return TicketView{}, verr
	}
	return view, nil
}

// Redeem consumes a ticket inside r's transaction and returns its verified
// challenge; */complete flows call it.
func Redeem(ctx context.Context, r ports.Repos, ticket string, scene domain.Scene, deviceID string, now time.Time) (*domain.Challenge, error) {
	if ticket == "" {
		return nil, domain.ErrTicketInvalid
	}
	id, err := r.Tickets().Consume(ctx, domain.HashToken(ticket), scene, deviceID, now)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, domain.ErrTicketInvalid
	}
	c, err := r.Challenges().GetForUpdate(ctx, id)
	if err != nil {
		return nil, err
	}
	if c == nil || c.Status != domain.ChallengeVerified {
		return nil, domain.ErrTicketInvalid
	}
	return c, nil
}

func failureReason(err error) string {
	switch {
	case apperr.Is(err, "AUTH_OTP_EXPIRED"):
		return "EXPIRED"
	case apperr.Is(err, "AUTH_OTP_ATTEMPTS_EXCEEDED"):
		return "ATTEMPTS_EXCEEDED"
	default:
		return "INVALID"
	}
}

// aggregateKey keys events by user, or by challenge before a user exists.
func aggregateKey(userID, fallback string) string {
	if userID != "" {
		return userID
	}
	return fallback
}

// hashKey keeps personal data out of Redis keys.
func hashKey(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:12])
}
