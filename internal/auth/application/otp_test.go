package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	authv1 "github.com/skill/exchange/api/gen/go/exchange/auth/v1"
	"github.com/skill/exchange/internal/auth/domain"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
)

const device = "device-0001"

type fixture struct {
	svc      *OTPService
	store    *memStore
	notifier *fakeNotifier
	flags    fakeFlags
	now      time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	h, err := domain.NewCodeHasher(domain.RandomBytes(32))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{store: newMemStore(), notifier: &fakeNotifier{}, flags: fakeFlags{}, now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	f.svc = &OTPService{
		Store:    f.store,
		Notifier: f.notifier,
		Captcha:  fakeCaptcha{},
		Limiter:  &memLimiter{},
		Flags:    f.flags,
		Hasher:   h,
		SMS:      SMSBudget{Hourly: 100, Daily: 1000},
		Log:      slog.New(slog.DiscardHandler),
		Now:      func() time.Time { return f.now },
		Dispatch: func(fn func(context.Context)) { fn(context.Background()) },
	}
	f.store.identities = []domain.Identity{{ID: "i-1", UserID: "u-1", Kind: "EMAIL", Value: "known@example.com"}}
	return f
}

func (f *fixture) request(t *testing.T, scene, channel, identifier string) (ChallengeView, error) {
	t.Helper()
	return f.svc.Request(context.Background(), RequestOTP{
		Scene: scene, Channel: channel, Identifier: identifier, CaptchaToken: "human", DeviceID: device, IP: "203.0.113.9",
	})
}

func (f *fixture) verify(challengeID, code string) (TicketView, error) {
	return f.svc.Verify(context.Background(), VerifyOTP{ChallengeID: challengeID, Code: code, DeviceID: device})
}

func TestRegisterCodeFlow(t *testing.T) {
	f := newFixture(t)
	view, err := f.request(t, "REGISTER", "EMAIL", " New@Example.com ")
	if err != nil || view.Delivery != "QUEUED" || !view.ExpiresAt.Equal(f.now.Add(domain.CodeTTL)) {
		t.Fatalf("request: %+v %v", view, err)
	}
	sent, ok := f.notifier.last()
	if !ok || sent.Target != "new@example.com" || sent.Scene != domain.SceneRegister || len(sent.Code) != 6 {
		t.Fatalf("delivery: %+v", sent)
	}
	if _, isRequested := f.store.events[0].(*authv1.OtpRequested); !isRequested {
		t.Fatalf("event: %T", f.store.events[0])
	}

	ticket, err := f.verify(view.ChallengeID, sent.Code)
	if err != nil || ticket.Scene != domain.SceneRegister || ticket.Ticket == "" {
		t.Fatalf("verify: %+v %v", ticket, err)
	}
	var redeemed *domain.Challenge
	err = f.store.Tx(context.Background(), func(r portsRepos) error {
		c, err := Redeem(context.Background(), r, ticket.Ticket, domain.SceneRegister, device, f.now)
		redeemed = c
		return err
	})
	if err != nil || redeemed.Target != "new@example.com" {
		t.Fatalf("redeem: %+v %v", redeemed, err)
	}
	err = f.store.Tx(context.Background(), func(r portsRepos) error {
		_, err := Redeem(context.Background(), r, ticket.Ticket, domain.SceneRegister, device, f.now)
		return err
	})
	if !errors.Is(err, domain.ErrTicketInvalid) {
		t.Fatalf("a ticket works once: %v", err)
	}
}

func TestDecoysLookTheSame(t *testing.T) {
	cases := map[string]struct {
		scene, identifier string
		decoy             bool
	}{
		"register taken":       {"REGISTER", "known@example.com", true},
		"register free":        {"REGISTER", "free@example.com", false},
		"login unknown":        {"LOGIN", "ghost@example.com", true},
		"login known":          {"LOGIN", "known@example.com", false},
		"password reset ghost": {"PASSWORD_RESET", "ghost@example.com", true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			view, err := f.request(t, tc.scene, "EMAIL", tc.identifier)
			if err != nil || view.Delivery != "QUEUED" {
				t.Fatalf("every request looks the same: %+v %v", view, err)
			}
			sent, delivered := f.notifier.last()
			if delivered == tc.decoy {
				t.Fatalf("decoy=%v but delivered=%v", tc.decoy, delivered)
			}
			if !tc.decoy && tc.scene == "LOGIN" && sent.UserID != "u-1" {
				t.Fatalf("a login code carries the known user: %+v", sent)
			}
			if tc.decoy {
				// No code can verify a decoy; the error is the ordinary one.
				for _, code := range []string{"000000", "123456"} {
					if _, err := f.verify(view.ChallengeID, code); !errors.Is(err, domain.ErrOTPInvalid) {
						t.Fatalf("decoy verify: %v", err)
					}
				}
			}
		})
	}
}

