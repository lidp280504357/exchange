package application

import (
	"context"

	"github.com/skill/exchange/internal/wallet/domain"
)

// Suspensions lists the assets whose withdrawals are suspended, for the
// admin console (C5.5 ⑯: the custody suspension's console side).
func (s *Service) Suspensions(ctx context.Context) ([]domain.Suspension, error) {
	return s.Store.Read().Suspensions().List(ctx)
}

// Withdrawable reports whether an asset has a network to withdraw it on,
// as SuspendWithdrawals checks.
func (s *Service) Withdrawable(ctx context.Context, asset string) (bool, error) {
	nets, err := s.Networks.ForAsset(ctx, asset)
	return len(nets) > 0, err
}
