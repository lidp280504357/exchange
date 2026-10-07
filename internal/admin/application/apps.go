package application

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/platform/apperr"
)

// The apps to download (design 2026-10-07, App download page; batch H1):
// each platform's settings live in instrument-service; the files are
// uploaded here in parts, checked and stored for nginx to serve under
// /downloads/, then kept by instrument-service. One administrator with
// settings.write does it alone, audited (§2.3): the settings as
// admin.platform.app_updated, a file as admin.platform.app_file_uploaded
// and admin.platform.app_file_deleted.

const (
	// appMaxFiles is instrument-service's bound on a platform's files,
	// checked before an upload starts.
	appMaxFiles = 10
	// appDiskReserve is the room an upload leaves on the server's disk
	// beyond its parts and its file (the services' data is on it too).
	appDiskReserve = 2 << 30
	// appOrphanAge is how old a file under /downloads/ that no platform
	// keeps, or an upload's directory without its row, must be before the
	// sweep deletes it (one being written is younger).
	appOrphanAge = time.Hour
	// appPartHold is how long a part being written holds its upload
	// against a completion or a drop (review FX, A74 ③).
	appPartHold = 2 * time.Minute
)

// appHostRE is a host name the console may be reached at.
var appHostRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// appTarget is a platform's audit target.
func appTarget(platform string) string { return "app:" + platform }

// appFileJSON is what admin-service reads of a file instrument-service
// answers: where it is, for deleting it, and what to audit.
type appFileJSON struct {
	FileID   string `json:"file_id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	StoredAs string `json:"stored_as"`
	Manifest string `json:"manifest"`
}

// appAnswer is instrument-service's answer to a change: the platform, and
// the file a change replaced or removed.
type appAnswer struct {
	App      json.RawMessage `json:"app"`
	Replaced *appFileJSON    `json:"replaced"`
	Removed  *appFileJSON    `json:"removed"`
}

// appState is what admin-service reads of a platform: its settings (for
// the audit), its version and its files.
type appState struct {
	Platform string          `json:"platform"`
	Mode     string          `json:"mode"`
	LinkURL  string          `json:"link_url"`
	Enabled  bool            `json:"enabled"`
	Notes    json.RawMessage `json:"notes"`
	Version  int64           `json:"version"`
	Mobile   *appFileJSON    `json:"mobileconfig"`
	Files    []appFileJSON   `json:"files"`
}

func validAppPlatform(platform string) error {
	if platform != domain.AppAndroid && platform != domain.AppIOS {
		return apperr.NotFound("no such platform: ANDROID or IOS")
	}
	return nil
}

func (s *Service) appsReady() error {
	if s.Apps == nil || s.AppFiles == nil || s.AppUploads == nil {
		return apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "the app downloads are not configured")
	}
	return nil
}

// appStates reads both platforms as instrument-service keeps them.
func (s *Service) appStates(ctx context.Context) ([]appState, error) {
	raw, err := s.Apps.Apps(ctx)
	if err != nil {
		return nil, err
	}
	var list struct {
		Apps []appState `json:"apps"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "instrument-service answered the apps in another shape")
	}
	return list.Apps, nil
}

func (s *Service) appState(ctx context.Context, platform string) (appState, error) {
	list, err := s.appStates(ctx)
	if err != nil {
		return appState{}, err
	}
	for _, a := range list {
		if a.Platform == platform {
			return a, nil
		}
	}
	return appState{}, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "instrument-service has no "+platform)
}

// PlatformApps returns both platforms as the console shows them; every
// administrator reads them.
func (s *Service) PlatformApps(ctx context.Context, p Principal) (json.RawMessage, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return nil, err
	}
	if err := s.appsReady(); err != nil {
		return nil, err
	}
	return s.Apps.Apps(ctx)
}