func TestHumanVerification(t *testing.T) {
	f := newFixture(t)
	req := RequestOTP{Scene: "REGISTER", Channel: "EMAIL", Identifier: "a@example.com", DeviceID: device}
	if _, err := f.svc.Request(context.Background(), req); !apperr.Is(err, "AUTH_CAPTCHA_REQUIRED") {
		t.Fatalf("missing token: %v", err)
	}
	req.CaptchaToken = "robot"
	if _, err := f.svc.Request(context.Background(), req); !apperr.Is(err, "AUTH_CAPTCHA_FAILED") {
		t.Fatalf("bad token: %v", err)
	}
	if _, delivered := f.notifier.last(); delivered {
		t.Fatal("nothing may be sent without human verification")
	}
}

func TestSMSNeedsItsFlag(t *testing.T) {
	f := newFixture(t)
	if _, err := f.request(t, "REGISTER", "SMS", "+8613812341234"); !apperr.Is(err, "AUTH_CHANNEL_UNAVAILABLE") {
		t.Fatalf("SMS is off by default: %v", err)
	}
	f.flags[flags.KeyRegistrationSMS] = flags.Flag{Enabled: true, Rules: flags.Rules{Regions: &flags.List{Deny: []string{"CN"}}}}
	if _, err := f.request(t, "REGISTER", "SMS", "+8613812341234"); !apperr.Is(err, "AUTH_CHANNEL_UNAVAILABLE") {
		t.Fatalf("a denied region stays email-only: %v", err)
	}
	if _, err := f.request(t, "REGISTER", "SMS", "+14155552671"); err != nil {
		t.Fatalf("an allowed region may use SMS: %v", err)
	}
	if sent, _ := f.notifier.last(); sent.Channel != domain.ChannelSMS || sent.Target != "+14155552671" {
		t.Fatalf("delivery: %+v", sent)
	}
}

func TestResendInterval(t *testing.T) {
	f := newFixture(t)
	if _, err := f.request(t, "REGISTER", "EMAIL", "a@example.com"); err != nil {
		t.Fatal(err)
	}
	_, err := f.request(t, "REGISTER", "EMAIL", "a@example.com")
	if !apperr.Is(err, "AUTH_OTP_RESEND_TOO_SOON") || apperr.From(err).Details["retry_after_seconds"] == nil {
		t.Fatalf("second request within 60s: %v", err)
	}
	if _, err := f.request(t, "REGISTER", "EMAIL", "b@example.com"); err != nil {
		t.Fatalf("another target is independent: %v", err)
	}
}

