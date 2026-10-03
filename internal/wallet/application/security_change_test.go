package application

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/lidp280504357/exchange/internal/wallet/domain"
)

// TestAuthenticatorRemovedHoldsWithdrawals checks a withdrawal asked for
// within a day of the authenticator app's removal (by the user or an
// administrator) waits for a reviewer as a security change, the
// step-up's TOTPChanged carried through RequestWithdrawal (C5.5 ⑤,
// review ⑭); a day later it does not.
func TestAuthenticatorRemovedHoldsWithdrawals(t *testing.T) {
	w := newWithdrawHarness(t)
	ctx := context.Background()
	payee := "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359"
	if _, err := w.svc.AddAddress(ctx, "alice", AddressInput{Network: net, Address: payee, Label: "cold", StepUp: w.stepUp("s1", 2, false)}); err != nil {
		t.Fatal(err)
	}
	// An old account, an old address: only the removal can hold it.
	w.svc.W.Profiles = fakeProfiles{"alice": w.now.Add(-30 * 24 * time.Hour)}
	w.now = w.now.Add(4 * 24 * time.Hour)
	removedAt := w.now.Add(-time.Hour)
	stepUp := func(token string) string {
		su := w.stepUps.tokens[w.stepUp(token, 2, false)]
		su.TOTPChanged = removedAt
		w.stepUps.tokens[token] = su
		return token
	}
	req := WithdrawalInput{Asset: "ETH", Network: net, Address: payee, Amount: d("0.01"), StepUp: stepUp("s2")}
	wd, err := w.svc.RequestWithdrawal(ctx, "alice", req)
	if err != nil || wd.Status != domain.WithdrawalReview || !slices.Equal(wd.RiskReasons, []string{domain.RiskSecurityChange}) {
		t.Fatalf("an hour after the removal %+v %v", wd, err)
	}

	w.now = removedAt.Add(25 * time.Hour)
	req.StepUp = stepUp("s3")
	later, err := w.svc.RequestWithdrawal(ctx, "alice", req)
	if err != nil || slices.Contains(later.RiskReasons, domain.RiskSecurityChange) {
		t.Fatalf("a day after the removal %+v %v", later, err)
	}
}
