package domain

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// OTP rules (requirements §5.3).
const (
	CodeTTL        = 5 * time.Minute
	TicketTTL      = 5 * time.Minute
	MaxAttempts    = 5
	LoginChallTTL  = 10 * time.Minute
	StepUpTokenTTL = 10 * time.Minute
)

// Scene is why a code is requested.
type Scene string

// Scenes (§6.1).
const (
	SceneRegister        Scene = "REGISTER"
	SceneLogin           Scene = "LOGIN"
	SceneLoginChallenge  Scene = "LOGIN_CHALLENGE"
	ScenePasswordReset   Scene = "PASSWORD_RESET"
	SceneBindIdentity    Scene = "BIND_IDENTITY"
	SceneRebindIdentity  Scene = "REBIND_IDENTITY"
	SceneStepUp          Scene = "STEP_UP"
	SceneWithdrawConfirm Scene = "WITHDRAW_CONFIRM"
)

// ParseScene accepts the scenes of §6.1.
func ParseScene(s string) (Scene, error) {
	switch sc := Scene(strings.ToUpper(strings.TrimSpace(s))); sc {
	case SceneRegister, SceneLogin, SceneLoginChallenge, ScenePasswordReset,
		SceneBindIdentity, SceneRebindIdentity, SceneStepUp, SceneWithdrawConfirm:
		return sc, nil
	default:
		return "", apperr.Invalid(fmt.Sprintf("unknown scene %q", s))
	}
}

// Authenticated reports whether the scene belongs to a signed-in user.
func (s Scene) Authenticated() bool {
	switch s {
	case SceneBindIdentity, SceneRebindIdentity, SceneStepUp, SceneWithdrawConfirm:
		return true
	default:
		return false
	}
}

// Errors of the OTP flow (appendix C). Wrong codes, unknown challenges and
// decoys all read AUTH_OTP_INVALID, so no response tells whether an
// account exists.
var (
	ErrOTPInvalid          = apperr.New(apperr.KindInvalid, "AUTH_OTP_INVALID", "the verification code is invalid")
	ErrOTPExpired          = apperr.New(apperr.KindInvalid, "AUTH_OTP_EXPIRED", "the verification code has expired")
	ErrOTPAttemptsExceeded = apperr.New(apperr.KindRateLimited, "AUTH_OTP_ATTEMPTS_EXCEEDED", "too many wrong codes; request a new one")
	ErrOTPResendTooSoon    = apperr.New(apperr.KindRateLimited, "AUTH_OTP_RESEND_TOO_SOON", "please wait before requesting another code")
	ErrTicketInvalid       = apperr.New(apperr.KindInvalid, "AUTH_TICKET_INVALID", "the verification ticket is invalid or expired")
	ErrCaptchaRequired     = apperr.New(apperr.KindInvalid, "AUTH_CAPTCHA_REQUIRED", "human verification is required")
	ErrCaptchaFailed       = apperr.New(apperr.KindForbidden, "AUTH_CAPTCHA_FAILED", "human verification failed")
	ErrChannelUnavailable  = apperr.New(apperr.KindForbidden, "AUTH_CHANNEL_UNAVAILABLE", "this channel is not available; use email")
	ErrDeviceRequired      = apperr.Invalid("device_id must be 8 to 64 characters")
)

// ValidDeviceID checks the client-generated device identifier.
func ValidDeviceID(id string) bool {
	return len(id) >= 8 && len(id) <= 64 && strings.TrimSpace(id) == id
}

// CodeHasher keys OTP hashes with a server secret (§5.3: HMAC-SHA256), so
// a database leak does not reveal codes.
type CodeHasher struct{ key []byte }

// NewCodeHasher returns a hasher; key must be at least 32 bytes.
func NewCodeHasher(key []byte) (CodeHasher, error) {
	if len(key) < 32 {
		return CodeHasher{}, fmt.Errorf("OTP HMAC key must be at least 32 bytes, got %d", len(key))
	}
	return CodeHasher{key: key}, nil
}

// Hash binds code to its challenge.
func (h CodeHasher) Hash(challengeID, code string) []byte {
	m := hmac.New(sha256.New, h.key)
	m.Write([]byte(challengeID + ":" + code))
	return m.Sum(nil)
}

// Match compares in constant time.
func (h CodeHasher) Match(hash []byte, challengeID, code string) bool {
	return hmac.Equal(hash, h.Hash(challengeID, code))
}

// NewCode returns six uniformly random digits.
func NewCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return fmt.Sprintf("%06d", n.Int64())
}

// RandomBytes returns n random bytes.
func RandomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

// NewToken returns an opaque token and the hash to store.
func NewToken() (string, []byte) {
	plain := base64.RawURLEncoding.EncodeToString(RandomBytes(32))
	return plain, HashToken(plain)
}

// HashToken is how opaque tokens are stored and looked up.
func HashToken(plain string) []byte {
	sum := sha256.Sum256([]byte(plain))
	return sum[:]
}

// Challenge statuses.
const (
	ChallengePending  = "PENDING"
	ChallengeVerified = "VERIFIED"
	ChallengeLocked   = "LOCKED"
)

// Challenge is one OTP send-and-verify session.
type Challenge struct {
	ID               string
	Scene            Scene
	Channel          Channel
	Target           string
	UserID           string
	LoginChallengeID string
	DeviceID         string
	CodeHash         []byte
	Attempts         int
	Status           string
	ExpiresAt        time.Time
	CreatedAt        time.Time
	VerifiedAt       time.Time
}

// Attempt checks code and advances the challenge: a correct code verifies
// it, a wrong one counts, and the fifth wrong one locks it. The caller
// persists the challenge whatever the result.
func (c *Challenge) Attempt(code, deviceID string, now time.Time, h CodeHasher) error {
	switch {
	case c.Status == ChallengeLocked:
		return ErrOTPAttemptsExceeded
	case c.Status != ChallengePending:
		return ErrOTPInvalid
	case !now.Before(c.ExpiresAt):
		return ErrOTPExpired
	}
	c.Attempts++
	if deviceID != c.DeviceID || !h.Match(c.CodeHash, c.ID, code) {
		if c.Attempts >= MaxAttempts {
			c.Status = ChallengeLocked
			return ErrOTPAttemptsExceeded
		}
		return ErrOTPInvalid
	}
	c.Status = ChallengeVerified
	c.VerifiedAt = now
	return nil
}

// Ticket proves a verified challenge to one */complete call.
type Ticket struct {
	Hash        []byte
	ChallengeID string
	Scene       Scene
	DeviceID    string
	ExpiresAt   time.Time
}