// SetPlatformApp changes a platform's mode, link, notes and switch as of
// the version write carries; audited with the fields that changed.
func (s *Service) SetPlatformApp(ctx context.Context, p Principal, platform string, write json.RawMessage, reason string) (json.RawMessage, error) {
	if err := p.require(domain.PermSettingsEdit); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	if err := validAppPlatform(platform); err != nil {
		return nil, err
	}
	if err := s.appsReady(); err != nil {
		return nil, err
	}
	before, err := s.appState(ctx, platform)
	if err != nil {
		return nil, err
	}
	raw, err := s.Apps.SetApp(ctx, platform, write, p.Admin.Email, strings.TrimSpace(reason))
	if err != nil {
		return nil, err
	}
	var out appAnswer
	var after appState
	if err := json.Unmarshal(raw, &out); err != nil || json.Unmarshal(out.App, &after) != nil {
		return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "instrument-service answered the app in another shape")
	}
	changes := map[string]map[string]any{}
	for field, v := range map[string][2]any{
		"mode": {before.Mode, after.Mode}, "link_url": {before.LinkURL, after.LinkURL}, "enabled": {before.Enabled, after.Enabled},
		"notes": {before.Notes, after.Notes},
	} {
		b, _ := json.Marshal(v[0])
		a, _ := json.Marshal(v[1])
		if !jsonEqual(b, a) {
			changes[field] = map[string]any{"old": json.RawMessage(b), "new": json.RawMessage(a)}
		}
	}
	details, _ := json.Marshal(map[string]any{"platform": platform, "changes": changes, "version": after.Version})
	return out.App, s.audit(ctx, p, appTarget(platform), "admin.platform.app_updated", strings.TrimSpace(reason), string(details))
}

// upload returns an upload of the platform still open; 404 otherwise.
func (s *Service) upload(ctx context.Context, platform, id string) (domain.AppUpload, error) {
	if err := validAppPlatform(platform); err != nil {
		return domain.AppUpload{}, err
	}
	if err := s.appsReady(); err != nil {
		return domain.AppUpload{}, err
	}
	if uuid.Validate(id) != nil {
		return domain.AppUpload{}, apperr.NotFound("no such upload")
	}
	u, err := s.AppUploads.Get(ctx, id)
	if err != nil {
		return domain.AppUpload{}, err
	}
	if u == nil || u.Platform != platform || !u.ExpiresAt.After(s.Now()) {
		return domain.AppUpload{}, apperr.NotFound("no such upload: completed, dropped or expired")
	}
	return *u, nil
}

// StartAppUpload opens an upload of an app or iOS's configuration profile:
// at most domain.AppUploadsOpen at a time, with room on the disk for its
// parts and its file, on a platform with room for another file.
func (s *Service) StartAppUpload(ctx context.Context, p Principal, platform, kind, name string, size int64, sha256 string) (domain.AppUpload, error) {
	if err := p.require(domain.PermSettingsEdit); err != nil {
		return domain.AppUpload{}, err
	}
	if err := s.appsReady(); err != nil {
		return domain.AppUpload{}, err
	}
	now := s.Now()
	u, err := domain.NewAppUpload(uuid.Must(uuid.NewV7()).String(), platform, kind, strings.TrimSpace(name), size, sha256, p.Admin.Email, now)
	if err != nil {
		return domain.AppUpload{}, err
	}
	open, err := s.AppUploads.Open(ctx, now)
	if err != nil {
		return domain.AppUpload{}, err
	}
	if open >= domain.AppUploadsOpen {
		return domain.AppUpload{}, domain.ErrAppUploadsFull
	}
	free, err := s.AppFiles.Free()
	if err != nil {
		return domain.AppUpload{}, err
	}
	if free < uint64(2*size+appDiskReserve) { //nolint:gosec // size is checked positive and at most 500 MiB
		return domain.AppUpload{}, domain.ErrAppDiskFull.WithDetail("free", free)
	}
	state, err := s.appState(ctx, platform)
	if err != nil {
		return domain.AppUpload{}, err
	}
	replacing := kind == domain.AppKindMobileconfig && state.Mobile != nil
	if len(state.Files) >= appMaxFiles && !replacing {
		return domain.AppUpload{}, apperr.New(apperr.KindConflict, "PLATFORM_APP_FILES_FULL",
			"the platform keeps 10 files: delete one first")
	}
	if err := s.AppUploads.Create(ctx, u); err != nil {
		return domain.AppUpload{}, err
	}
	return u, nil
}

