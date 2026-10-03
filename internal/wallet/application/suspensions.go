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
// they are stopped already (the first suspension stands).
func suspend(ctx context.Context, store ports.Store, x domain.Suspension) (bool, error) {
	var done bool
	err := store.Tx(ctx, func(r ports.Repos) error {
		var err error
		if done, err = r.Suspensions().Put(ctx, x); err != nil || !done {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "asset:" + x.Asset, Action: "wallet.withdrawals.suspend", Actor: x.SuspendedBy, Reason: x.Reason,
			Details: fmt.Sprintf(`{"shortfall":%q}`, x.Shortfall.String()),
		}, x.SuspendedBy)
	})
	return done, err
}

// SuspendWithdrawals stops an asset's withdrawals for an operator: new
// requests are refused and approved ones wait until ResumeWithdrawals.
func SuspendWithdrawals(ctx context.Context, store ports.Store, asset, actor, reason string, now time.Time) (domain.Suspension, error) {
	asset = strings.ToUpper(strings.TrimSpace(asset))
	if asset == "" || strings.TrimSpace(actor) == "" || len(strings.TrimSpace(reason)) < 3 {
		return domain.Suspension{}, apperr.Invalid("an asset, an actor and a reason are required")
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

// ResumeWithdrawals lifts an asset's suspension for a person who found the
// cause, with an audit event; the approved withdrawals go out within a
// round.
func ResumeWithdrawals(ctx context.Context, store ports.Store, asset, actor, reason string) (domain.Suspension, error) {
	asset = strings.ToUpper(strings.TrimSpace(asset))
	if strings.TrimSpace(actor) == "" || len(strings.TrimSpace(reason)) < 3 {
		return domain.Suspension{}, apperr.Invalid("an actor and a reason are required")
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
		lifted = *x
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "asset:" + asset, Action: "wallet.withdrawals.resume", Actor: actor, Reason: reason,
			Details: fmt.Sprintf(`{"suspended_by":%q,"suspended_at":%q,"shortfall":%q}`, x.SuspendedBy,
				x.SuspendedAt.UTC().Format(time.RFC3339), x.Shortfall.String()),
		}, actor)
	})
	return lifted, err
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
