package application

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skill/exchange/internal/instrument/domain"
	"github.com/skill/exchange/internal/instrument/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// profileTTL is how long the platform profile is served from memory: the
// sites read it every minute from every page, and a change made here
// drops it at once.
const profileTTL = 5 * time.Second

// Platform keeps the platform's profile (design 2026-10-04 §4.1): what
// the sites show of the exchange itself, changed by operators, and the
// welcome credits the ledger grants, as last read from it.
type Platform struct {
	Store ports.Store
	// Now is the clock; time.Now when nil.
	Now func() time.Time

	mu       sync.Mutex
	cached   *domain.PlatformProfile
	cachedAt time.Time
	credits  []domain.Credit
	known    bool
}

func (p *Platform) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Profile returns the profile, read at most profileTTL ago.
func (p *Platform) Profile(ctx context.Context) (domain.PlatformProfile, error) {
	p.mu.Lock()
	if p.cached != nil && p.now().Sub(p.cachedAt) < profileTTL {
		defer p.mu.Unlock()
		return *p.cached, nil
	}
	p.mu.Unlock()
	prof, err := p.Store.Read().Platform().Get(ctx)
	if err != nil {
		return domain.PlatformProfile{}, err
	}
	p.mu.Lock()
	p.cached, p.cachedAt = &prof, p.now()
	p.mu.Unlock()
	return prof, nil
}

// drop forgets the cached profile after a change.
func (p *Platform) drop() {
	p.mu.Lock()
	p.cached = nil
	p.mu.Unlock()
}

// WelcomeCredits returns the welcome credits as last read from the
// ledger; known is false before the first read.
func (p *Platform) WelcomeCredits() (credits []domain.Credit, known bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.credits, p.known
}

// RefreshWelcomeCredits reads the welcome credits from the ledger; on a
// failure the last ones stay.
func (p *Platform) RefreshWelcomeCredits(ctx context.Context, l ports.Ledger) error {
	credits, err := l.WelcomeCredits(ctx)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.credits, p.known = credits, true
	p.mu.Unlock()
	return nil
}

// UpdateProfile replaces the profile's text and settings (images and the
// welcome credits aside) on the version expected, INSTRUMENT_PLATFORM_
// CHANGED on another; the history keeps the profile before and after.
func (p *Platform) UpdateProfile(ctx context.Context, next domain.PlatformProfile, expected int64, actor, reason string) (domain.PlatformProfile, error) {
	if err := validChange(actor, reason); err != nil {
		return domain.PlatformProfile{}, err
	}
	next.Normalize()
	if err := next.Validate(); err != nil {
		return domain.PlatformProfile{}, err
	}
	var saved domain.PlatformProfile
	err := p.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Platform().GetForUpdate(ctx)
		if err != nil {
			return err
		}
		if cur.Version != expected {
			return domain.ErrPlatformChanged
		}
		next.UpdatedBy, next.UpdatedAt = actor, p.now().UTC()
		if saved, err = r.Platform().Save(ctx, next); err != nil {
			return err
		}
		change := map[string]any{"old": platformRecord(cur), "new": platformRecord(saved)}
		return r.Record(ctx, "PLATFORM_PROFILE", "platform", saved.Version, change, actor, reason, SourceProfile)
	})
	if err != nil {
		return domain.PlatformProfile{}, err
	}
	p.drop()
	return saved, nil
}

// SetImage stores an image of a kind (domain.PreparePlatformImage checks
// and cleans it) or, with img nil, removes it so the sites show their
// own; either moves the version on, so the image's URL changes. Removing
// an image that is not there changes nothing.
func (p *Platform) SetImage(ctx context.Context, kind string, img *domain.Logo, actor, reason string) (domain.PlatformProfile, error) {
	if !domain.ValidImageKind(kind) {
		return domain.PlatformProfile{}, apperr.NotFound("no such image")
	}
	if err := validChange(actor, reason); err != nil {
		return domain.PlatformProfile{}, err
	}
	var info domain.PlatformImage
	if img != nil {
		clean, i, err := domain.PreparePlatformImage(kind, *img)
		if err != nil {
			return domain.PlatformProfile{}, err
		}
		img, info = &clean, i
	}
	var saved domain.PlatformProfile
	err := p.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Platform().GetForUpdate(ctx)
		if err != nil {
			return err
		}
		old, had := cur.Images[kind]
		if img == nil && !had {
			saved = cur
			return nil
		}
		now := p.now().UTC()
		if err := r.Platform().SaveImage(ctx, kind, img, info, now); err != nil {
			return err
		}
		cur.UpdatedBy, cur.UpdatedAt = actor, now
		if saved, err = r.Platform().Save(ctx, cur); err != nil {
			return err
		}
		change := map[string]any{"image": kind, "old": imageRecord(old, had, nil), "new": imageRecord(info, img != nil, img)}
		return r.Record(ctx, "PLATFORM_PROFILE", "platform", saved.Version, change, actor, reason, SourceProfile)
	})
	if err != nil {
		return domain.PlatformProfile{}, err
	}
	p.drop()
	return saved, nil
}

// Image returns an uploaded image and the profile version it belongs to;
// ErrNotFound without one.
func (p *Platform) Image(ctx context.Context, kind string) (domain.Logo, int64, error) {
	if !domain.ValidImageKind(kind) {
		return domain.Logo{}, 0, domain.ErrNotFound
	}
	img, version, err := p.Store.Read().Platform().Image(ctx, kind)
	if err != nil {
		return domain.Logo{}, 0, err
	}
	if img == nil {
		return domain.Logo{}, 0, domain.ErrNotFound
	}
	return *img, version, nil
}

func validChange(actor, reason string) error {
	if strings.TrimSpace(actor) == "" {
		return apperr.Invalid("an actor is required")
	}
	return domain.ValidProfileReason(reason)
}

// platformRecord is the profile as the history keeps it.
func platformRecord(p domain.PlatformProfile) map[string]any {
	social := make([]map[string]string, 0, len(p.Social))
	for _, s := range p.Social {
		social = append(social, map[string]string{"kind": s.Kind, "url": s.URL})
	}
	return map[string]any{
		"name": p.Name, "short_name": p.ShortName, "domain": p.Domain, "theme_color": p.ThemeColor, "brand_color": p.BrandColor,
		"footer":  map[string]any{"copyright": p.Footer.Copyright, "compliance": p.Footer.Compliance},
		"contact": map[string]any{"email": p.Contact.Email, "support_url": p.Contact.SupportURL},
		"social":  social, "default_locale": p.DefaultLocale,
		"learning_mode": map[string]any{"enabled": p.Learning.Enabled, "text": p.Learning.Text},
		"registration":  map[string]any{"status": p.Registration.Status, "closed_text": p.Registration.ClosedText},
	}
}

// imageRecord is an image as the history keeps it: its type, size and
// width, and the digest of a new one.
func imageRecord(i domain.PlatformImage, present bool, data *domain.Logo) map[string]any {
	if !present {
		return nil
	}
	out := map[string]any{"mime": i.MIME, "size": i.Size, "width": i.Width}
	if data != nil {
		out["sha256"] = digest(data.Data)
	}
	return out
}

// PlatformImageURL is where the sites load an image of the profile: the
// public endpoint with the version, so a new image gets a new URL; empty
// without one.
func PlatformImageURL(p domain.PlatformProfile, kind string) string {
	if _, ok := p.Images[kind]; !ok {
		return ""
	}
	return "/v1/platform/images/" + kind + "?v=" + strconv.FormatInt(p.Version, 10)
}