// AppUpload returns an upload with the parts it has, to resume it.
func (s *Service) AppUpload(ctx context.Context, p Principal, platform, id string) (domain.AppUpload, error) {
	if err := p.require(domain.PermSettingsEdit); err != nil {
		return domain.AppUpload{}, err
	}
	return s.upload(ctx, platform, id)
}

// PutAppUploadPart stores part n of an upload: exactly its length (the
// part size, the last part the rest). It holds the upload while it writes,
// so a completion never joins a part half written, nor a part lands after
// the upload is gone; another part meanwhile is refused as busy.
func (s *Service) PutAppUploadPart(ctx context.Context, p Principal, platform, id string, n int, body io.Reader) (domain.AppUpload, error) {
	if err := p.require(domain.PermSettingsEdit); err != nil {
		return domain.AppUpload{}, err
	}
	u, err := s.upload(ctx, platform, id)
	if err != nil {
		return domain.AppUpload{}, err
	}
	size := u.PartLen(n)
	if size == 0 {
		return domain.AppUpload{}, apperr.Invalid("no such part: this upload has parts 1 to " + strconv.Itoa(u.Parts()))
	}
	now := s.Now()
	if ok, err := s.AppUploads.Claim(ctx, id, now, now.Add(appPartHold)); err != nil || !ok {
		return domain.AppUpload{}, cmpErr(err, domain.ErrAppUploadBusy)
	}
	defer s.releaseUpload(ctx, id)
	if err := s.AppFiles.PutPart(ctx, id, n, body, size); err != nil {
		return domain.AppUpload{}, err
	}
	got, err := s.AppUploads.Received(ctx, id, n)
	if err != nil {
		return domain.AppUpload{}, err
	}
	if got == nil {
		return domain.AppUpload{}, apperr.NotFound("no such upload: completed, dropped or expired")
	}
	return *got, nil
}

// DropAppUpload removes an upload and its parts.
func (s *Service) DropAppUpload(ctx context.Context, p Principal, platform, id string) error {
	if err := p.require(domain.PermSettingsEdit); err != nil {
		return err
	}
	if _, err := s.upload(ctx, platform, id); err != nil {
		return err
	}
	now := s.Now()
	if ok, err := s.AppUploads.Claim(ctx, id, now, now.Add(appPartHold)); err != nil || !ok {
		return cmpErr(err, domain.ErrAppUploadBusy)
	}
	return s.dropUpload(ctx, id)
}

// releaseUpload lets the next part, completion or drop take an upload.
func (s *Service) releaseUpload(ctx context.Context, id string) {
	if err := s.AppUploads.Release(context.WithoutCancel(ctx), id); err != nil {
		s.Log.WarnContext(ctx, "app upload: not released", "upload_id", id, "error", err)
	}
}

// dropUpload removes an upload's parts, then its row: a crash in between
// leaves a row the expiry sweep removes.
func (s *Service) dropUpload(ctx context.Context, id string) error {
	if err := s.AppFiles.DropUpload(id); err != nil {
		return err
	}
	return s.AppUploads.Delete(ctx, id)
}

// cmpErr is err when there is one, else the refusal.
func cmpErr(err, refusal error) error {
	if err != nil {
		return err
	}
	return refusal
}

