package application

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"strings"
	"testing"

	authv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/auth/v1"
	"github.com/lidp280504357/exchange/internal/auth/domain"
	"github.com/lidp280504357/exchange/internal/platform/secretbox"
)

func withTOTP(t *testing.T, a *accountFixture) {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	box, err := secretbox.New(base64.StdEncoding.EncodeToString(k))
	if err != nil {
		t.Fatal(err)
	}
	a.acc.TOTP = box
}

func TestAuthenticatorApps(t *testing.T) {
	a := newAccountFixture(t)
	withTOTP(t, a)
	tok := a.register(t, "totp@example.com")

	// Setting up needs a step-up and returns the secret; nothing is bound
	// until a code confirms it.
	_, _, err := a.acc.SetupTOTP(ctx, tok.UserID, "")
	wantCode(t, err, "AUTH_STEP_UP_REQUIRED")
	secretB32, uri, err := a.acc.SetupTOTP(ctx, tok.UserID, a.stepUp(t, tok, "EMAIL"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uri, "otpauth://totp/Exchange:t%2A%2A%2A@example.com?") || !strings.Contains(uri, "secret="+secretB32) {
		t.Fatalf("uri %s", uri)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secretB32)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := a.acc.TOTPStatus(ctx, tok.UserID); st.Enabled || !st.Pending {
		t.Fatalf("status after setup: %+v", st)
	}
	code := func() string { return domain.TOTPCode(secret, domain.TOTPStep(a.now)) }
	wantCode(t, a.acc.ConfirmTOTP(ctx, tok.UserID, "000000"), "AUTH_TOTP_INVALID")
	if err := a.acc.ConfirmTOTP(ctx, tok.UserID, code()); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.acc.TOTPStatus(ctx, tok.UserID); !st.Enabled {
		t.Fatalf("status after confirm: %+v", st)
	}
	if n := len(eventsOf[*authv1.TotpEnabled](a.store)); n != 1 {
		t.Fatalf("%d TotpEnabled events", n)
	}
	wantCode(t, a.acc.ConfirmTOTP(ctx, tok.UserID, code()), "AUTH_TOTP_NOT_PENDING")

	// Bound: step-ups need the app, a mailed code is refused.
	ticket := a.ticket(t, RequestOTP{Scene: "STEP_UP", Channel: "EMAIL", UserID: tok.UserID})
	_, _, err = a.acc.StepUp(ctx, tok.UserID, tok.SessionID, ticket, web(device))
	wantCode(t, err, "AUTH_TOTP_REQUIRED")
	// The confirming code was used: the same step is refused.
	_, _, err = a.acc.StepUpTOTP(ctx, tok.UserID, tok.SessionID, code())
	wantCode(t, err, "AUTH_TOTP_INVALID")
	a.now = a.now.Add(domain.TOTPPeriod)
	su, _, err := a.acc.StepUpTOTP(ctx, tok.UserID, tok.SessionID, code())
	if err != nil {
		t.Fatal(err)
	}
	step, sec, err := a.acc.ConsumeStepUp(ctx, tok.UserID, su)
	if err != nil || step.SessionID != tok.SessionID || step.Channel != domain.ChannelTOTP || !sec.TOTPEnabled || sec.Identities != 1 {
		t.Fatalf("consume: %+v %+v %v", step, sec, err)
	}
	_, _, err = a.acc.SetupTOTP(ctx, tok.UserID, a.totpStepUp(t, tok, secret))
	wantCode(t, err, "AUTH_TOTP_ENABLED")

	// Removing it needs a step-up proven with the app.
	if err := a.acc.DisableTOTP(ctx, tok.UserID, a.totpStepUp(t, tok, secret)); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.acc.TOTPStatus(ctx, tok.UserID); st.Enabled || st.Pending {
		t.Fatalf("status after disable: %+v", st)
	}
	if n := len(eventsOf[*authv1.TotpDisabled](a.store)); n != 1 {
		t.Fatalf("%d TotpDisabled events", n)
	}
	// Mailed codes work again.
	if su := a.stepUp(t, tok, "EMAIL"); su == "" {
		t.Fatal("no step-up")
	}
	wantCode(t, a.acc.DisableTOTP(ctx, tok.UserID, a.stepUp(t, tok, "EMAIL")), "AUTH_TOTP_NOT_ENABLED")
}

// totpStepUp steps up with the app at the next step.
func (a *accountFixture) totpStepUp(t *testing.T, tok Tokens, secret []byte) string {
	t.Helper()
	a.now = a.now.Add(domain.TOTPPeriod)
	su, _, err := a.acc.StepUpTOTP(ctx, tok.UserID, tok.SessionID, domain.TOTPCode(secret, domain.TOTPStep(a.now)))
	if err != nil {
		t.Fatalf("totp step up: %v", err)
	}
	return su
}

func TestTOTPNeedsItsKey(t *testing.T) {
	a := newAccountFixture(t)
	tok := a.register(t, "nokey@example.com")
	_, _, err := a.acc.SetupTOTP(ctx, tok.UserID, a.stepUp(t, tok, "EMAIL"))
	wantCode(t, err, "AUTH_TOTP_UNAVAILABLE")
	_, _, err = a.acc.StepUpTOTP(ctx, tok.UserID, tok.SessionID, "123456")
	wantCode(t, err, "AUTH_TOTP_NOT_ENABLED")
}
