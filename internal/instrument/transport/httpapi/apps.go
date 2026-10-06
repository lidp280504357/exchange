package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/skill/exchange/internal/instrument/domain"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/httpx"
)

// The apps to download (design 2026-10-07, App download page; H0 contract
// in api/openapi/platform.yaml and api/admin/admin.yaml): the sites' public
// read, and the admin console's reads and changes on /internal (the
// gateway does not route those) - its settings, and the files
// admin-service stored and deletes.
func (h *Handler) appRoutes(r chi.Router) {
	r.Get("/v1/platform/apps", h.publicApps)
	r.Get("/internal/platform/apps", h.internalApps)
	r.Put("/internal/platform/apps/{platform}", h.setApp)
	r.Post("/internal/platform/apps/{platform}/files", h.addAppFile)
	r.Delete("/internal/platform/apps/{platform}/files/{file_id}", h.deleteAppFile)
}

// AppFileJSON is an uploaded file as the console sees it (admin.yaml's
// AppFile).
type AppFileJSON struct {
	FileID      string  `json:"file_id"`
	Kind        string  `json:"kind"`
	Name        string  `json:"name"`
	Size        int64   `json:"size"`
	SHA256      string  `json:"sha256"`
	URL         string  `json:"url"`
	ManifestURL *string `json:"manifest_url"`
	Package     *string `json:"package"`
	Version     *string `json:"version"`
	Build       *string `json:"build"`
	MinOS       *string `json:"min_os"`
	UploadedAt  string  `json:"uploaded_at"`
	UploadedBy  string  `json:"uploaded_by"`
	// StoredAs and Manifest (the paths under /downloads/) are for
	// admin-service, which deletes the files; the console leaves them.
	StoredAs string `json:"stored_as"`
	Manifest string `json:"manifest"`
}

func appFileJSONOf(f domain.AppFileInfo, siteDomain string) AppFileJSON {
	return AppFileJSON{
		FileID: f.FileID, Kind: f.Kind, Name: f.Name, Size: f.Size, SHA256: f.SHA256, URL: domain.FileURL(siteDomain, f.Origin, f.StoredAs),
		ManifestURL: optional(domain.FileURL(siteDomain, f.Origin, f.Manifest)), Package: optional(f.Package), Version: optional(f.Version),
		Build: optional(f.Build), MinOS: optional(f.MinOS), UploadedAt: httpx.FormatTime(f.UploadedAt), UploadedBy: f.UploadedBy,
		StoredAs: f.StoredAs, Manifest: f.Manifest,
	}
}

// AppDownloadJSON is what the sites show of a platform (platform.yaml's
// AppDownload).
type AppDownloadJSON struct {
	Mode            string       `json:"mode"`
	URL             string       `json:"url"`
	InstallURL      *string      `json:"install_url"`
	IOSInstall      *string      `json:"ios_install"`
	Package         *string      `json:"package"`
	Version         *string      `json:"version"`
	Build           *string      `json:"build"`
	MinOS           *string      `json:"min_os"`
	Size            *int64       `json:"size"`
	SHA256          *string      `json:"sha256"`
	MobileconfigURL *string      `json:"mobileconfig_url"`
	Notes           domain.Texts `json:"notes"`
	UpdatedAt       string       `json:"updated_at"`
}

func appDownloadJSONOf(d *domain.AppDownload) *AppDownloadJSON {
	if d == nil {
		return nil
	}
	out := &AppDownloadJSON{
		Mode: d.Mode, URL: d.URL, InstallURL: optional(d.InstallURL), IOSInstall: optional(d.IOSInstall),
		MobileconfigURL: optional(d.MobileconfigURL), Notes: d.Notes, UpdatedAt: httpx.FormatTime(d.UpdatedAt),
	}
	if f := d.File; f != nil {
		out.Package, out.Version, out.Build, out.MinOS = optional(f.Package), optional(f.Version), optional(f.Build), optional(f.MinOS)
		out.Size, out.SHA256 = &f.Size, &f.SHA256
	}
	return out
}

// PlatformAppJSON is a platform as the console sees it (admin.yaml's
// PlatformAppAdmin), with what the sites show of it now.
type PlatformAppJSON struct {
	Platform     string           `json:"platform"`
	Mode         string           `json:"mode"`
	LinkURL      string           `json:"link_url"`
	Enabled      bool             `json:"enabled"`
	Notes        domain.Texts     `json:"notes"`
	Current      *AppFileJSON     `json:"current"`
	Mobileconfig *AppFileJSON     `json:"mobileconfig"`
	Files        []AppFileJSON    `json:"files"`
	Public       *AppDownloadJSON `json:"public"`
	Version      int64            `json:"version"`
	UpdatedAt    string           `json:"updated_at"`
	UpdatedBy    string           `json:"updated_by"`
}

func platformAppJSONOf(a domain.PlatformApp, siteDomain string) PlatformAppJSON {
	a.Normalize()
	file := func(f *domain.AppFileInfo) *AppFileJSON {
		if f == nil {
			return nil
		}
		j := appFileJSONOf(*f, siteDomain)
		return &j
	}
	out := PlatformAppJSON{
		Platform: a.Platform, Mode: a.Mode, LinkURL: a.LinkURL, Enabled: a.Enabled, Notes: a.Notes, Current: file(a.Current),
		Mobileconfig: file(a.Mobileconfig), Files: make([]AppFileJSON, 0, len(a.Files)), Public: appDownloadJSONOf(a.Public(siteDomain)),
		Version: a.Version, UpdatedAt: httpx.FormatTime(a.UpdatedAt), UpdatedBy: a.UpdatedBy,
	}
	for _, f := range a.Files {
		out.Files = append(out.Files, appFileJSONOf(f, siteDomain))
	}
	return out
}