// appOrigin is the site the files are served at, https://<host>: the
// profile's domain, else the site of the console's host (admin.<site> is
// <site>); with the name an app whose package has none is shown by.
func (s *Service) appOrigin(ctx context.Context, host string) (string, string, error) {
	raw, err := s.Platform.Profile(ctx)
	if err != nil {
		return "", "", err
	}
	var prof struct {
		Name   string `json:"name"`
		Domain string `json:"domain"`
	}
	_ = json.Unmarshal(raw, &prof)
	site := prof.Domain
	if site == "" {
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		site = strings.TrimPrefix(strings.ToLower(host), "admin.")
	}
	if !appHostRE.MatchString(site) {
		return "", "", apperr.Invalid("the profile has no domain and the console's site is not a host name: set the domain in the platform settings")
	}
	return "https://" + site, prof.Name, nil
}

// CompleteAppUpload joins an upload's parts, checks the file, stores it
// under /downloads/ and has instrument-service keep it: an app becomes the
// platform's current one, a configuration profile replaces iOS's (whose
// file goes). A file that is not what it claims drops the upload; any
// other failure keeps it for another try.
func (s *Service) CompleteAppUpload(ctx context.Context, p Principal, platform, id, reason, host string) (json.RawMessage, error) {
	if err := p.require(domain.PermSettingsEdit); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	u, err := s.upload(ctx, platform, id)
	if err != nil {
		return nil, err
	}
	if missing := u.Missing(); len(missing) > 0 {
		return nil, domain.ErrAppUploadIncomplete.WithDetail("missing", missing)
	}
	now := s.Now()
	ok, err := s.AppUploads.Claim(ctx, id, now, now.Add(domain.AppCompleteHold))
	if err != nil || !ok {
		return nil, cmpErr(err, domain.ErrAppUploadBusy)
	}
	// Gone when it completed; released for another try otherwise.
	defer s.releaseUpload(ctx, id)
	origin, title, err := s.appOrigin(ctx, host)
	if err != nil {
		return nil, err
	}
	fileID := uuid.Must(uuid.NewV7()).String()
	stored, err := s.AppFiles.Store(ctx, u, fileID, origin, title)
	if apperr.Is(err, "PLATFORM_APP_FILE_INVALID") {
		if derr := s.dropUpload(context.WithoutCancel(ctx), id); derr != nil {
			s.Log.WarnContext(ctx, "app upload: an invalid one not dropped", "upload_id", id, "error", derr)
		}
	}
	if err != nil {
		return nil, err
	}
	stored.UploadedAt, stored.UploadedBy = now.UTC().Format(time.RFC3339Nano), p.Admin.Email
	reason = strings.TrimSpace(reason)
	raw, err := s.Apps.AddAppFile(ctx, platform, stored, p.Admin.Email, reason)
	var out appAnswer
	if err == nil && json.Unmarshal(raw, &out) != nil {
		err = apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "instrument-service answered the app in another shape")
	}
	if err != nil {
		// The answer may be lost with the file kept: a file no platform
		// keeps is deleted now (or by the sweep when that cannot be told).
		if state, rerr := s.appState(context.WithoutCancel(ctx), platform); rerr == nil &&
			!slices.ContainsFunc(state.Files, func(f appFileJSON) bool { return f.FileID == fileID }) {
			_ = s.AppFiles.Remove(stored.StoredAs, stored.Manifest)
		}
		return nil, err
	}
	if r := out.Replaced; r != nil {
		if err := s.AppFiles.Remove(r.StoredAs, r.Manifest); err != nil {
			s.Log.WarnContext(ctx, "app files: the replaced profile not deleted", "file_id", r.FileID, "error", err)
		}
	}
	if err := s.dropUpload(context.WithoutCancel(ctx), id); err != nil {
		s.Log.WarnContext(ctx, "app upload: completed, not dropped", "upload_id", id, "error", err)
	}
	details := map[string]any{
		"platform": platform, "kind": u.Kind, "file_id": fileID, "name": u.Name, "size": stored.Size, "sha256": stored.SHA256,
		"package": stored.Package, "version": stored.Version, "build": stored.Build, "min_os": stored.MinOS,
	}
	if out.Replaced != nil {
		details["replaced"] = out.Replaced.FileID
	}
	d, _ := json.Marshal(details)
	return out.App, s.audit(ctx, p, appTarget(platform), "admin.platform.app_file_uploaded", reason, string(d))
}

