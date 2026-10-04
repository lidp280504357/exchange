package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// profileCatalog answers as instrument-service does and records the
// profile writes.
type profileCatalog struct {
	ports.Instruments
	writes []ports.ProfileWrite
}

func (c *profileCatalog) AssetProfile(_ context.Context, code string) (json.RawMessage, error) {
	return json.RawMessage(`{"display_name":"` + code + `","version":3}`), nil
}

func (c *profileCatalog) UpdateAssetProfile(_ context.Context, w ports.ProfileWrite) (json.RawMessage, error) {
	c.writes = append(c.writes, w)
	return json.Marshal(map[string]any{"display_name": w.DisplayName, "version": len(c.writes) + 3})
}

func TestAnAssetsProfileFromTheConsole(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	catalog := &profileCatalog{}
	h.svc.Catalog = catalog
	h.admin(t, "ops@example.com", domain.RoleOperator)
	h.admin(t, "aud@example.com", domain.RoleAuditor)
	ops, aud := h.login(t, "ops@example.com"), h.login(t, "aud@example.com")

	if raw, err := h.svc.AssetProfile(ctx, aud, "astra"); err != nil || !strings.Contains(string(raw), `"ASTRA"`) {
		t.Fatalf("every administrator reads a profile: %s %v", raw, err)
	}
	if _, err := h.svc.AssetProfile(ctx, aud, "../x"); code(err) != apperr.CodeNotFound {
		t.Fatalf("not an asset code: %v", err)
	}
	in := ProfileInput{DisplayName: " Astra ", Description: map[string]string{"zh-CN": "平台币"}, Links: map[string]string{"website": "https://astras.vip"}}
	if _, err := h.svc.UpdateAssetProfile(ctx, aud, "ASTRA", in, "rename the coin"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("AUDITOR edits no profile: %v", err)
	}
	if _, err := h.svc.UpdateAssetProfile(ctx, ops, "ASTRA", in, " x "); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("a reason is needed: %v", err)
	}
	both := in
	both.Logo, both.LogoMIME, both.ClearLogo = base64.StdEncoding.EncodeToString([]byte("<svg/>")), "image/svg+xml", true
	if _, err := h.svc.UpdateAssetProfile(ctx, ops, "ASTRA", both, "a logo and none"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("a logo and its removal: %v", err)
	}
	bad := in
	bad.Logo = "not base64!"
	if _, err := h.svc.UpdateAssetProfile(ctx, ops, "ASTRA", bad, "a broken upload"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("a logo not in base64: %v", err)
	}
	big := in
	big.Logo = base64.StdEncoding.EncodeToString(make([]byte, 300<<10))
	if _, err := h.svc.UpdateAssetProfile(ctx, ops, "ASTRA", big, "a logo too large"); code(err) != apperr.CodeInvalidArgument || len(catalog.writes) != 0 {
		t.Fatalf("a logo above 200 KB: %v", err)
	}
	logo := in
	logo.Logo, logo.LogoMIME = base64.StdEncoding.EncodeToString([]byte(`<svg viewBox="0 0 64 64"/>`)), "image/svg+xml"
	if _, err := h.svc.UpdateAssetProfile(ctx, ops, "astra", logo, "the new logo of the coin"); err != nil {
		t.Fatal(err)
	}
	w := catalog.writes[0]
	if w.Code != "ASTRA" || w.DisplayName != "Astra" || string(w.Logo) != `<svg viewBox="0 0 64 64"/>` || w.Actor != "ops@example.com" ||
		w.Reason != "the new logo of the coin" {
		t.Fatalf("written %+v", w)
	}
	last := h.store.audits[len(h.store.audits)-1]
	if last.GetAction() != "admin.instruments.profile_updated" || last.GetTarget() != "asset:ASTRA" ||
		!strings.Contains(last.GetDetails(), `"logo":"replaced"`) || !strings.Contains(last.GetDetails(), `"logo_bytes":26`) ||
		strings.Contains(last.GetDetails(), "svg viewBox") {
		t.Fatalf("audited without the logo's bytes: %v", last)
	}
}
