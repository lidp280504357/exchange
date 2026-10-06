package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/instrument/adapters/postgres"
	"github.com/skill/exchange/internal/instrument/application"
	"github.com/skill/exchange/internal/instrument/domain"
	"github.com/skill/exchange/internal/instrument/transport/httpapi"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
)

// The apps to download (design 2026-10-07): the seeded rows (OFF, version
// 1), a link on the version read, files kept and deleted with the history,
// and what the sites and the console read - the ETag the two versions
// (review FM ②), 304 for it weakened, a change raising it.
func TestPlatformApps(t *testing.T) {
	_, db := setup(t)
	ctx := context.Background()
	store := postgres.NewStore(db, event.NewFactory("instrument-service", "test"))
	plat := &application.Platform{Store: store, Now: time.Now}
	apps := &application.Apps{Store: store, Now: time.Now}

	list, err := apps.List(ctx)
	if err != nil || len(list) != 2 || list[0].Platform != domain.AppAndroid || list[1].Platform != domain.AppIOS || list[0].Mode != domain.AppOff ||
		list[0].Version != 1 || list[1].Enabled || len(list[1].Files) != 0 || list[0].Notes[domain.LocaleTW] != "" {
		t.Fatalf("seeded %+v %v", list, err)
	}

	r := chi.NewRouter()
	(&httpapi.Handler{Svc: &application.Service{Store: store}, Platform: plat, Apps: apps}).Routes(r)
	get := func(path, etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	w := get("/v1/platform/apps", "")
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"android":null,"ios":null}` || w.Header().Get("ETag") != `"1-1"` ||
		w.Header().Get("Cache-Control") != "public, max-age=60" {
		t.Fatalf("nothing shown %d %s %s", w.Code, w.Body, w.Header().Get("ETag"))
	}
	if w := get("/v1/platform/apps", `W/"1-1"`); w.Code != http.StatusNotModified {
		t.Fatalf("with its ETag weakened: %d", w.Code)
	}

	link := domain.AppSetting{
		Mode: domain.AppLink, LinkURL: "https://apps.apple.com/app/id6400000000", Enabled: true,
		Notes: domain.Texts{domain.LocaleZH: "首个版本", domain.LocaleEN: "The first"},
	}
	ios, err := apps.Set(ctx, domain.AppIOS, link, 1, "admin:ops@example.com", "on the App Store")
	if err != nil || ios.Version != 2 || ios.Mode != domain.AppLink || !ios.Enabled || ios.UpdatedBy != "admin:ops@example.com" {
		t.Fatalf("a link %+v %v", ios, err)
	}
	if _, err := apps.Set(ctx, domain.AppIOS, link, 1, "admin:b@example.com", "stale"); !apperr.Is(err, "INSTRUMENT_PLATFORM_CHANGED") {
		t.Fatalf("on a stale version: %v", err)
	}
	if _, err := apps.Set(ctx, "WINDOWS", link, 1, "admin:b@example.com", "no such"); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("no such platform: %v", err)
	}
	if _, err := apps.Set(ctx, domain.AppAndroid, domain.AppSetting{Mode: domain.AppFile}, 1, "admin:b@example.com", "nothing up"); !apperr.Is(err,
		apperr.CodeInvalidArgument) {
		t.Fatalf("FILE without an app: %v", err)
	}
	w = get("/v1/platform/apps", `"1-1"`)
	var public struct {
		Android *httpapi.AppDownloadJSON `json:"android"`
		IOS     *httpapi.AppDownloadJSON `json:"ios"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &public); err != nil || w.Code != http.StatusOK || w.Header().Get("ETag") != `"1-2"` ||
		public.Android != nil || public.IOS == nil || public.IOS.Mode != "LINK" || public.IOS.URL != link.LinkURL ||
		*public.IOS.IOSInstall != "APP_STORE" || public.IOS.Size != nil || public.IOS.Notes[domain.LocaleZH] != "首个版本" {
		t.Fatalf("a link shown %d %s", w.Code, w.Body)
	}

	// An app stored by admin-service: current, FILE, served at the
	// upload's site while the profile has no domain.
	id := "0192a000-0000-7000-8000-0000000000a1"
	apk := domain.AppFileInfo{
		FileID: id, Kind: domain.AppKindApp, Name: "Astras-1.2.0.apk", Size: 2048, SHA256: strings.Repeat("0f", 32),
		StoredAs: "android/" + id + ".apk", Origin: "https://astras.vip", Package: "vip.astras.app", Version: "1.2.0", Build: "42", MinOS: "24",
		UploadedAt: time.Now(), UploadedBy: "ops@example.com",
	}
	android, replaced, err := apps.AddFile(ctx, domain.AppAndroid, apk, "admin:ops@example.com", "1.2.0")
	if err != nil || replaced != nil || android.Version != 2 || android.Mode != domain.AppFile || android.Current == nil || android.Current.Build != "42" ||
		len(android.Files) != 1 || android.Enabled {
		t.Fatalf("an apk %+v %v", android, err)
	}
	if _, _, err := apps.AddFile(ctx, domain.AppAndroid, apk, "admin:ops@example.com", "again"); !apperr.Is(err, apperr.CodeConflict) {
		t.Fatalf("the same file again: %v", err)
	}
	if _, err := apps.Set(ctx, domain.AppAndroid, domain.AppSetting{Mode: domain.AppFile, Enabled: true}, 2, "admin:ops@example.com", "shown"); err != nil {
		t.Fatal(err)
	}
	w = get("/internal/platform/apps", "")
	var console struct {
		Apps []httpapi.PlatformAppJSON `json:"apps"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &console); err != nil || len(console.Apps) != 2 {
		t.Fatalf("the console's list %s %v", w.Body, err)
	}
	a := console.Apps[0]
	if a.Platform != "ANDROID" || a.Version != 3 || a.Current == nil || a.Current.URL != "https://astras.vip/downloads/android/"+id+".apk" ||
		a.Current.ManifestURL != nil || *a.Current.Package != "vip.astras.app" || a.Public == nil || a.Public.Mode != "FILE" ||
		*a.Public.Size != 2048 || a.Public.InstallURL != nil || a.Public.IOSInstall != nil || len(a.Files) != 1 || a.UpdatedBy != "admin:ops@example.com" {
		t.Fatalf("Android in the console %s", w.Body)
	}

	// Deleted, Android goes back to OFF (it has no link); the history keeps
	// each change.
	android, gone, err := apps.DeleteFile(ctx, domain.AppAndroid, id, "admin:ops@example.com", "withdrawn")
	if err != nil || gone.FileID != id || android.Mode != domain.AppOff || android.Current != nil || len(android.Files) != 0 || android.Version != 4 {
		t.Fatalf("deleted %+v %v", android, err)
	}
	if _, _, err := apps.DeleteFile(ctx, domain.AppAndroid, id, "admin:ops@example.com", "again"); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("deleted twice: %v", err)
	}
	if w := get("/v1/platform/apps", `"1-2"`); w.Code != http.StatusOK || w.Header().Get("ETag") != `"4-2"` {
		t.Fatalf("after the changes: %d %s", w.Code, w.Header().Get("ETag"))
	}
	if n := count(t, db, `SELECT count(*) FROM config_history WHERE entity = 'PLATFORM_APP'`); n != 4 {
		t.Fatalf("%d history rows", n)
	}
}
