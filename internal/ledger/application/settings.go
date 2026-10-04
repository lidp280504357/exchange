package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	"github.com/skill/exchange/internal/ledger/domain"
	"github.com/skill/exchange/internal/ledger/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
)

// welcomeTTL is how long a grant uses the welcome credits it read: a
// change applies to the accounts opened at most this much later.
const welcomeTTL = 30 * time.Second

// welcomeCache keeps the welcome credits the grants read.
type welcomeCache struct {
	mu      sync.Mutex
	readAt  time.Time
	credits []Credit
}

// SeedWelcomeCredits stores the welcome credits of the environment
// (WELCOME_FUNDS) as the first value unless there is one: from then on
// operators change them (design 2026-10-04 §4.2) and the environment is
// read no more.
func (s *Service) SeedWelcomeCredits(ctx context.Context, credits []Credit) error {
	w := domain.WelcomeCredits{Credits: credits, Version: 1, UpdatedBy: domain.WelcomeFromEnv, UpdatedAt: s.Now().UTC()}
	if err := domain.ValidWelcomeCredits(credits); err != nil {
		return fmt.Errorf("WELCOME_FUNDS: %w", err)
	}
	seeded, err := s.Store.Read().Settings().SeedWelcomeCredits(ctx, w)
	if err != nil {
		return err
	}
	if seeded && s.Log != nil {
		s.Log.InfoContext(ctx, "welcome credits taken from WELCOME_FUNDS", "credits", creditsText(credits))
	}
	return nil
}

// WelcomeCredits returns the welcome credits as stored.
func (s *Service) WelcomeCredits(ctx context.Context) (domain.WelcomeCredits, error) {
	w, err := s.Store.Read().Settings().WelcomeCredits(ctx)
	if err != nil {
		return domain.WelcomeCredits{}, err
	}
	if w == nil {
		return domain.WelcomeCredits{Credits: []Credit{}}, nil
	}
	return *w, nil
}

// grantCredits returns the welcome credits a grant gives, read at most
// welcomeTTL ago.
func (s *Service) grantCredits(ctx context.Context) ([]Credit, error) {
	c := &s.welcome
	c.mu.Lock()
	defer c.mu.Unlock()
	now := s.Now()
	if !c.readAt.IsZero() && now.Sub(c.readAt) < welcomeTTL {
		return c.credits, nil
	}
	w, err := s.WelcomeCredits(ctx)
	if err != nil {
		return nil, err
	}
	c.credits, c.readAt = w.Credits, now
	return c.credits, nil
}

// SetWelcomeCredits replaces the welcome credits (an empty list grants
// nothing) on the version expected, LEDGER_SETTINGS_CHANGED on another;
// the change is audited as ledger.settings.welcome_credits with the
// credits before and after. Whether a raise needs a second operator is
// the admin console's rule (design §5): the ledger records what it is
// given.
func (s *Service) SetWelcomeCredits(ctx context.Context, credits []Credit, expected int64, actor, reason string) (domain.WelcomeCredits, error) {
	if err := domain.ValidSettingChange(actor, reason); err != nil {
		return domain.WelcomeCredits{}, err
	}
	if err := domain.ValidWelcomeCredits(credits); err != nil {
		return domain.WelcomeCredits{}, err
	}
	for _, c := range credits {
		decimals, err := s.Assets.Decimals(ctx, c.Asset)
		if apperr.Is(err, apperr.CodeNotFound) {
			return domain.WelcomeCredits{}, apperr.Invalid(fmt.Sprintf("credits: no asset %s", c.Asset))
		}
		if err != nil {
			return domain.WelcomeCredits{}, err
		}
		if !c.Amount.Equal(c.Amount.Truncate(decimals)) {
			return domain.WelcomeCredits{}, apperr.Invalid(fmt.Sprintf("credits: %s has %d decimals", c.Asset, decimals))
		}
	}
	var saved domain.WelcomeCredits
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Settings().WelcomeCreditsForUpdate(ctx)
		if err != nil {
			return err
		}
		old := domain.WelcomeCredits{Credits: []Credit{}}
		if cur != nil {
			old = *cur
		}
		if expected != old.Version {
			return domain.ErrSettingsChanged
		}
		saved = domain.WelcomeCredits{Credits: credits, Version: old.Version + 1, UpdatedBy: actor, UpdatedAt: s.Now().UTC()}
		if err := r.Settings().SaveWelcomeCredits(ctx, saved); err != nil {
			return err
		}
		details, err := json.Marshal(map[string]any{"old": creditsJSON(old.Credits), "new": creditsJSON(credits), "version": saved.Version})
		if err != nil {
			return err
		}
		return r.Emit(ctx, event.TopicAudit, &auditv1.AdminActionPerformed{
			Target: "ledger:settings", Action: "ledger.settings.welcome_credits", Actor: actor, Reason: reason, Details: string(details),
		}, "actor", actor)
	})
	if err != nil {
		return domain.WelcomeCredits{}, err
	}
	s.welcome.mu.Lock()
	s.welcome.readAt = time.Time{} // this instance's grants read the change at once
	s.welcome.mu.Unlock()
	return saved, nil
}

func creditsJSON(list []Credit) []map[string]string {
	out := make([]map[string]string, 0, len(list))
	for _, c := range list {
		out = append(out, map[string]string{"asset": c.Asset, "amount": c.Amount.String()})
	}
	return out
}

func creditsText(list []Credit) string {
	parts := make([]string, 0, len(list))
	for _, c := range list {
		parts = append(parts, c.Asset+":"+c.Amount.String())
	}
	return strings.Join(parts, ",")
}