// DeleteAppFile deletes a file: instrument-service forgets it (the current
// app gone, the platform goes back to its link or OFF), then the disk.
func (s *Service) DeleteAppFile(ctx context.Context, p Principal, platform, fileID, reason string) (json.RawMessage, error) {
	if err := p.require(domain.PermSettingsEdit); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	if err := validAppPlatform(platform); err != nil {
		return nil, err
	}
	if err := s.appsReady(); err != nil {
		return nil, err
	}
	if uuid.Validate(fileID) != nil {
		return nil, apperr.NotFound("no such file")
	}
	reason = strings.TrimSpace(reason)
	raw, err := s.Apps.DeleteAppFile(ctx, platform, fileID, p.Admin.Email, reason)
	if err != nil {
		return nil, err
	}
	var out appAnswer
	if err := json.Unmarshal(raw, &out); err != nil || out.Removed == nil {
		return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "instrument-service answered the app in another shape")
	}
	gone := out.Removed
	if err := s.AppFiles.Remove(gone.StoredAs, gone.Manifest); err != nil {
		// The sweep deletes it later: no platform keeps it.
		s.Log.WarnContext(ctx, "app files: a deleted file stays on the disk", "file_id", fileID, "error", err)
	}
	d, _ := json.Marshal(map[string]any{
		"platform": platform, "kind": gone.Kind, "file_id": fileID, "name": gone.Name, "size": gone.Size, "sha256": gone.SHA256,
	})
	return out.App, s.audit(ctx, p, appTarget(platform), "admin.platform.app_file_deleted", reason, string(d))
}

// SweepAppFiles drops the uploads expired with their parts and the
// uploads' directories without a row, and deletes the files under
// /downloads/ no platform keeps, those two once appOrphanAge old (a
// completion whose answer was lost, a deletion whose file stayed); it
// returns how many of each.
func (s *Service) SweepAppFiles(ctx context.Context) (uploads, files int, err error) {
	if s.appsReady() != nil {
		return 0, 0, nil
	}
	now := s.Now()
	expired, err := s.AppUploads.Expired(ctx, now)
	if err != nil {
		return 0, 0, err
	}
	for _, u := range expired {
		if err := s.dropUpload(ctx, u.ID); err != nil {
			return uploads, 0, err
		}
		uploads++
	}
	// A directory whose row went (a crash between the two, or the row
	// dropped by hand) goes too once it is old (review FX, A74 ④).
	dirs, err := s.AppFiles.UploadDirs()
	if err != nil {
		return uploads, 0, err
	}
	for _, d := range dirs {
		if now.Sub(d.ModTime) < appOrphanAge {
			continue
		}
		if row, err := s.AppUploads.Get(ctx, d.Path); err != nil || row != nil {
			continue
		}
		if err := s.AppFiles.DropUpload(d.Path); err != nil {
			return uploads, 0, err
		}
		uploads++
	}
	states, err := s.appStates(ctx)
	if err != nil {
		return uploads, 0, err
	}
	kept := map[string]bool{}
	for _, a := range states {
		for _, f := range a.Files {
			kept[f.StoredAs], kept[f.Manifest] = true, true
		}
	}
	stored, err := s.AppFiles.Stored()
	if err != nil {
		return uploads, 0, err
	}
	for _, f := range stored {
		if kept[f.Path] || now.Sub(f.ModTime) < appOrphanAge {
			continue
		}
		if err := s.AppFiles.Remove(f.Path); err != nil {
			return uploads, files, err
		}
		files++
	}
	return uploads, files, nil
}
