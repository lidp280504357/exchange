package application

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// The custody suspension's console side (C5.5 ⑯, review B4): when funds
// go missing on two custody checks wallet-service suspends the asset's
// withdrawals until a person lifts it. The console lists the suspended
// assets and an ADMIN lifts one with a reason; a withdrawal of a
// suspended asset is still approved, and its answer says it waits for the
// lift (the coordinator's decision of 2026-10-03).

// WithdrawalSuspensions lists the assets whose withdrawals are suspended.
func (s *Service) WithdrawalSuspensions(ctx context.Context, p Principal) ([]ports.Suspension, error) {
	if err := p.require(domain.PermWithdrawalsRead); err != nil {
		return nil, err
	}
	return s.Wallet.Suspensions(ctx)
}

// ResumeWithdrawals lifts an asset's suspension (ADMIN): its approved
// withdrawals go out within a round and the custody checks start over.
// wallet-service audits it (wallet.withdrawals.resume) in the ADMIN's
// name, as exchangectl wallet withdrawals-resume.
func (s *Service) ResumeWithdrawals(ctx context.Context, p Principal, asset, reason string) (ports.Suspension, error) {
	if err := p.require(domain.PermWithdrawalsResume); err != nil {
		return ports.Suspension{}, err
	}
	if err := needReason(reason); err != nil {
		return ports.Suspension{}, err
	}
	asset = strings.ToUpper(strings.TrimSpace(asset))
	if asset == "" {
		return ports.Suspension{}, apperr.Invalid("the asset is required")
	}
	return s.Wallet.Resume(ctx, asset, p.Admin.Email, strings.TrimSpace(reason))
}

// suspendedAssets are the suspended assets by code; nil when they cannot
// be read (a review's answer then says nothing of them).
func (s *Service) suspendedAssets(ctx context.Context) map[string]ports.Suspension {
	list, err := s.Wallet.Suspensions(ctx)
	if err != nil {
		s.Log.WarnContext(ctx, "withdrawals: read the suspensions", "error", err)
		return nil
	}
	out := make(map[string]ports.Suspension, len(list))
	for _, x := range list {
		out[x.Asset] = x
	}
	return out
}

// withSuspension adds to a reviewed withdrawal's answer that its asset's
// withdrawals are suspended (suspended_at, suspension_reason): approved
// all the same, it waits for the lift. It returns the answer and the
// suspension it found.
func withSuspension(raw []byte, suspended map[string]ports.Suspension) ([]byte, bool) {
	var w map[string]json.RawMessage
	if len(suspended) == 0 || json.Unmarshal(raw, &w) != nil {
		return raw, false
	}
	var asset string
	if json.Unmarshal(w["asset"], &asset) != nil {
		return raw, false
	}
	x, ok := suspended[asset]
	if !ok {
		return raw, false
	}
	w["suspended_at"], _ = json.Marshal(x.SuspendedAt.UTC().Format(time.RFC3339))
	w["suspension_reason"], _ = json.Marshal(x.Reason)
	out, err := json.Marshal(w)
	if err != nil {
		return raw, false
	}
	return out, true
}
