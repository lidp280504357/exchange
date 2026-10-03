package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
)

// suspend stops an asset's withdrawals with an audit event; false when
// they are stopped already (the first suspension stands). A suspicion the
// custody checks kept of the asset is spent either way.
func suspend(ctx context.Context, store ports.Store, x domain.Suspension) (bool, error) {
	var done bool
	err := store.Tx(ctx, func(r ports.Repos) error {
		var err error
		if done, err = r.Suspensions().Put(ctx, x); err != nil {
			return err
		}
		if err := r.Suspensions().Clear(ctx, x.Asset, false); err != nil || !done {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "asset:" + x.Asset, Action: "wallet.withdrawals.suspend", Actor: x.SuspendedBy, Reason: x.Reason,
			Details: fmt.Sprintf(`{"shortfall":%q}`, x.Shortfall.String()),
		}, x.SuspendedBy)
	})
	return done, err
}

// Withdrawable reports whether an asset has a network to withdraw it on.
type Withdrawable func(ctx context.Context, asset string) (bool, error)

// SuspendWithdrawals stops an asset's withdrawals for an operator: new
// requests are refused and approved ones wait until ResumeWithdrawals.
// The asset must have a network to withdraw it on.
func SuspendWithdrawals(ctx context.Context, store ports.Store, withdrawable Withdrawable, asset, actor, reason string, now time.Time) (domain.Suspension, error) {
	asset = strings.ToUpper(strings.TrimSpace(asset))
	if asset == "" || strings.TrimSpace(actor) == "" || len(strings.TrimSpace(reason)) < 3 {
		return domain.Suspension{}, apperr.Invalid("an asset, an actor and a reason are required")
	}
	ok, err := withdrawable(ctx, asset)
	if err != nil {
		return domain.Suspension{}, err
	}
	if !ok {
		return domain.Suspension{}, apperr.NotFound("no network withdraws " + asset)
	}
	x := domain.Suspension{Asset: asset, Shortfall: decimal.Zero, Reason: reason, SuspendedBy: actor, SuspendedAt: now}
	done, err := suspend(ctx, store, x)
	if err != nil {
		return domain.Suspension{}, err
	}
	if !done {
		return domain.Suspension{}, apperr.New(apperr.KindConflict, apperr.CodeConflict, "withdrawals of "+asset+" are suspended already")
	}
	return x, nil
}

// Resume is a person lifting an asset's suspension, and why. Accept is a
// difference they accept, for AcceptFor (at most domain.MaxAcceptFor):
// until then the custody checks count only what is missing beyond it, so
// a standing difference they looked into does not suspend the asset again
// an hour later (review of ebb8aaa, H2); zero keeps what was accepted
// before.
type Resume struct {
	Asset, Actor, Reason string
	Accept               decimal.Decimal
	AcceptFor            time.Duration
}

// ResumeWithdrawals lifts an asset's suspension for a person who found the
// cause, with an audit event; the approved withdrawals go out within a
// round. The checks start over: funds still missing are suspected anew and
// suspend the asset again only on two checks.
func ResumeWithdrawals(ctx context.Context, store ports.Store, in Resume, now time.Time) (domain.Suspension, error) {
	asset := strings.ToUpper(strings.TrimSpace(in.Asset))
	if strings.TrimSpace(in.Actor) == "" || len(strings.TrimSpace(in.Reason)) < 3 {
		return domain.Suspension{}, apperr.Invalid("an actor and a reason are required")
	}
	if in.Accept.IsNegative() || in.Accept.IsPositive() && (in.AcceptFor <= 0 || in.AcceptFor > domain.MaxAcceptFor) {
		return domain.Suspension{}, apperr.Invalid(fmt.Sprintf("a difference is accepted for a while, at most %s", domain.MaxAcceptFor))
	}
	var lifted domain.Suspension
	err := store.Tx(ctx, func(r ports.Repos) error {
		x, err := r.Suspensions().Get(ctx, asset)
		if err != nil {
			return err
		}
		if x == nil {
			return apperr.NotFound("withdrawals of " + asset + " are not suspended")
		}
		if _, err := r.Suspensions().Delete(ctx, asset); err != nil {
			return err
		}
		if err := r.Suspensions().Clear(ctx, asset, false); err != nil {
			return err
		}
		accepted := ""
		if in.Accept.IsPositive() {
			until := now.Add(in.AcceptFor)
			if err := r.Suspensions().Accept(ctx, asset, in.Accept, until, in.Actor); err != nil {
				return err
			}
			accepted = fmt.Sprintf(`,"accepted":%q,"accepted_until":%q`, in.Accept.String(), until.UTC().Format(time.RFC3339))
		}
		lifted = *x
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "asset:" + asset, Action: "wallet.withdrawals.resume", Actor: in.Actor, Reason: in.Reason,
			Details: fmt.Sprintf(`{"suspended_by":%q,"suspended_at":%q,"shortfall":%q%s}`, x.SuspendedBy,
				x.SuspendedAt.UTC().Format(time.RFC3339), x.Shortfall.String(), accepted),
		}, in.Actor)
	})
	return lifted, err
}

// WithdrawalsSuspended is the set of assets whose withdrawals are
// suspended (the public networks show them closed).
func (s *Service) WithdrawalsSuspended(ctx context.Context) (map[string]bool, error) {
	return suspendedAssets(ctx, s.Store.Read())
}

// suspendedAssets is the set of assets whose withdrawals are suspended.
func suspendedAssets(ctx context.Context, r ports.Repos) (map[string]bool, error) {
	list, err := r.Suspensions().List(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(list))
	for _, x := range list {
		out[x.Asset] = true
	}
	return out, nil
}