func TestWrongCodesLockTheChallenge(t *testing.T) {
	f := newFixture(t)
	view, err := f.request(t, "REGISTER", "EMAIL", "a@example.com")
	if err != nil {
		t.Fatal(err)
	}
	sent, _ := f.notifier.last()
	for i := 1; i < domain.MaxAttempts; i++ {
		if _, err := f.verify(view.ChallengeID, "999999"); !errors.Is(err, domain.ErrOTPInvalid) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, err := f.verify(view.ChallengeID, "999999"); !errors.Is(err, domain.ErrOTPAttemptsExceeded) {
		t.Fatalf("fifth: %v", err)
	}
	if _, err := f.verify(view.ChallengeID, sent.Code); !errors.Is(err, domain.ErrOTPAttemptsExceeded) {
		t.Fatalf("the right code after the lock: %v", err)
	}
	if c := f.store.challenges[view.ChallengeID]; c.Attempts != domain.MaxAttempts || c.Status != domain.ChallengeLocked {
		t.Fatalf("attempts must be persisted: %+v", c)
	}
	if _, err := f.verify("not-a-uuid", "123456"); !errors.Is(err, domain.ErrOTPInvalid) {
		t.Fatalf("garbage challenge: %v", err)
	}
}

func TestExpiredCode(t *testing.T) {
	f := newFixture(t)
	view, _ := f.request(t, "REGISTER", "EMAIL", "a@example.com")
	sent, _ := f.notifier.last()
	f.now = f.now.Add(domain.CodeTTL + time.Second)
	if _, err := f.verify(view.ChallengeID, sent.Code); !errors.Is(err, domain.ErrOTPExpired) {
		t.Fatalf("expired: %v", err)
	}
}

func TestTicketsAreBoundToSceneAndDevice(t *testing.T) {
	f := newFixture(t)
	view, _ := f.request(t, "REGISTER", "EMAIL", "a@example.com")
	sent, _ := f.notifier.last()
	ticket, err := f.verify(view.ChallengeID, sent.Code)
	if err != nil {
		t.Fatal(err)
	}
	for name, try := range map[string]struct {
		scene  domain.Scene
		device string
	}{
		"other scene":  {domain.SceneLogin, device},
		"other device": {domain.SceneRegister, "device-9999"},
	} {
		err := f.store.Tx(context.Background(), func(r portsRepos) error {
			_, err := Redeem(context.Background(), r, ticket.Ticket, try.scene, try.device, f.now)
			return err
		})
		if !errors.Is(err, domain.ErrTicketInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	f.now = f.now.Add(domain.TicketTTL + time.Second)
	err = f.store.Tx(context.Background(), func(r portsRepos) error {
		_, err := Redeem(context.Background(), r, ticket.Ticket, domain.SceneRegister, device, f.now)
		return err
	})
	if !errors.Is(err, domain.ErrTicketInvalid) {
		t.Fatalf("expired ticket: %v", err)
	}
}

func TestAuthenticatedScenes(t *testing.T) {
	f := newFixture(t)
	if _, err := f.request(t, "STEP_UP", "EMAIL", ""); !apperr.Is(err, apperr.CodeUnauthorized) {
		t.Fatalf("step-up needs a user: %v", err)
	}
	_, err := f.svc.Request(context.Background(), RequestOTP{
		Scene: "STEP_UP", Channel: "EMAIL", CaptchaToken: "human", DeviceID: device, UserID: "u-1",
	})
	if err != nil {
		t.Fatalf("step-up: %v", err)
	}
	if sent, _ := f.notifier.last(); sent.Target != "known@example.com" || sent.UserID != "u-1" {
		t.Fatalf("a step-up code goes to the bound identity: %+v", sent)
	}
	_, err = f.svc.Request(context.Background(), RequestOTP{
		Scene: "STEP_UP", Channel: "SMS", CaptchaToken: "human", DeviceID: device, UserID: "u-1",
	})
	if err == nil {
		t.Fatal("no phone is bound, so an SMS step-up must fail")
	}
}

type fakeOTPMetrics struct {
	requests, verifications []string
}

func (m *fakeOTPMetrics) Requested(scene, channel, outcome string) {
	m.requests = append(m.requests, scene+"/"+channel+"/"+outcome)
}

func (m *fakeOTPMetrics) Verified(outcome string) { m.verifications = append(m.verifications, outcome) }

func TestOTPMetrics(t *testing.T) {
	f := newFixture(t)
	m := &fakeOTPMetrics{}
	f.svc.Metrics = m

	view, err := f.request(t, "REGISTER", "EMAIL", "counted@example.com")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.request(t, "REGISTER", "EMAIL", "counted@example.com")  // too soon
	_, _ = f.request(t, "LOGIN", "EMAIL", "nobody-here@example.com") // decoy
	_, _ = f.request(t, "HACK", "PIGEON", "x@example.com")
	_, _ = f.svc.Request(context.Background(), RequestOTP{
		Scene: "LOGIN", Channel: "EMAIL", Identifier: "known@example.com", CaptchaToken: "bot", DeviceID: device,
	})
	sent, _ := f.notifier.last()
	_, _ = f.verify(view.ChallengeID, "000000")
	_, _ = f.verify(view.ChallengeID, sent.Code)

	want := []string{
		"REGISTER/EMAIL/queued", "REGISTER/EMAIL/rate_limited", "LOGIN/EMAIL/decoy",
		"invalid/invalid/invalid", "LOGIN/EMAIL/captcha_rejected",
	}
	if strings.Join(m.requests, " ") != strings.Join(want, " ") {
		t.Fatalf("requests: %v", m.requests)
	}
	if strings.Join(m.verifications, " ") != "invalid verified" {
		t.Fatalf("verifications: %v", m.verifications)
	}
}

func TestTestersSkipOnlyTheIPQuota(t *testing.T) {
	f := newFixture(t)
	f.svc.Tester = func(token string) bool { return token == "tester" }
	send := func(i int, token string) error {
		_, err := f.svc.Request(context.Background(), RequestOTP{
			Scene: "REGISTER", Channel: "EMAIL", Identifier: fmt.Sprintf("ip-%d-%s@example.com", i, token),
			CaptchaToken: token, DeviceID: fmt.Sprintf("device-%04d-%s", i, token), IP: "198.51.100.7",
		})
		return err
	}
	f.svc.Captcha = acceptAll{}
	for i := range RuleIPHourly.Limit + 5 {
		if err := send(i, "tester"); err != nil {
			t.Fatalf("tester request %d: %v", i, err)
		}
	}
	for i := range RuleIPHourly.Limit {
		if err := send(i, "human"); err != nil {
			t.Fatalf("human request %d: %v", i, err)
		}
	}
	if err := send(RuleIPHourly.Limit, "human"); !apperr.Is(err, apperr.CodeRateLimited) {
		t.Fatalf("the %dth request of an IP: %v", RuleIPHourly.Limit+1, err)
	}
}

type acceptAll struct{}

func (acceptAll) Verify(context.Context, string, string) error { return nil }
