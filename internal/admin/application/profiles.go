package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// Asset profiles (ASTRA design §5.3, C4c): the name, introductions, links
// and logo the sites show for an asset. instrument-service checks them
// (lengths, https links, the logo's type, size and squareness; an SVG is
// rebuilt from an allow list) and versions them: a new logo gets a new URL,
// so the sites show it within a minute.

var assetCode = regexp.MustCompile(`^[A-Z0-9]{2,10}$`)

// maxLogo bounds an uploaded logo's bytes (instrument-service's limit).
const maxLogo = 200 << 10

// ProfileInput is an asset's profile as the console sends it: Logo is
// base64 (with LogoMIME) to replace the logo; ClearLogo removes it.
type ProfileInput struct {
	DisplayName string            `json:"display_name"`
	Description map[string]string `json:"description"`
	Links       map[string]string `json:"links"`
	Logo        string            `json:"logo"`
	LogoMIME    string            `json:"logo_mime"`
	ClearLogo   bool              `json:"clear_logo"`
}

func profileCode(code string) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if !assetCode.MatchString(code) {
		return "", apperr.NotFound("no such asset")
	}
	return code, nil
}

// AssetProfile returns an asset's profile.
func (s *Service) AssetProfile(ctx context.Context, p Principal, code string) (json.RawMessage, error) {
	if err := p.require(domain.PermInstrumentsRead); err != nil {
		return nil, err
	}
	code, err := profileCode(code)
	if err != nil {
		return nil, err
	}
	return s.Catalog.AssetProfile(ctx, code)
}

// UpdateAssetProfile replaces an asset's profile (instruments.write), with
// a reason; audited as admin.instruments.profile_updated on asset:<code>
// with what changed (the logo by its type and size, never its bytes).
func (s *Service) UpdateAssetProfile(ctx context.Context, p Principal, code string, in ProfileInput, reason string) (json.RawMessage, error) {
	if err := p.require(domain.PermInstrumentsEdit); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	code, err := profileCode(code)
	if err != nil {
		return nil, err
	}
	w := ports.ProfileWrite{
		Code: code, DisplayName: strings.TrimSpace(in.DisplayName), Description: in.Description, Links: in.Links, ClearLogo: in.ClearLogo,
		Actor: p.Admin.Email, Reason: strings.TrimSpace(reason),
	}
	logo := "kept"
	switch {
	case in.Logo != "" && in.ClearLogo:
		return nil, apperr.Invalid("send a logo or clear it, not both")
	case in.Logo != "":
		if base64.StdEncoding.DecodedLen(len(in.Logo)) > maxLogo+3 {
			return nil, apperr.Invalid("logo: at most 200 KB")
		}
		if w.Logo, err = base64.StdEncoding.DecodeString(in.Logo); err != nil {
			return nil, apperr.Invalid("logo must be base64")
		}
		w.LogoMIME, logo = in.LogoMIME, "replaced"
	case in.ClearLogo:
		logo = "cleared"
	}
	raw, err := s.Catalog.UpdateAssetProfile(ctx, w)
	if err != nil {
		return nil, err
	}
	details := map[string]any{"display_name": w.DisplayName, "description": w.Description, "links": w.Links, "logo": logo}
	if logo == "replaced" {
		details["logo_mime"], details["logo_bytes"] = w.LogoMIME, len(w.Logo)
	}
	d, _ := json.Marshal(details)
	return raw, s.audit(ctx, p, "asset:"+code, "admin.instruments.profile_updated", w.Reason, string(d))
}
