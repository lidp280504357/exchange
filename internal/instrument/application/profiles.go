package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/lidp280504357/exchange/internal/instrument/domain"
	"github.com/lidp280504357/exchange/internal/instrument/ports"
)

// ProfileChange is an operator's new profile for an asset (ASTRA design
// §5.3): the text replaces the old; Logo, when set, replaces the logo and
// ClearLogo removes it, otherwise the logo stays.
type ProfileChange struct {
	DisplayName string
	Description map[string]string
	Links       map[string]string
	Logo        *domain.Logo
	ClearLogo   bool
}

// UpdateProfile changes an asset's profile: the text is checked, a logo
// prepared (domain.PrepareLogo cleans an SVG), and the change recorded in
// the history with the profile before and after it (a logo by type, size
// and digest).
func (s *Service) UpdateProfile(ctx context.Context, code string, ch ProfileChange, actor, reason string) (domain.AssetProfile, error) {
	if err := domain.ValidProfileReason(reason); err != nil {
		return domain.AssetProfile{}, err
	}
	next := domain.AssetProfile{Code: code, DisplayName: ch.DisplayName, Description: ch.Description, Links: ch.Links}
	if err := next.Validate(); err != nil {
		return domain.AssetProfile{}, err
	}
	var logo *domain.Logo
	switch {
	case ch.ClearLogo:
		logo = &domain.Logo{}
	case ch.Logo != nil:
		l, err := domain.PrepareLogo(*ch.Logo)
		if err != nil {
			return domain.AssetProfile{}, err
		}
		logo = &l
	}
	var saved domain.AssetProfile
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Profiles().GetForUpdate(ctx, code)
		if err != nil {
			return err
		}
		if cur == nil {
			return domain.ErrNotFound
		}
		var oldDigest string
		if logo != nil && cur.LogoSize > 0 {
			old, _, err := r.Profiles().Logo(ctx, code)
			if err != nil {
				return err
			}
			if old != nil {
				oldDigest = digest(old.Data)
			}
		}
		if saved, err = r.Profiles().Save(ctx, next, logo); err != nil {
			return err
		}
		change := map[string]any{"old": profileRecord(*cur, oldDigest, logo != nil), "new": profileRecord(saved, logoDigest(logo), logo != nil)}
		return r.Record(ctx, "ASSET_PROFILE", code, saved.Version, change, actor, reason, SourceProfile)
	})
	return saved, err
}

// profileRecord is a profile as the history keeps it: the logo by type,
// size and, when it changed, digest.
func profileRecord(p domain.AssetProfile, logoDigest string, logoChanged bool) map[string]any {
	out := map[string]any{
		"display_name": p.DisplayName, "description": p.Description, "links": p.Links,
		"logo_mime": p.LogoMIME, "logo_size": p.LogoSize,
	}
	if logoChanged && logoDigest != "" {
		out["logo_sha256"] = logoDigest
	}
	return out
}

func logoDigest(l *domain.Logo) string {
	if l == nil || len(l.Data) == 0 {
		return ""
	}
	return digest(l.Data)
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Profiles returns every asset's profile by code, without logos.
func (s *Service) Profiles(ctx context.Context) (map[string]domain.AssetProfile, error) {
	list, err := s.Store.Read().Profiles().List(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]domain.AssetProfile, len(list))
	for _, p := range list {
		out[p.Code] = p
	}
	return out, nil
}

// Logo returns an asset's logo and its profile version for the sites;
// ErrNotFound without a logo, and for a hidden test asset (ADR-0017).
func (s *Service) Logo(ctx context.Context, code string) (domain.Logo, int64, error) {
	r := s.Store.Read()
	if a, err := r.Assets().Get(ctx, code); err != nil || a == nil || a.Hidden {
		if err == nil {
			err = domain.ErrNotFound
		}
		return domain.Logo{}, 0, err
	}
	l, version, err := r.Profiles().Logo(ctx, code)
	if err != nil {
		return domain.Logo{}, 0, err
	}
	if l == nil {
		return domain.Logo{}, 0, domain.ErrNotFound
	}
	return *l, version, nil
}

// LogoURL is where the sites load an asset's logo: the public endpoint
// with the profile version, so a new logo gets a new URL; empty without a
// logo.
func LogoURL(p domain.AssetProfile) string {
	if p.LogoSize == 0 {
		return ""
	}
	return fmt.Sprintf("/v1/market/assets/%s/logo?v=%d", p.Code, p.Version)
}