// appsETag is the two platforms' versions, Android's then iOS's: every
// change raises one of them (review FM ②: a sum could come back to a value
// it had). A strong tag; notModified takes it weakened too.
func appsETag(list []domain.PlatformApp) string {
	v := map[string]int64{}
	for _, a := range list {
		v[a.Platform] = a.Version
	}
	return fmt.Sprintf(`"%d-%d"`, v[domain.AppAndroid], v[domain.AppIOS])
}

// apps returns the platforms with the profile's domain, which their files'
// addresses use.
func (h *Handler) apps(r *http.Request) ([]domain.PlatformApp, string, error) {
	list, err := h.Apps.List(r.Context())
	if err != nil {
		return nil, "", err
	}
	p, err := h.Platform.Profile(r.Context())
	if err != nil {
		return nil, "", err
	}
	return list, p.Domain, nil
}

// publicApps serves the apps to the sites: cacheable for a minute, 304 for
// the ETag they hold.
func (h *Handler) publicApps(w http.ResponseWriter, r *http.Request) {
	list, siteDomain, err := h.apps(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	tag := appsETag(list)
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Header().Set("ETag", tag)
	if notModified(r, tag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	out := map[string]*AppDownloadJSON{"android": nil, "ios": nil}
	for _, a := range list {
		out[strings.ToLower(a.Platform)] = appDownloadJSONOf(a.Public(siteDomain))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) internalApps(w http.ResponseWriter, r *http.Request) {
	list, siteDomain, err := h.apps(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := make([]PlatformAppJSON, 0, len(list))
	for _, a := range list {
		out = append(out, platformAppJSONOf(a, siteDomain))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"apps": out})
}

// writeApp answers a platform as the console sees it, with the file a
// change replaced or removed (for admin-service to delete) under key.
func (h *Handler) writeApp(w http.ResponseWriter, r *http.Request, a domain.PlatformApp, key string, f *domain.AppFileInfo) {
	p, err := h.Platform.Profile(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	out := map[string]any{"app": platformAppJSONOf(a, p.Domain)}
	if key != "" {
		var file *AppFileJSON
		if f != nil {
			j := appFileJSONOf(*f, p.Domain)
			file = &j
		}
		out[key] = file
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) setApp(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode            string       `json:"mode"`
		LinkURL         string       `json:"link_url"`
		Notes           domain.Texts `json:"notes"`
		Enabled         *bool        `json:"enabled"`
		ExpectedVersion *int64       `json:"expected_version"`
		Actor           string       `json:"actor"`
		Reason          string       `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if body.ExpectedVersion == nil || body.Enabled == nil {
		httpx.WriteError(w, r, apperr.Invalid("enabled and expected_version are required"))
		return
	}
	s := domain.AppSetting{Mode: body.Mode, LinkURL: body.LinkURL, Notes: body.Notes, Enabled: *body.Enabled}
	a, err := h.Apps.Set(r.Context(), chi.URLParam(r, "platform"), s, *body.ExpectedVersion, body.Actor, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.writeApp(w, r, a, "", nil)
}

// appFileWriteJSON is a file admin-service stored, as it reports it.
type appFileWriteJSON struct {
	FileID     string `json:"file_id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	StoredAs   string `json:"stored_as"`
	Manifest   string `json:"manifest"`
	Origin     string `json:"origin"`
	Package    string `json:"package"`
	Version    string `json:"version"`
	Build      string `json:"build"`
	MinOS      string `json:"min_os"`
	UploadedAt string `json:"uploaded_at"`
	UploadedBy string `json:"uploaded_by"`
}

func (h *Handler) addAppFile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		File   appFileWriteJSON `json:"file"`
		Actor  string           `json:"actor"`
		Reason string           `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	f := body.File
	at, err := time.Parse(time.RFC3339Nano, f.UploadedAt)
	if err != nil {
		httpx.WriteError(w, r, apperr.Invalid("file.uploaded_at: RFC 3339"))
		return
	}
	info := domain.AppFileInfo{
		FileID: f.FileID, Kind: f.Kind, Name: f.Name, Size: f.Size, SHA256: f.SHA256, StoredAs: f.StoredAs, Manifest: f.Manifest,
		Origin: f.Origin, Package: f.Package, Version: f.Version, Build: f.Build, MinOS: f.MinOS, UploadedAt: at, UploadedBy: f.UploadedBy,
	}
	a, replaced, err := h.Apps.AddFile(r.Context(), chi.URLParam(r, "platform"), info, body.Actor, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.writeApp(w, r, a, "replaced", replaced)
}

func (h *Handler) deleteAppFile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Actor  string `json:"actor"`
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	a, gone, err := h.Apps.DeleteFile(r.Context(), chi.URLParam(r, "platform"), chi.URLParam(r, "file_id"), body.Actor, body.Reason)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.writeApp(w, r, a, "removed", &gone)
}
