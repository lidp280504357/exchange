package backends

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/skill/exchange/internal/admin/ports"
)

// The apps to download on instrument-service's internal API (design
// 2026-10-07, App download page §7 #9); Platform implements
// ports.PlatformApps too.

// Apps returns both platforms as the console sees them.
func (p Platform) Apps(ctx context.Context) (json.RawMessage, error) {
	return p.do(ctx, http.MethodGet, p.Instruments+"/internal/platform/apps", nil, nil)
}

// SetApp changes a platform's mode, link, notes and switch.
func (p Platform) SetApp(ctx context.Context, platform string, write json.RawMessage, actor, reason string) (json.RawMessage, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(write, &body); err != nil {
		return nil, err
	}
	body["actor"], _ = json.Marshal(actor)
	body["reason"], _ = json.Marshal(reason)
	return p.do(ctx, http.MethodPut, p.Instruments+"/internal/platform/apps/"+url.PathEscape(platform), body, nil)
}

// AddAppFile has a stored file kept.
func (p Platform) AddAppFile(ctx context.Context, platform string, f ports.StoredAppFile, actor, reason string) (json.RawMessage, error) {
	body := map[string]any{"file": f, "actor": actor, "reason": reason}
	return p.do(ctx, http.MethodPost, p.Instruments+"/internal/platform/apps/"+url.PathEscape(platform)+"/files", body, nil)
}

// DeleteAppFile has a file forgotten.
func (p Platform) DeleteAppFile(ctx context.Context, platform, fileID, actor, reason string) (json.RawMessage, error) {
	body := map[string]string{"actor": actor, "reason": reason}
	return p.do(ctx, http.MethodDelete,
		p.Instruments+"/internal/platform/apps/"+url.PathEscape(platform)+"/files/"+url.PathEscape(fileID), body, nil)
}
